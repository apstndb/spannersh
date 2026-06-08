package dbsqlrows

import (
	"bytes"
	"errors"
	"testing"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"

	"github.com/apstndb/spanvalue"
	"github.com/apstndb/spanvalue/gcvctor"
	"github.com/apstndb/spanvalue/writer"
)

var _ rowsFacade = (*stubSQLRows)(nil)

type stubSQLRows struct {
	resultSets [][]stubRow
	set        int
	row        int
	scanErr    error
	columns    []string
}

type stubRow struct {
	values []any
}

func (s *stubSQLRows) next() bool {
	if s.scanErr != nil {
		return false
	}
	if s.set >= len(s.resultSets) {
		return false
	}
	if s.row >= len(s.resultSets[s.set]) {
		return false
	}
	s.row++
	return true
}

func (s *stubSQLRows) nextResultSet() bool {
	if s.set+1 >= len(s.resultSets) {
		return false
	}
	s.set++
	s.row = 0
	return true
}

func (s *stubSQLRows) scan(dest ...any) error {
	if s.scanErr != nil {
		return s.scanErr
	}
	if s.set >= len(s.resultSets) || s.row == 0 {
		return errors.New("stubSQLRows: scan without row")
	}
	row := s.resultSets[s.set][s.row-1]
	if len(dest) != len(row.values) {
		return errors.New("stubSQLRows: scan arity mismatch")
	}
	for i, d := range dest {
		switch t := d.(type) {
		case *sppb.ResultSetMetadata:
			if md, ok := row.values[i].(*sppb.ResultSetMetadata); ok {
				*t = *md
			} else {
				*t = row.values[i].(sppb.ResultSetMetadata)
			}
		case **sppb.ResultSetMetadata:
			*t = row.values[i].(*sppb.ResultSetMetadata)
		case *sppb.ResultSetStats:
			if st, ok := row.values[i].(*sppb.ResultSetStats); ok {
				*t = *st
			} else {
				*t = row.values[i].(sppb.ResultSetStats)
			}
		case **sppb.ResultSetStats:
			*t = row.values[i].(*sppb.ResultSetStats)
		case *spanner.GenericColumnValue:
			*t = row.values[i].(spanner.GenericColumnValue)
		default:
			return errors.New("stubSQLRows: unsupported scan dest")
		}
	}
	return nil
}

func (s *stubSQLRows) err() error {
	return s.scanErr
}

func (s *stubSQLRows) columnCount() (int, error) {
	if len(s.columns) > 0 {
		return len(s.columns), nil
	}
	if s.set >= len(s.resultSets) || s.row == 0 {
		return 0, errors.New("stubSQLRows: columnCount without row")
	}
	return len(s.resultSets[s.set][s.row-1].values), nil
}

func metadataWithNames(names ...string) *sppb.ResultSetMetadata {
	fields := make([]*sppb.StructType_Field, len(names))
	for i, name := range names {
		fields[i] = &sppb.StructType_Field{
			Name: name,
			Type: &sppb.Type{Code: sppb.TypeCode_INT64},
		}
	}
	return &sppb.ResultSetMetadata{
		RowType: &sppb.StructType{Fields: fields},
	}
}

func TestWriteGCVStream_zeroDataRowsFlushHeaderOneColumn(t *testing.T) {
	t.Parallel()

	md := metadataWithNames("id")
	stub := &stubSQLRows{
		columns: []string{"id"},
		resultSets: [][]stubRow{
			{{values: []any{md}}},
			nil,
		},
	}

	var out bytes.Buffer
	w, err := writer.NewCSVWriter(&out, writer.WithHeader(true))
	if err != nil {
		t.Fatal(err)
	}

	got, err := runRows(stub, nil, SQLRowsHooksFromGCVWriter(w), RunOptions{ReadMetadataPseudoRow: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.RowsRead != 0 {
		t.Fatalf("RowsRead = %d, want 0", got.RowsRead)
	}
	if out.String() != "id\n" {
		t.Fatalf("output = %q, want %q", out.String(), "id\n")
	}
}

func TestWriteGCVStream_zeroDataRowsFlushHeaderManyColumns(t *testing.T) {
	t.Parallel()

	md := metadataWithNames("a", "b", "c")
	stub := &stubSQLRows{
		columns: []string{"a", "b", "c"},
		resultSets: [][]stubRow{
			{{values: []any{md}}},
			nil,
		},
	}

	var out bytes.Buffer
	w, err := writer.NewCSVWriter(&out, writer.WithHeader(true))
	if err != nil {
		t.Fatal(err)
	}

	_, err = runRows(stub, nil, SQLRowsHooksFromGCVWriter(w), RunOptions{ReadMetadataPseudoRow: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "a,b,c\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestRunAtData_oneRow(t *testing.T) {
	t.Parallel()

	md := metadataWithNames("id")
	gcv := gcvctor.Int64Value(42)
	stub := &stubSQLRows{
		columns: []string{"id"},
		resultSets: [][]stubRow{
			{{values: []any{gcv}}},
		},
	}

	var out bytes.Buffer
	w, err := writer.NewCSVWriter(&out, writer.DelimitedGCVExportOptions(
		md,
		spanvalue.SimpleFormatConfig(),
		spanvalue.IndexedUnnamedFieldNamer,
	)...)
	if err != nil {
		t.Fatal(err)
	}

	got, err := runRows(stub, md, SQLRowsHooksFromGCVWriter(w), RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.RowsRead != 1 {
		t.Fatalf("RowsRead = %d, want 1", got.RowsRead)
	}
	if !bytes.Contains(out.Bytes(), []byte("42")) {
		t.Fatalf("output = %q, want row with 42", out.String())
	}
}

func TestDrainAtData_skipsWriteDataRow(t *testing.T) {
	t.Parallel()

	md := metadataWithNames("id")
	gcv := gcvctor.Int64Value(1)
	stub := &stubSQLRows{
		columns: []string{"id"},
		resultSets: [][]stubRow{
			{{values: []any{gcv}}, {values: []any{gcvctor.Int64Value(2)}}},
		},
	}

	var rowsWritten int
	hooks := SQLRowsHooks{
		WriteDataRow: func([]spanner.GenericColumnValue) error {
			rowsWritten++
			return nil
		},
	}
	got, err := runRows(stub, md, hooks, RunOptions{DataMode: DataModeDrain})
	if err != nil {
		t.Fatal(err)
	}
	if got.RowsRead != 2 {
		t.Fatalf("RowsRead = %d, want 2", got.RowsRead)
	}
	if rowsWritten != 0 {
		t.Fatalf("WriteDataRow called %d times, want 0", rowsWritten)
	}
}

func TestRunAtData_readsStatsWhenRequested(t *testing.T) {
	t.Parallel()

	md := metadataWithNames("id")
	stats := &sppb.ResultSetStats{
		RowCount: &sppb.ResultSetStats_RowCountExact{RowCountExact: 0},
	}
	stub := &stubSQLRows{
		columns: []string{"id"},
		resultSets: [][]stubRow{
			nil,
			{{values: []any{stats}}},
		},
	}

	got, err := runRows(stub, md, SQLRowsHooks{}, RunOptions{ReadResultSetStats: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Stats == nil {
		t.Fatal("Stats is nil")
	}
}
