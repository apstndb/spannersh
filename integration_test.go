//go:build integration

package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/apstndb/spanemuboost"
)

var lazyRuntime = spanemuboost.NewLazyRuntime(spanemuboost.BackendEmulator, spanemuboost.EnableInstanceAutoConfigOnly())

func TestMain(m *testing.M) {
	lazyRuntime.TestMain(m)
}

func openIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	clients := spanemuboost.SetupClients(t, lazyRuntime, spanemuboost.WithRandomDatabaseID())
	t.Setenv("SPANNER_EMULATOR_HOST", clients.URI())
	dsn := composeSpannerDSN(clients.ProjectID, clients.InstanceID, clients.DatabaseID, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, "use_plain_text=true")
	db, err := sql.Open("spanner", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// openIntegrationDBPostgreSQL creates a PostgreSQL-dialect database on the shared emulator and opens database/sql.
func openIntegrationDBPostgreSQL(t *testing.T) *sql.DB {
	t.Helper()
	clients := spanemuboost.SetupClients(t, lazyRuntime,
		spanemuboost.WithRandomDatabaseID(),
		spanemuboost.WithDatabaseDialect(databasepb.DatabaseDialect_POSTGRESQL),
	)
	t.Setenv("SPANNER_EMULATOR_HOST", clients.URI())
	dsn := composeSpannerDSN(clients.ProjectID, clients.InstanceID, clients.DatabaseID, databasepb.DatabaseDialect_POSTGRESQL, "use_plain_text=true")
	db, err := sql.Open("spanner", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func integrationExecOutput(t *testing.T, sql string) string {
	t.Helper()
	return integrationExecOutputFormat(t, outputFormatTable, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, sql)
}

func openIntegrationApp(t *testing.T, dialect databasepb.DatabaseDialect) *app {
	t.Helper()
	var db *sql.DB
	if dialect == databasepb.DatabaseDialect_POSTGRESQL {
		db = openIntegrationDBPostgreSQL(t)
	} else {
		db = openIntegrationDB(t)
	}
	conn, err := acquireSessionConn(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &app{ctx: t.Context(), out: io.Discard, db: db, conn: conn, format: outputFormatTable, dialect: dialect}
}

func integrationExecOutputFormat(t *testing.T, format outputFormat, dialect databasepb.DatabaseDialect, sqlText string) string {
	t.Helper()
	cli := openIntegrationApp(t, dialect)
	var buf bytes.Buffer
	cli.out = &buf
	cli.format = format
	if err := cli.executeAndRender(sqlText); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func integrationPostgreSQLExecOutput(t *testing.T, format outputFormat, sqlText string) string {
	t.Helper()
	return integrationExecOutputFormat(t, format, databasepb.DatabaseDialect_POSTGRESQL, sqlText)
}

func sessionExec(t *testing.T, cli *app, sqlText string) string {
	t.Helper()
	var buf bytes.Buffer
	cli.out = &buf
	if err := cli.executeAndRender(sqlText); err != nil {
		t.Fatalf("%s: %v", sqlText, err)
	}
	return buf.String()
}

func TestIntegrationSessionSurvivesInputs(t *testing.T) {
	cli := openIntegrationApp(t, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
	sessionExec(t, cli, "CREATE TABLE SessionTxn (Id INT64 NOT NULL) PRIMARY KEY (Id)")

	sessionExec(t, cli, "BEGIN TRANSACTION")
	sessionExec(t, cli, "INSERT INTO SessionTxn (Id) VALUES (1)")
	sessionExec(t, cli, "ROLLBACK")
	if got := sessionCount(t, cli); got != "0" {
		t.Fatalf("rollback count = %s", got)
	}

	sessionExec(t, cli, "BEGIN TRANSACTION")
	sessionExec(t, cli, "INSERT INTO SessionTxn (Id) VALUES (1)")
	sessionExec(t, cli, "COMMIT")
	if got := sessionCount(t, cli); got != "1" {
		t.Fatalf("commit count = %s", got)
	}

	sessionExec(t, cli, "BEGIN TRANSACTION")
	sessionExec(t, cli, "INSERT INTO SessionTxn (Id) VALUES (2)")
	sessionExec(t, cli, "ROLLBACK")
	if got := sessionCount(t, cli); got != "1" {
		t.Fatalf("second rollback count = %s", got)
	}

	sessionExec(t, cli, "SET statement_timeout = '1s'")
	if out := sessionExec(t, cli, "SHOW VARIABLE statement_timeout"); !strings.Contains(out, "1s") {
		t.Fatalf("SET did not persist:\n%s", out)
	}

	sessionExec(t, cli, "BEGIN TRANSACTION")
	sessionExec(t, cli, "EXPLAIN SELECT 1")
	sessionExec(t, cli, "INSERT INTO SessionTxn (Id) VALUES (3)")
	sessionExec(t, cli, "ROLLBACK")
	if got := sessionCount(t, cli); got != "1" {
		t.Fatalf("PLAN input count = %s", got)
	}

	sessionExec(t, cli, "START BATCH DML")
	sessionExec(t, cli, "INSERT INTO SessionTxn (Id) VALUES (4)")
	sessionExec(t, cli, "ABORT BATCH")
	if got := sessionCount(t, cli); got != "1" {
		t.Fatalf("aborted batch count = %s", got)
	}

	sessionExec(t, cli, "BEGIN TRANSACTION")
	sessionExec(t, cli, "INSERT INTO SessionTxn (Id) VALUES (5)")
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	err := cli.executeAndRenderContext(canceled, "SELECT COUNT(*) AS n FROM SessionTxn")
	// The driver surfaces cancellation as a status error. It does not always unwrap to context.Canceled.
	if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled")) {
		t.Fatalf("canceled query err = %v", err)
	}
	sessionExec(t, cli, "COMMIT")
	if got := sessionCount(t, cli); got != "2" {
		t.Fatalf("commit after canceled query count = %s", got)
	}

	sessionExec(t, cli, "BEGIN TRANSACTION")
	sessionExec(t, cli, "INSERT INTO SessionTxn (Id) VALUES (6)")
	if err := runWarmupQuery(t.Context(), cli.db); err != nil {
		t.Fatal(err)
	}
	sessionExec(t, cli, "ROLLBACK")
	if got := sessionCount(t, cli); got != "2" {
		t.Fatalf("pool warmup count = %s", got)
	}

	sessionExec(t, cli, "BEGIN TRANSACTION")
	sessionExec(t, cli, "INSERT INTO SessionTxn (Id) VALUES (7)")
	if err := cli.conn.Close(); err != nil {
		t.Fatal(err)
	}
	cli.conn = nil
	var n int64
	if err := cli.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM SessionTxn").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("closing the session connection count = %d, want 2", n)
	}
}

func sessionCount(t *testing.T, cli *app) string {
	t.Helper()
	out := sessionExec(t, cli, "SELECT COUNT(*) AS n FROM SessionTxn")
	// Table cells are padded, for example "| 0     |", and a type row sits under the name.
	matches := regexp.MustCompile(`(?m)^\|\s+(\d+)\s+\|$`).FindAllStringSubmatch(out, -1)
	if len(matches) == 1 {
		return matches[0][1]
	}
	t.Fatalf("count output:\n%s", out)
	return ""
}

func TestIntegrationPartitionedDMLReportsLowerBound(t *testing.T) {
	clients := spanemuboost.SetupClients(t, lazyRuntime, spanemuboost.WithRandomDatabaseID())
	t.Setenv("SPANNER_EMULATOR_HOST", clients.URI())
	open := func(suffix string) *sql.DB {
		t.Helper()
		dsn := composeSpannerDSN(clients.ProjectID, clients.InstanceID, clients.DatabaseID, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, suffix)
		db, err := sql.Open("spanner", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	openApp := func(db *sql.DB, out io.Writer) *app {
		t.Helper()
		conn, err := acquireSessionConn(t.Context(), db)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return &app{ctx: t.Context(), out: out, db: db, conn: conn, format: outputFormatTable, dialect: databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL}
	}
	normal := open("use_plain_text=true")
	cli := openApp(normal, io.Discard)
	for _, stmt := range []string{
		"CREATE TABLE PdmlCount (Id INT64 NOT NULL) PRIMARY KEY (Id)",
		"INSERT INTO PdmlCount (Id) VALUES (1), (2)",
	} {
		if err := cli.executeAndRender(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	pdml := open("use_plain_text=true;autocommit_dml_mode=PARTITIONED_NON_ATOMIC")
	var out bytes.Buffer
	pdmlApp := openApp(pdml, &out)
	if err := pdmlApp.executeAndRender("DELETE FROM PdmlCount WHERE Id > 0"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "0 rows in set") || !strings.Contains(got, "at least 2 rows in set") {
		t.Fatalf("partitioned DML summary:\n%s", got)
	}
	var n int64
	if err := normal.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM PdmlCount").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("deleted rows remaining = %d", n)
	}
}

func TestIntegrationExecuteAndRenderSelect1(t *testing.T) {
	if out := integrationExecOutput(t, "SELECT 1;"); !strings.Contains(out, "1 row in set") {
		t.Fatalf("expected row summary in output:\n%s", out)
	}
}

func TestIntegrationMultiStatementDisplay(t *testing.T) {
	out := integrationExecOutput(t, "SELECT 1 AS x; SELECT 2 AS y;")
	if strings.Count(out, "row in set") < 2 {
		t.Fatalf("want two row summaries, got:\n%s", out)
	}
	if !strings.Contains(out, "| x") || !strings.Contains(out, "| y") {
		t.Fatalf("expected column headers x and y in output:\n%s", out)
	}
	// Blank line before the second ASCII table (delimiter "\n\n+" strips the leading '+' from the split tail).
	if parts := strings.Split(out, "\n\n+"); len(parts) < 2 {
		t.Fatalf("expected a blank line between multi-statement result blocks, got:\n%s", out)
	}
}

func TestIntegrationExplainDDLDoesNotChangeSchema(t *testing.T) {
	cli := openIntegrationApp(t, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
	var buf bytes.Buffer
	cli.out = &buf
	statements := []string{
		"EXPLAIN CREATE TABLE ExplainReject (Id INT64 NOT NULL) PRIMARY KEY (Id)",
		"/* note */ EXPLAIN CREATE TABLE ExplainReject2 (Id INT64 NOT NULL) PRIMARY KEY (Id)",
		"EXPLAIN /* note */ DROP TABLE ExplainReject",
	}
	for _, sqlText := range statements {
		buf.Reset()
		err := cli.executeAndRender(sqlText)
		if err == nil || !strings.Contains(err.Error(), "does not support DDL") {
			t.Fatalf("%s: err = %v", sqlText, err)
		}
	}
	var n int
	if err := cli.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME IN ('ExplainReject', 'ExplainReject2')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("EXPLAIN DDL created %d tables", n)
	}
}

func TestIntegrationLineCommentDoesNotSwallowNextStatement(t *testing.T) {
	for _, dialect := range []databasepb.DatabaseDialect{
		databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL,
		databasepb.DatabaseDialect_POSTGRESQL,
	} {
		t.Run(dialect.String(), func(t *testing.T) {
			out := integrationExecOutputFormat(t, outputFormatTable, dialect, "SELECT 1 AS x -- trailing comment\n; SELECT 2 AS y;")
			if strings.Count(out, "row in set") < 2 || !strings.Contains(out, "| x") || !strings.Contains(out, "| y") {
				t.Fatalf("want both statements, got:\n%s", out)
			}
		})
	}
}

func TestIntegrationExplainPlanSelect1(t *testing.T) {
	// PLAN mode; emulator may omit plan nodes and produce no visible output — must complete without error.
	_ = integrationExecOutput(t, "EXPLAIN SELECT 1;")
}

func TestIntegrationExplainAnalyzeSelect1(t *testing.T) {
	if out := integrationExecOutput(t, "EXPLAIN ANALYZE SELECT 1;"); !strings.Contains(out, "1 row in set") {
		t.Fatalf("expected row summary in output:\n%s", out)
	}
	// Emulator may return an empty plan tree; real instances usually print "Operator".
}

func TestIntegrationExplainAnalyzeTwoStatements(t *testing.T) {
	// PLAN-only EXPLAIN often prints nothing on the emulator when there are no plan nodes; PROFILE still yields row summaries.
	out := integrationExecOutput(t, "EXPLAIN ANALYZE SELECT 1; EXPLAIN ANALYZE SELECT 2;")
	if strings.Count(out, "row in set") < 2 {
		t.Fatalf("want two PROFILE row summaries, got:\n%s", out)
	}
	if !strings.Contains(out, "\n\n") {
		t.Fatalf("expected a blank line between sequential statements, got:\n%s", out)
	}
}

// TestIntegrationMultiStatementStats walks two SELECTs in one QueryContext: each statement yields
// metadata → data → ResultSetStats (go-sql-spanner multi-sql / NextResultSet). The emulator does not
// populate a query plan tree, but PROFILE should still attach QueryStats on ResultSetStats.
func TestIntegrationMultiStatementStats(t *testing.T) {
	assertMultiStatementProfileQueryStats(t, openIntegrationDB(t))
}

func assertMultiStatementProfileQueryStats(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT 1 AS x; SELECT 2 AS y;", buildExecOptions(sppb.ExecuteSqlRequest_PROFILE.Enum()))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	wantNames := []string{"x", "y"}
	for si := range 2 {
		rsm, err := fetchSingleValueInResultSet[*sppb.ResultSetMetadata](rows)
		if err != nil {
			t.Fatalf("statement %d metadata: %v", si, err)
		}
		fields := rsm.GetRowType().GetFields()
		if len(fields) != 1 || fields[0].GetName() != wantNames[si] {
			t.Fatalf("statement %d: metadata fields = %v, want one column %q", si, fieldNames(fields), wantNames[si])
		}

		n, err := drainResultSet(rsm, rows)
		if err != nil {
			t.Fatalf("statement %d data rows: %v", si, err)
		}
		if n != 1 {
			t.Fatalf("statement %d: got %d data rows, want 1", si, n)
		}

		rss, err := fetchResultSetStatsAfterDataRows(rows)
		if err != nil {
			t.Fatalf("statement %d ResultSetStats: %v", si, err)
		}
		if rss == nil {
			t.Fatalf("statement %d: ResultSetStats is nil", si)
		}
		if rss.GetQueryStats() == nil {
			t.Fatalf("statement %d: QueryStats nil (PROFILE on emulator should still attach query_stats)", si)
		}
	}

	if rows.Next() {
		t.Fatal("unexpected extra row after two statements")
	}
	if rows.NextResultSet() {
		t.Fatal("unexpected extra result set after two statements")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func fieldNames(fields []*sppb.StructType_Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.GetName()
	}
	return out
}

func TestIntegrationPostgreSQLExecuteAndRenderSelect1(t *testing.T) {
	out := integrationPostgreSQLExecOutput(t, outputFormatTable, "SELECT 1;")
	if !strings.Contains(out, "1 row in set") {
		t.Fatalf("expected row summary in output:\n%s", out)
	}
}

func TestIntegrationPostgreSQLMultiStatementDisplay(t *testing.T) {
	out := integrationPostgreSQLExecOutput(t, outputFormatTable, "SELECT 1 AS x; SELECT 2 AS y;")
	if strings.Count(out, "row in set") < 2 {
		t.Fatalf("want two row summaries, got:\n%s", out)
	}
	if !strings.Contains(out, "| x") || !strings.Contains(out, "| y") {
		t.Fatalf("expected column headers x and y in output:\n%s", out)
	}
	if parts := strings.Split(out, "\n\n+"); len(parts) < 2 {
		t.Fatalf("expected a blank line between multi-statement result blocks, got:\n%s", out)
	}
}

func TestIntegrationPostgreSQLExplainAnalyzeSelect1(t *testing.T) {
	out := integrationPostgreSQLExecOutput(t, outputFormatTable, "EXPLAIN ANALYZE SELECT 1;")
	if !strings.Contains(out, "1 row in set") {
		t.Fatalf("expected row summary in output:\n%s", out)
	}
}

func TestIntegrationPostgreSQLExplainAnalyzeTwoStatements(t *testing.T) {
	out := integrationPostgreSQLExecOutput(t, outputFormatTable, "EXPLAIN ANALYZE SELECT 1; EXPLAIN ANALYZE SELECT 2;")
	if strings.Count(out, "row in set") < 2 {
		t.Fatalf("want two PROFILE row summaries, got:\n%s", out)
	}
	if !strings.Contains(out, "\n\n") {
		t.Fatalf("expected a blank line between sequential statements, got:\n%s", out)
	}
}

// TestIntegrationPostgreSQLMultiStatementStats mirrors TestIntegrationMultiStatementStats for a PostgreSQL-dialect database.
func TestIntegrationPostgreSQLMultiStatementStats(t *testing.T) {
	assertMultiStatementProfileQueryStats(t, openIntegrationDBPostgreSQL(t))
}

// TestIntegrationPostgreSQLTableHeaderSpantype marks INT64 columns with a PostgreSQL-oriented type label, not GoogleSQL INT64.
func TestIntegrationPostgreSQLTableHeaderSpantype(t *testing.T) {
	out := integrationPostgreSQLExecOutput(t, outputFormatTable, "SELECT 1 AS n;")
	lo := strings.ToLower(out)
	if !strings.Contains(lo, "bigint") {
		t.Fatalf("expected PostgreSQL-style bigint in typed table header, got:\n%s", out)
	}
}

func TestIntegrationPostgreSQLCSVExport(t *testing.T) {
	out := integrationPostgreSQLExecOutput(t, outputFormatCSV, "SELECT 42 AS answer;")
	if !strings.Contains(out, "answer") || !strings.Contains(out, "42") {
		t.Fatalf("expected csv header and value, got:\n%s", out)
	}
}

func TestIntegrationGoogleSQLCSVExport(t *testing.T) {
	out := integrationExecOutputFormat(t, outputFormatCSV, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, "SELECT 42 AS answer;")
	if !strings.Contains(out, "answer") || !strings.Contains(out, "42") {
		t.Fatalf("expected csv header and value, got:\n%s", out)
	}
}

// Zero-row SELECT still emits a CSV header via spanvalue writer Flush (v0.4+).
func TestIntegrationCSVZeroRows(t *testing.T) {
	out := integrationExecOutputFormat(t, outputFormatCSV, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, "SELECT 1 AS x LIMIT 0;")
	if !strings.Contains(out, "x") {
		t.Fatalf("expected csv column header x, got:\n%s", out)
	}
	if !strings.Contains(out, "0 rows in set") {
		t.Fatalf("expected zero-row summary, got:\n%s", out)
	}
}

func TestIntegrationPostgreSQLJSONLExport(t *testing.T) {
	out := integrationPostgreSQLExecOutput(t, outputFormatJSONL, "SELECT 'pg' AS dialect;")
	if !strings.Contains(out, "dialect") || !strings.Contains(out, "pg") {
		t.Fatalf("expected jsonl keys/values, got:\n%s", out)
	}
}
