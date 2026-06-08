package dbsqlrows

import (
	"database/sql"
	"errors"
	"fmt"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
)

var (
	ErrNilRows     = errors.New("nil sql.Rows")
	ErrNilMetadata = errors.New("nil result set metadata")
)

// RunOptions configures pseudo-row handling for a single statement cycle.
type RunOptions struct {
	// ReadMetadataPseudoRow reads the first metadata pseudo-row and advances to
	// data rows (go-sql-spanner ReturnResultSetMetadata). When false, rows must
	// already be positioned on the data result set and metadata must be supplied.
	ReadMetadataPseudoRow bool
	// ReadResultSetStats advances past data rows to the stats pseudo-row when present.
	ReadResultSetStats bool
	// DataMode selects process vs drain-only iteration (see [DataModeDrain] for EXPLAIN).
	DataMode DataMode
}

// WriteGCVStream exports data rows into w using [SQLRowsHooksFromGCVWriter].
// metadata is required when ReadMetadataPseudoRow is false (spannersh executeQuery path).
func WriteGCVStream(w GCVStreamWriter, rows *sql.Rows, metadata *sppb.ResultSetMetadata, opts RunOptions) (*SQLRowsResult, error) {
	if opts.DataMode == 0 {
		opts.DataMode = DataModeProcess
	}
	return Run(rows, metadata, SQLRowsHooksFromGCVWriter(w), opts)
}

// DrainAtData consumes data rows and optionally reads [ResultSetStats] into the result.
// Use for EXPLAIN / EXPLAIN ANALYZE before rendering from [SQLRowsResult.Stats].
func DrainAtData(rows *sql.Rows, metadata *sppb.ResultSetMetadata, readStats bool) (*SQLRowsResult, error) {
	return RunAtData(rows, metadata, SQLRowsHooks{}, RunOptions{
		DataMode:             DataModeDrain,
		ReadResultSetStats:   readStats,
		ReadMetadataPseudoRow: false,
	})
}

// RunAtData streams rows already positioned on the data result set. metadata must be non-nil.
func RunAtData(rows *sql.Rows, metadata *sppb.ResultSetMetadata, hooks SQLRowsHooks, opts RunOptions) (*SQLRowsResult, error) {
	opts.ReadMetadataPseudoRow = false
	return Run(rows, metadata, hooks, opts)
}

// RunFromStart reads metadata (when opts.ReadMetadataPseudoRow) then data rows.
func RunFromStart(rows *sql.Rows, hooks SQLRowsHooks, opts RunOptions) (*SQLRowsResult, error) {
	opts.ReadMetadataPseudoRow = true
	return Run(rows, nil, hooks, opts)
}

// Run executes one metadata → data → optional stats cycle on rows.
func Run(rows *sql.Rows, metadata *sppb.ResultSetMetadata, hooks SQLRowsHooks, opts RunOptions) (*SQLRowsResult, error) {
	if rows == nil {
		return nil, ErrNilRows
	}
	return runRows(sqlRowsFacade{rows}, metadata, hooks, opts)
}

func runRows(fac rowsFacade, metadata *sppb.ResultSetMetadata, hooks SQLRowsHooks, opts RunOptions) (*SQLRowsResult, error) {
	result := &SQLRowsResult{Metadata: metadata}

	if opts.ReadMetadataPseudoRow {
		if !fac.next() {
			if err := fac.err(); err != nil {
				return nil, err
			}
			return nil, errors.New("missing result set metadata row")
		}
		var md *sppb.ResultSetMetadata
		if err := fac.scan(&md); err != nil {
			return nil, err
		}
		result.Metadata = md
		metadata = md
		if !fac.nextResultSet() {
			if err := fac.err(); err != nil {
				return nil, err
			}
			return finishRun(result, hooks)
		}
	} else if metadata == nil {
		return nil, ErrNilMetadata
	}

	if hooks.PrepareMetadata != nil {
		if err := hooks.PrepareMetadata(metadata); err != nil {
			return result, err
		}
	}

	dataMode := opts.DataMode
	if dataMode == 0 {
		dataMode = DataModeProcess
	}
	for fac.next() {
		gcvs, err := scanGCVRow(fac)
		if err != nil {
			return result, err
		}
		if dataMode == DataModeProcess && hooks.WriteDataRow != nil {
			if err := hooks.WriteDataRow(gcvs); err != nil {
				return result, err
			}
		}
		result.RowsRead++
	}
	if err := fac.err(); err != nil {
		return result, err
	}

	if opts.ReadResultSetStats {
		stats, err := readResultSetStats(fac)
		if err != nil {
			return result, err
		}
		result.Stats = stats
	}

	return finishRun(result, hooks)
}

func finishRun(result *SQLRowsResult, hooks SQLRowsHooks) (*SQLRowsResult, error) {
	if hooks.Finish != nil {
		if err := hooks.Finish(result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func readResultSetStats(fac rowsFacade) (*sppb.ResultSetStats, error) {
	if !fac.nextResultSet() {
		if err := fac.err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if !fac.next() {
		return nil, fmt.Errorf("expected result set stats row")
	}
	var stats *sppb.ResultSetStats
	if err := fac.scan(&stats); err != nil {
		return nil, err
	}
	_ = fac.nextResultSet()
	return stats, nil
}

func scanGCVRow(fac rowsFacade) ([]spanner.GenericColumnValue, error) {
	n, err := fac.columnCount()
	if err != nil {
		return nil, err
	}
	gcvs, dest := gcvScanTargets(n)
	if err := fac.scan(dest...); err != nil {
		return nil, err
	}
	return gcvs, nil
}

func gcvScanTargets(n int) ([]spanner.GenericColumnValue, []any) {
	gcvs := make([]spanner.GenericColumnValue, n)
	dest := make([]any, n)
	for i := range gcvs {
		dest[i] = &gcvs[i]
	}
	return gcvs, dest
}
