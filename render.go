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
	"github.com/apstndb/spantype"
	"github.com/apstndb/spanvalue"
	"github.com/apstndb/spanvalue/dbsqlrows"
	spanwriter "github.com/apstndb/spanvalue/writer"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
)

// spannerCLIReadableFormatConfig is Spanner CLI-compatible cell text with tuple STRUCT
// parentheses (not bracket style). Shared by GoogleSQL table cells and CSV export.
// pgSimpleFormatConfig renders PostgreSQL-dialect table cells; hoisted to
// package level because renderTableCells runs per row.
var pgSimpleFormatConfig = spanvalue.SimpleFormatConfig()

var spannerCLIReadableFormatConfig = func() *spanvalue.FormatConfig {
	fc := spanvalue.SpannerCLICompatibleFormatConfig().WithComplexPlugin(
		spanvalue.PluginForStruct(spanvalue.FormatSimpleStructField, spanvalue.FormatTupleStruct))
	if err := fc.Validate(); err != nil {
		panic("spannerCLIReadableFormatConfig: " + err.Error())
	}
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

// renderQueryPlanFromStats prints EXPLAIN (PLAN) or EXPLAIN ANALYZE (PROFILE) from stats already
// read via [dbsqlrows.RunRowsAtData] (counting hooks, ReadResultSetStats). PLAN does not populate QueryStats in general; we only show the
// plan tree. PROFILE shows the plan first, then row count, then query_stats when the API returns it.
func renderQueryPlanFromStats(out io.Writer, rss *sppb.ResultSetStats, drainedRowCount int, kind stmtDisplayKind, verbose bool) error {
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
		return fmt.Errorf("renderQueryPlanFromStats: unexpected kind %v", kind)
	}
	return nil
}

func metadataRowTypeFields(metadata *sppb.ResultSetMetadata) ([]*sppb.StructType_Field, error) {
	if metadata == nil {
		return nil, errors.New("metadata is nil")
	}
	if metadata.GetRowType() == nil {
		return nil, errors.New("metadata row type is nil")
	}
	return metadata.GetRowType().GetFields(), nil
}

func columnNamesFromFields(fields []*sppb.StructType_Field) ([]string, error) {
	return spanvalue.ColumnNames(fields, spanvalue.IndexedUnnamedFieldNamer)
}

// columnNamesForRender returns names for table headers and FormatRowColumns. When
// ColumnNames rejects duplicate aliases, fall back to raw field names (indexed
// placeholders for unnamed columns) so valid result sets still render.
func columnNamesForRender(fields []*sppb.StructType_Field) []string {
	names, err := columnNamesFromFields(fields)
	if err == nil {
		return names
	}
	fallback := make([]string, len(fields))
	for i, f := range fields {
		name := f.GetName()
		if name == "" {
			name = fmt.Sprintf("_%d", i)
		}
		fallback[i] = name
	}
	return fallback
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

	var columnNames []string
	exported, err := dbsqlrows.RunRowsAtData(result, metadata, dbsqlrows.NewSQLRowsHooks().
		WithPrepareMetadata(func(md *sppb.ResultSetMetadata) error {
			fields, err := metadataRowTypeFields(md)
			if err != nil {
				return err
			}
			columnNames = columnNamesForRender(fields)
			header, err := renderHeader(fields, columnNames, dialect)
			if err != nil {
				return err
			}
			table.Header(header)
			return nil
		}).
		WithWriteDataRow(func(values []spanner.GenericColumnValue) error {
			ss, err := renderTableCells(dialect, spannerCLIReadableFormatConfig, columnNames, values)
			if err != nil {
				return err
			}
			return table.Append(ss)
		}).
		WithFinish(func(*dbsqlrows.SQLRowsResult) error {
			return table.Render()
		}),
		dbsqlrows.SQLRowsConfig{})
	if err != nil {
		return "", exported.RowsRead, err
	}
	return sb.String(), exported.RowsRead, nil
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

// formatTypeForHeader uses PostgreSQL spellings when --dialect postgresql matches
// [spantype.FormatTypePostgreSQL].
func formatTypeForHeader(typ *sppb.Type, dialect databasepb.DatabaseDialect) string {
	if dialect == databasepb.DatabaseDialect_POSTGRESQL {
		return spantype.FormatTypePostgreSQL(typ)
	}
	return spantype.FormatTypeNormal(typ)
}

// renderTableCells uses [spanvalue.SimpleFormatConfig] for PostgreSQL dialect (human-readable
// scalars, matching the former spanpg.FormatColumnSimple bridge); GoogleSQL uses
// [spanvalue.FormatRowColumns] with the Cloud Spanner CLI-compatible formatter (STRUCT as
// tuple parentheses).
func renderTableCells(dialect databasepb.DatabaseDialect, fc *spanvalue.FormatConfig, columnNames []string, values []spanner.GenericColumnValue) ([]string, error) {
	if dialect == databasepb.DatabaseDialect_POSTGRESQL {
		ss := make([]string, 0, len(values))
		for _, v := range values {
			s, err := pgSimpleFormatConfig.FormatToplevelColumn(v)
			if err != nil {
				return nil, err
			}
			ss = append(ss, s)
		}
		return ss, nil
	}
	return spanvalue.FormatRowColumns(fc, columnNames, values)
}

// renderResultSetCSV writes one header row and data rows. Cell text matches GoogleSQL table cells
// ([spannerCLIReadableFormatConfig]); [encoding/csv] quotes fields when needed. The first WriteGCVs
// emits the header; Flush writes header-only output for zero-row results.
func renderResultSetCSV(out io.Writer, metadata *sppb.ResultSetMetadata, result *sql.Rows) (int, error) {
	if _, err := metadataRowTypeFields(metadata); err != nil {
		return 0, err
	}
	w, err := spanwriter.NewCSVWriter(out, spanwriter.DelimitedGCVExportOptions(
		metadata,
		spannerCLIReadableFormatConfig,
		spanvalue.IndexedUnnamedFieldNamer,
	)...)
	if err != nil {
		return 0, err
	}
	exported, err := dbsqlrows.WriteRowsAtData(result, metadata, w, dbsqlrows.SQLRowsConfig{})
	if exported == nil {
		// Argument-validation failures carry no partial result.
		return 0, err
	}
	// On abort, RowsRead reflects progress at the abort point (dbsqlrows
	// partial-result contract); surface it alongside the error.
	return exported.RowsRead, err
}

// renderResultSetJSONL writes one JSON object per row via [spanwriter.JSONLWriter]. Cell values use
// [spanvalue.JSONFormatConfig] for machine-oriented round-trip (contrast with CSV/table CLI text).
// [dbsqlrows.WriteRowsAtData] calls [spanwriter.JSONLWriter.Flush] for symmetry with CSV.
func renderResultSetJSONL(out io.Writer, metadata *sppb.ResultSetMetadata, result *sql.Rows) (int, error) {
	if _, err := metadataRowTypeFields(metadata); err != nil {
		return 0, err
	}
	w, err := spanwriter.NewJSONLWriter(out, spanwriter.JSONLGCVExportOptions(
		metadata,
		spanvalue.JSONFormatConfig(),
		spanvalue.IndexedUnnamedFieldNamer,
	)...)
	if err != nil {
		return 0, err
	}
	exported, err := dbsqlrows.WriteRowsAtData(result, metadata, w, dbsqlrows.SQLRowsConfig{})
	if exported == nil {
		// Argument-validation failures carry no partial result.
		return 0, err
	}
	// On abort, RowsRead reflects progress at the abort point (dbsqlrows
	// partial-result contract); surface it alongside the error.
	return exported.RowsRead, err
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
