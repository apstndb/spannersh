package dbsqlrows

import (
	"errors"
	"io"
	"strings"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
)

// TableRenderConfig injects column naming and cell formatting from the caller (avoids import cycles with main).
type TableRenderConfig struct {
	ColumnNames func(fields []*sppb.StructType_Field) []string
	HeaderRow   func(fields []*sppb.StructType_Field, columnNames []string) ([]string, error)
	DataRow     func(columnNames []string, values []spanner.GenericColumnValue) ([]string, error)
}

// TableRenderer implements [StatementSink] for ASCII table output (olekukonko/tablewriter).
type TableRenderer struct {
	cfg         TableRenderConfig
	out         io.Writer
	table       *tablewriter.Table
	columnNames []string
}

// NewTableRenderer builds a sink that writes a table into buf.
func NewTableRenderer(buf io.Writer, cfg TableRenderConfig) *TableRenderer {
	return &TableRenderer{
		cfg: cfg,
		out: buf,
		table: tablewriter.NewTable(buf,
			tablewriter.WithRendition(
				renderer.NewBlueprint(tw.Rendition{Symbols: tw.NewSymbols(tw.StyleASCII)}).Config(),
			),
			tablewriter.WithTrimSpace(tw.Off),
			tablewriter.WithHeaderAutoFormat(tw.Off),
			tablewriter.WithHeaderAlignment(tw.AlignLeft),
			tablewriter.WithFooterAutoWrap(tw.WrapNone)),
	}
}

// PrepareMetadata implements [StatementSink].
func (t *TableRenderer) PrepareMetadata(md *sppb.ResultSetMetadata) error {
	if md == nil || md.GetRowType() == nil {
		return errors.New("nil result set metadata or row type")
	}
	fields := md.GetRowType().GetFields()
	if t.cfg.ColumnNames == nil || t.cfg.HeaderRow == nil {
		return errors.New("TableRenderConfig: ColumnNames and HeaderRow required")
	}
	t.columnNames = t.cfg.ColumnNames(fields)
	header, err := t.cfg.HeaderRow(fields, t.columnNames)
	if err != nil {
		return err
	}
	t.table.Header(header)
	return nil
}

// WriteDataRow implements [StatementSink].
func (t *TableRenderer) WriteDataRow(gcvs []spanner.GenericColumnValue) error {
	if t.cfg.DataRow == nil {
		return errors.New("TableRenderConfig: DataRow required")
	}
	ss, err := t.cfg.DataRow(t.columnNames, gcvs)
	if err != nil {
		return err
	}
	return t.table.Append(ss)
}

// Finish implements [StatementSink].
func (t *TableRenderer) Finish(*SQLRowsResult) error {
	return t.table.Render()
}

// String returns rendered table text when buf is a [strings.Builder].
func (t *TableRenderer) String() string {
	if b, ok := t.out.(*strings.Builder); ok {
		return b.String()
	}
	return ""
}
