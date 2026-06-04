package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"

	"cloud.google.com/go/spanner"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/apstndb/spannerplan/plantree/reference"
	"github.com/apstndb/spanpg"
	"github.com/apstndb/spantype"
	"github.com/apstndb/spanvalue"
	spanwriter "github.com/apstndb/spanvalue/writer"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
)

// spannerCLIReadableFormatConfig is Spanner CLI-compatible cell text with tuple STRUCT
// parentheses (not bracket style). Shared by GoogleSQL table cells and CSV export.
var spannerCLIReadableFormatConfig = func() *spanvalue.FormatConfig {
	fc := spanvalue.SpannerCLICompatibleFormatConfig().Clone()
	fc.FormatStruct.FormatStructParen = spanvalue.FormatTupleStruct
	return fc
}()

func writeStringWithTrailingNewline(out io.Writer, s string) {
	fmt.Fprint(out, s)
	if !strings.HasSuffix(s, "\n") {
		fmt.Fprintln(out)
	}
}

// renderQueryResultData prints one statement's rows (table, csv, or jsonl) and returns the row count for summaries.
func renderQueryResultData(out io.Writer, rsm *sppb.ResultSetMetadata, rows *sql.Rows, format outputFormat, dialect databasepb.DatabaseDialect) (int, error) {
	switch format {
	case outputFormatCSV:
		return renderResultSetCSV(out, rsm, rows)
	case outputFormatJSONL:
		return renderResultSetJSONL(out, rsm, rows)
	default:
		result, n, err := renderResultSetTable(rsm, rows, dialect)
		if err != nil {
			return 0, err
		}
		writeStringWithTrailingNewline(out, result)
		return n, nil
	}
}

// renderQueryPlan prints EXPLAIN (PLAN) or EXPLAIN ANALYZE (PROFILE) output. PLAN does not populate
// QueryStats in general; we only show the plan tree. PROFILE shows the plan first, then row count, then query_stats when the API returns it.
func renderQueryPlan(out io.Writer, rows *sql.Rows, drainedRowCount int, kind stmtDisplayKind, verbose bool) error {
	rss, err := fetchResultSetStatsAfterDataRows(rows)
	if err != nil {
		return err
	}
	if rss == nil {
		return errors.New("no result set for query stats")
	}
	if nodes := rss.GetQueryPlan().GetPlanNodes(); len(nodes) > 0 {
		rendered, err := reference.RenderTreeTableWithOptions(nodes, reference.RenderModeAuto, reference.FormatCurrent,
			reference.WithWrapWidth(0),
			reference.WithHangingIndent(),
		)
		if err != nil {
			return err
		}
		writeStringWithTrailingNewline(out, rendered)
	}
	switch kind {
	case stmtDisplayPlanOnlyProfile:
		stats := queryStatsMap(rss)
		n := effectiveRowCount(rss, drainedRowCount)
		fmt.Fprintln(out, rowsInSetLine(n, stats))
		writeQueryStatsDetails(out, stats, verbose)
	case stmtDisplayPlanOnlyPlan:
		// plan tree only (already printed above)
	default:
		return fmt.Errorf("renderQueryPlan: unexpected kind %v", kind)
	}
	return nil
}

// forEachResultRow calls fn for each data row (after scan). Returns row count and result.Err().
func forEachResultRow(metadata *sppb.ResultSetMetadata, result *sql.Rows, fn func([]spanner.GenericColumnValue) error) (int, error) {
	fields := metadata.GetRowType().GetFields()
	n := 0
	for result.Next() {
		n++
		values, err := scanValues(fields, result)
		if err != nil {
			return n, err
		}
		if err := fn(values); err != nil {
			return n, err
		}
	}
	return n, result.Err()
}

func columnNamesFromFields(fields []*sppb.StructType_Field) ([]string, error) {
	return spanvalue.ColumnNames(fields, spanvalue.IndexedUnnamedFieldNamer)
}

func renderResultSetTable(metadata *sppb.ResultSetMetadata, result *sql.Rows, dialect databasepb.DatabaseDialect) (string, int, error) {
	fields := metadata.GetRowType().GetFields()
	columnNames, err := columnNamesFromFields(fields)
	if err != nil {
		return "", 0, err
	}

	var sb strings.Builder
	table := tablewriter.NewTable(&sb,
		tablewriter.WithRendition(
			renderer.NewBlueprint(tw.Rendition{Symbols: tw.NewSymbols(tw.StyleASCII)}).Config(),
		),
		tablewriter.WithTrimSpace(tw.Off),
		tablewriter.WithHeaderAutoFormat(tw.Off),
		tablewriter.WithHeaderAlignment(tw.AlignLeft),
		tablewriter.WithFooterAutoWrap(tw.WrapNone))

	header, err := renderHeader(fields, columnNames, dialect)
	if err != nil {
		return "", 0, err
	}
	table.Header(header)

	n, err := forEachResultRow(metadata, result, func(values []spanner.GenericColumnValue) error {
		ss, err := renderTableCells(dialect, spannerCLIReadableFormatConfig, columnNames, values)
		if err != nil {
			return err
		}
		return table.Append(ss)
	})
	if err != nil {
		return "", n, err
	}
	if err = table.Render(); err != nil {
		return "", n, err
	}
	return sb.String(), n, nil
}

