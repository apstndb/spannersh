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

var spannerCLITableFormatConfig = func() *spanvalue.FormatConfig {
	fc := spanvalue.SpannerCLICompatibleFormatConfig()
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

func renderResultSetTable(metadata *sppb.ResultSetMetadata, result *sql.Rows, dialect databasepb.DatabaseDialect) (string, int, error) {
	var sb strings.Builder
	table := tablewriter.NewTable(&sb,
		tablewriter.WithRendition(
			renderer.NewBlueprint(tw.Rendition{Symbols: tw.NewSymbols(tw.StyleASCII)}).Config(),
		),
		tablewriter.WithTrimSpace(tw.Off),
		tablewriter.WithHeaderAutoFormat(tw.Off),
		tablewriter.WithHeaderAlignment(tw.AlignLeft),
		tablewriter.WithFooterAutoWrap(tw.WrapNone))

	table.Header(renderHeader(metadata.GetRowType().GetFields(), dialect))

	n, err := forEachResultRow(metadata, result, func(values []spanner.GenericColumnValue) error {
		ss, err := renderTableCells(dialect, spannerCLITableFormatConfig, values)
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

func renderHeader(fields []*sppb.StructType_Field, dialect databasepb.DatabaseDialect) []string {
	header := make([]string, 0, len(fields))
	for _, f := range fields {
		header = append(header, f.GetName()+"\n"+formatTypeForHeader(f.GetType(), dialect))
	}
	return header
}

// formatTypeForHeader uses PostgreSQL spellings when --dialect postgresql matches [spanpg.FormatPostgreSQLType].
func formatTypeForHeader(typ *sppb.Type, dialect databasepb.DatabaseDialect) string {
	if dialect == databasepb.DatabaseDialect_POSTGRESQL {
		return spanpg.FormatPostgreSQLType(typ)
	}
	return spantype.FormatTypeNormal(typ)
}

// renderTableCells uses [spanpg.FormatColumnSimple] for PostgreSQL dialect (human-readable PG-oriented scalars);
// GoogleSQL uses the Cloud Spanner CLI-compatible formatter, but renders STRUCT with tuple-style parentheses.
func renderTableCells(dialect databasepb.DatabaseDialect, fc *spanvalue.FormatConfig, values []spanner.GenericColumnValue) ([]string, error) {
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
	return renderColumns(fc, values)
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

func renderColumns(fc *spanvalue.FormatConfig, values []spanner.GenericColumnValue) ([]string, error) {
	ss := make([]string, 0, len(values))
	for _, v := range values {
		s, err := fc.FormatToplevelColumn(v)
		if err != nil {
			return nil, err
		}
		ss = append(ss, s)
	}
	return ss, nil
}

// renderResultSetCSV writes one header row and data rows. Cell text uses [spanvalue.JSONFormatConfig]
// through [spanwriter.CSVWriter] so ARRAY/STRUCT are unambiguous; encoding/csv quotes fields as needed.
func renderResultSetCSV(out io.Writer, metadata *sppb.ResultSetMetadata, result *sql.Rows) (int, error) {
	w := spanwriter.NewCSVWriter(out, metadata)
	w.Formatter = spanvalue.JSONFormatConfig()
	if err := w.WriteHeader(); err != nil {
		return 0, err
	}
	n, err := forEachResultRow(metadata, result, func(values []spanner.GenericColumnValue) error {
		return w.WriteGCVs(values)
	})
	return n, finishCSVWrite(w.Flush, err)
}

// renderResultSetJSONL writes one JSON object per row via [spanwriter.JSONLWriter].
func renderResultSetJSONL(out io.Writer, metadata *sppb.ResultSetMetadata, result *sql.Rows) (int, error) {
	w := spanwriter.NewJSONLWriter(out, metadata)
	return forEachResultRow(metadata, result, func(values []spanner.GenericColumnValue) error {
		return w.WriteGCVs(values)
	})
}

func finishCSVWrite(flush func() error, rowErr error) error {
	if flushErr := flush(); flushErr != nil {
		if rowErr != nil {
			return errors.Join(rowErr, flushErr)
		}
		return flushErr
	}
	return rowErr
}
