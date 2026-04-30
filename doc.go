// Spannersh is a small interactive client for Cloud Spanner built on database/sql with
// github.com/googleapis/go-sql-spanner.
//
// Layout (single package main, one file per concern):
//
//	main.go      — signal-aware context, exit status on interrupt
//	run.go       — CLI flags, DSN open, background warmup, REPL loop
//	dsn.go       — DSN composition
//	warmup.go    — non-blocking driver database_dialect warm-up after connect (skipped when --dialect auto)
//	explain.go   — EXPLAIN / EXPLAIN ANALYZE stripping → QueryMode + display kind
//	query.go     — ExecOptions, query execution, driving render/summary
//	render.go    — table / CSV / JSONL, plan tree, row helpers
//	summary.go   — execution summary from ResultSetStats.query_stats when present
//	format.go    — --format parsing
package main
