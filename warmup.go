package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"
)

// startBackgroundWarmup reads database_dialect via the driver (same path as [detectDatabaseDialect])
// once without blocking the REPL. When --dialect auto, run.go skips this and relies on synchronous
// [detectDatabaseDialect] only (one round trip for dialect + warm-up).
// Failures are logged to errOut unless ctx is already canceled.
func startBackgroundWarmup(ctx context.Context, errOut io.Writer, db *sql.DB) {
	go func() {
		if err := runWarmupQuery(ctx, db); err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(errOut, "warmup: %v\n", err)
		}
	}()
}

func runWarmupQuery(ctx context.Context, db *sql.DB) error {
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := queryDriverDatabaseDialect(wctx, db)
	return err
}
