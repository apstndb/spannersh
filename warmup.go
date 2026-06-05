package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"
)

// startBackgroundWarmup runs a lightweight SELECT 1 once without blocking the REPL so the first
// interactive query is less likely to pay full cold-start cost. Uses default query mode (no PROFILE
// ExecOptions). On failure, logs one line to errOut; on ctx cancel or benign shutdown (db closed
// while exiting), logs nothing.
func startBackgroundWarmup(ctx context.Context, errOut io.Writer, db *sql.DB) {
	go func() {
		if err := runWarmupQuery(ctx, db); err != nil {
			if ctx.Err() != nil || isWarmupBenignShutdownErr(err) {
				return
			}
			fmt.Fprintf(errOut, "warmup: %v\n", err)
		}
	}()
}

func isWarmupBenignShutdownErr(err error) bool {
	if errors.Is(err, sql.ErrConnDone) {
		return true
	}
	// Normal quit: root ctx is not cancelled, but run() closes the pool while warmup is in flight.
	return err.Error() == "sql: database is closed"
}

func runWarmupQuery(ctx context.Context, db *sql.DB) error {
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := db.QueryContext(wctx, "SELECT 1")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}