func renderHeader(fields []*sppb.StructType_Field, columnNames []string, dialect databasepb.DatabaseDialect) ([]string, error) {
	if len(columnNames) != len(fields) {
		return nil, fmt.Errorf("renderHeader: len(columnNames)=%d != len(fields)=%d", len(columnNames), len(fields))
	}
	header := make([]string, 0, len(fields))
	for i, f := range fields {
		header = append(header, columnNames[i]+"\n"+formatTypeForHeader(f.GetType(), dialect))
	}
	return header, nil
}

// formatTypeForHeader uses PostgreSQL spellings when --dialect postgresql matches [spanpg.FormatPostgreSQLType].
func formatTypeForHeader(typ *sppb.Type, dialect databasepb.DatabaseDialect) string {
	if dialect == databasepb.DatabaseDialect_POSTGRESQL {
		return spanpg.FormatPostgreSQLType(typ)
	}
	return spantype.FormatTypeNormal(typ)
}

// renderTableCells uses [spanpg.FormatColumnSimple] for PostgreSQL dialect (human-readable PG-oriented scalars);
// GoogleSQL uses [spanvalue.FormatRowColumns] with the Cloud Spanner CLI-compatible formatter (STRUCT as tuple parentheses).
func renderTableCells(dialect databasepb.DatabaseDialect, fc *spanvalue.FormatConfig, columnNames []string, values []spanner.GenericColumnValue) ([]string, error) {
	if dialect == databasepb.DatabaseDialect_POSTGRESQL {
		ss := make([]string, 0, len(values))
		for _, v := range values {
			s, err := spanpg.FormatColumnSimple(v)
			if err != nil {
				return nil, err
			}
			ss = append(ss, s)
		}
		return ss, nil
	}
	return spanvalue.FormatRowColumns(fc, columnNames, values)
}

func scanValues(fields []*sppb.StructType_Field, result *sql.Rows) ([]spanner.GenericColumnValue, error) {
	values := make([]spanner.GenericColumnValue, len(fields))

	anys := make([]any, len(fields))
	for i := range values {
		anys[i] = &values[i]
	}

	if err := result.Scan(anys...); err != nil {
		return nil, err
	}
	return values, nil
}

// renderResultSetCSV writes one header row and data rows. Cell text matches GoogleSQL table cells
// ([spannerCLIReadableFormatConfig]); [encoding/csv] quotes fields when needed. JSONL keeps
// [spanvalue.JSONFormatConfig] for round-trip. The first WriteGCVs emits the header; Flush writes
// header-only output for zero-row results.
func renderResultSetCSV(out io.Writer, metadata *sppb.ResultSetMetadata, result *sql.Rows) (int, error) {
	w, err := spanwriter.NewCSVWriter(out, spanwriter.DelimitedGCVExportOptions(
		metadata,
		spannerCLIReadableFormatConfig,
		spanvalue.IndexedUnnamedFieldNamer,
	)...)
	if err != nil {
		return 0, err
	}
	n, err := forEachResultRow(metadata, result, func(values []spanner.GenericColumnValue) error {
		return w.WriteGCVs(values)
	})
	return n, finishWriterFlush(w.Flush, err)
}

// renderResultSetJSONL writes one JSON object per row via [spanwriter.JSONLWriter].
func renderResultSetJSONL(out io.Writer, metadata *sppb.ResultSetMetadata, result *sql.Rows) (int, error) {
	w, err := spanwriter.NewJSONLWriter(out, spanwriter.JSONLGCVExportOptions(
		metadata,
		spanvalue.JSONFormatConfig(),
		spanvalue.IndexedUnnamedFieldNamer,
	)...)
	if err != nil {
		return 0, err
	}
	n, err := forEachResultRow(metadata, result, func(values []spanner.GenericColumnValue) error {
		return w.WriteGCVs(values)
	})
	return n, finishWriterFlush(w.Flush, err)
}

func finishWriterFlush(flush func() error, rowErr error) error {
	if flushErr := flush(); flushErr != nil {
		if rowErr != nil {
			return errors.Join(rowErr, flushErr)
		}
		return flushErr
	}
	return rowErr
}
