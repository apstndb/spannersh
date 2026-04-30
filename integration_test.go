//go:build integration

package main

import (
	"bytes"
	"database/sql"
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
	db := openIntegrationDB(t)
	var buf bytes.Buffer
	cli := &app{ctx: t.Context(), out: &buf, db: db, format: outputFormatTable, dialect: databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL}
	if err := cli.executeAndRender(sql); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func integrationPostgreSQLExecOutput(t *testing.T, format outputFormat, sqlText string) string {
	t.Helper()
	db := openIntegrationDBPostgreSQL(t)
	var buf bytes.Buffer
	cli := &app{ctx: t.Context(), out: &buf, db: db, format: format, dialect: databasepb.DatabaseDialect_POSTGRESQL}
	if err := cli.executeAndRender(sqlText); err != nil {
		t.Fatal(err)
	}
	return buf.String()
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

// TestIntegrationPostgreSQLTableHeaderSpanpg marks INT64 columns with a PostgreSQL-oriented type label (spanpg), not GoogleSQL INT64.
func TestIntegrationPostgreSQLTableHeaderSpanpg(t *testing.T) {
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

func TestIntegrationPostgreSQLJSONLExport(t *testing.T) {
	out := integrationPostgreSQLExecOutput(t, outputFormatJSONL, "SELECT 'pg' AS dialect;")
	if !strings.Contains(out, "dialect") || !strings.Contains(out, "pg") {
		t.Fatalf("expected jsonl keys/values, got:\n%s", out)
	}
}
