package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/spanner"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	spannerdriver "github.com/googleapis/go-sql-spanner"
)

// app bundles the database/sql session and output settings for one REPL process.
type app struct {
	ctx     context.Context
	out     io.Writer
	db      *sql.DB
	format  outputFormat
	verbose bool
	dialect databasepb.DatabaseDialect
}

func (a *app) executeAndRender(query string) error {
	return a.executeAndRenderContext(a.ctx, query)
}

func (a *app) executeAndRenderContext(ctx context.Context, query string) error {
	plan, err := planExecution(query, a.dialect)
	if err != nil {
		return err
	}
	for i, batch := range plan.batches {
		if i > 0 {
			fmt.Fprintln(a.out)
		}
		pq := preparedQuery{
			execSQL: joinBatchExecSQL(batch),
			mode:    batch[0].mode,
		}
		head, err := executeQuery(ctx, a.db, pq)
		if err != nil {
			return err
		}
		err = func() error {
			defer head.rows.Close()
			return displayResults(a.out, head.meta, head.rows, stmtKindsFromBatch(batch), a.format, a.verbose, a.dialect)
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// queryHead is open Rows after the metadata result set is consumed. With buildExecOptions,
// go-sql-spanner yields metadata → data rows → optional stats; caller must Close head.rows.
type queryHead struct {
	rows *sql.Rows
	meta *sppb.ResultSetMetadata
}

func buildExecOptions(mode *sppb.ExecuteSqlRequest_QueryMode) spannerdriver.ExecOptions {
	return spannerdriver.ExecOptions{
		DecodeOption:            spannerdriver.DecodeOptionProto,
		DirectExecuteQuery:      true,
		ReturnResultSetMetadata: true,
		ReturnResultSetStats:    true,
		QueryOptions:            spanner.QueryOptions{Mode: mode},
	}
}

func executeQuery(ctx context.Context, db *sql.DB, pq preparedQuery) (*queryHead, error) {
	rows, err := db.QueryContext(ctx, pq.execSQL, buildExecOptions(pq.mode))
	if err != nil {
		return nil, err
	}
	// Do not call Rows.Err before the first Next: it reflects iteration errors, not QueryContext failures.

	rsm, ok, err := readMetadataAndAdvanceToData(rows)
	if err != nil {
		rows.Close()
		return nil, err
	}
	if !ok {
		rows.Close()
		return nil, fmt.Errorf("no result set metadata")
	}

	return &queryHead{rows: rows, meta: rsm}, nil
}

func stmtKindsFromBatch(batch []preparedQuery) []stmtDisplayKind {
	k := make([]stmtDisplayKind, len(batch))
	for i, s := range batch {
		k[i] = s.kind
	}
	return k
}

// displayResults walks one driver batch: each element of kinds matches one statement (metadata → rows → stats cycle).
func displayResults(out io.Writer, rsm *sppb.ResultSetMetadata, rows *sql.Rows, kinds []stmtDisplayKind, format outputFormat, verbose bool, dialect databasepb.DatabaseDialect) error {
	if len(kinds) == 0 {
		return nil
	}
	for stmtIdx, kind := range kinds {
		if stmtIdx > 0 {
			fmt.Fprintln(out)
			nextRsm, ok, err := readMetadataAndAdvanceToData(rows)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("expected metadata for statement %d after batch result", stmtIdx)
			}
			rsm = nextRsm
		}
		if err := displayStatementResult(out, rsm, rows, kind, format, verbose, dialect); err != nil {
			return err
		}
	}
	return nil
}

func displayStatementResult(out io.Writer, rsm *sppb.ResultSetMetadata, rows *sql.Rows, kind stmtDisplayKind, format outputFormat, verbose bool, dialect databasepb.DatabaseDialect) error {
	switch kind {
	case stmtDisplayQueryResult:
		n, err := renderQueryResultData(out, rsm, rows, format, dialect)
		if err != nil {
			return err
		}
		return writeExecutionSummaryAfterDataRows(out, rows, n, verbose)
	default:
		n, err := drainResultSet(rsm, rows)
		if err != nil {
			return err
		}
		return renderQueryPlan(out, rows, n, kind, verbose)
	}
}

// readMetadataAndAdvanceToData reads the metadata pseudo-row and advances to the data result set.
// If there is no next row, returns ok=false and err=rows.Err() (nil on clean EOF).
func readMetadataAndAdvanceToData(rows *sql.Rows) (rsm *sppb.ResultSetMetadata, ok bool, err error) {
	if !rows.Next() {
		return nil, false, rows.Err()
	}
	if err := rows.Scan(&rsm); err != nil {
		return nil, false, err
	}
	if !rows.NextResultSet() {
		return nil, false, fmt.Errorf("expected data rows result set after metadata")
	}
	return rsm, true, nil
}

// fetchResultSetStatsAfterDataRows advances to the stats result set after data rows and decodes [ResultSetStats].
// If there is no following result set, returns (nil, nil).
func fetchResultSetStatsAfterDataRows(rows *sql.Rows) (*sppb.ResultSetStats, error) {
	if !rows.NextResultSet() {
		return nil, nil
	}
	rss, err := fetchSingleValueInResultSet[*sppb.ResultSetStats](rows)
	if err != nil {
		return nil, fmt.Errorf("fetch ResultSetStats: %w", err)
	}
	return rss, nil
}

// writeExecutionSummaryAfterDataRows reads the following result set as [ResultSetStats] and prints the execution summary (query_stats lines only when populated).
func writeExecutionSummaryAfterDataRows(out io.Writer, rows *sql.Rows, dataRowCount int, verbose bool) error {
	rss, err := fetchResultSetStatsAfterDataRows(rows)
	if err != nil {
		return err
	}
	formatExecutionSummary(out, rss, dataRowCount, verbose)
	return nil
}

func drainResultSet(metadata *sppb.ResultSetMetadata, result *sql.Rows) (int, error) {
	return forEachResultRow(metadata, result, func([]spanner.GenericColumnValue) error { return nil })
}

func fetchSingleValueInResultSet[T any](rows *sql.Rows) (T, error) {
	var v T
	if !rows.Next() {
		return v, errors.New("no result")
	}
	if err := rows.Scan(&v); err != nil {
		return v, err
	}
	_ = rows.NextResultSet()
	return v, nil
}
