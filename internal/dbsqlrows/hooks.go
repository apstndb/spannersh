package dbsqlrows

import (
	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
)

// GCVStreamWriter is implemented by [github.com/apstndb/spanvalue/writer] delimited/JSONL writers.
type GCVStreamWriter interface {
	WriteGCVs([]spanner.GenericColumnValue) error
	Flush() error
	PrepareRowType(*sppb.StructType) error
}

type gcvStreamSink struct {
	w GCVStreamWriter
}

func (s gcvStreamSink) PrepareMetadata(md *sppb.ResultSetMetadata) error {
	if md == nil {
		return nil
	}
	return s.w.PrepareRowType(md.GetRowType())
}

func (s gcvStreamSink) WriteDataRow(gcvs []spanner.GenericColumnValue) error {
	return s.w.WriteGCVs(gcvs)
}

func (s gcvStreamSink) Finish(*SQLRowsResult) error {
	return s.w.Flush()
}

// SQLRowsHooks drives [Run]. Nil callbacks are skipped.
type SQLRowsHooks struct {
	PrepareMetadata func(*sppb.ResultSetMetadata) error
	WriteDataRow    func([]spanner.GenericColumnValue) error
	Finish          func(*SQLRowsResult) error
}

// SQLRowsHooksFromGCVWriter adapts a spanvalue writer (mirrors [writer.RowIteratorHooksFromWriter]).
func SQLRowsHooksFromGCVWriter(w GCVStreamWriter) SQLRowsHooks {
	return SQLRowsHooksFromSink(gcvStreamSink{w: w})
}
