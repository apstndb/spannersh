package dbsqlrows

import (
	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
)

// StatementSink is the go-sql-spanner analogue of [writer.RowIteratorWriter]: prepare
// schema from metadata, handle each data row, then finish (flush table, CSV, etc.).
type StatementSink interface {
	PrepareMetadata(*sppb.ResultSetMetadata) error
	WriteDataRow([]spanner.GenericColumnValue) error
	Finish(*SQLRowsResult) error
}

// SQLRowsHooksFromSink builds hooks from any sink (table, GCV writer, custom).
func SQLRowsHooksFromSink(s StatementSink) SQLRowsHooks {
	if s == nil {
		return SQLRowsHooks{}
	}
	return SQLRowsHooks{
		PrepareMetadata: s.PrepareMetadata,
		WriteDataRow:    s.WriteDataRow,
		Finish:          s.Finish,
	}
}
