package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/apstndb/spantype"
	spannerdriver "github.com/googleapis/go-sql-spanner"
)

func TestParseCLIDialect(t *testing.T) {
	pos := []struct {
		in       string
		want     databasepb.DatabaseDialect
		wantAuto bool
	}{
		{"auto", databasepb.DatabaseDialect_DATABASE_DIALECT_UNSPECIFIED, true},
		{"AUTO", databasepb.DatabaseDialect_DATABASE_DIALECT_UNSPECIFIED, true},
		{"google-standard-sql", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, false},
		{"GOOGLE_STANDARD_SQL", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, false},
		{"googlesql", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, false},
		{"postgresql", databasepb.DatabaseDialect_POSTGRESQL, false},
		{"POSTGRES", databasepb.DatabaseDialect_POSTGRESQL, false},
		{"pg", databasepb.DatabaseDialect_POSTGRESQL, false},
	}
	for _, tc := range pos {
		got, auto, err := parseCLIDialect(tc.in)
		if err != nil || got != tc.want || auto != tc.wantAuto {
			t.Fatalf("parseCLIDialect(%q) = (%v, %v, %v), want (%v, %v, nil)", tc.in, got, auto, err, tc.want, tc.wantAuto)
		}
	}
	if _, _, err := parseCLIDialect("nope"); err == nil {
		t.Fatal("expected error for invalid dialect")
	}
}

func TestDialectFromDatabaseOptionValue(t *testing.T) {
	d, err := dialectFromDatabaseOptionValue("GOOGLE_STANDARD_SQL")
	if err != nil || d != databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL {
		t.Fatalf("got (%v, %v)", d, err)
	}
	d, err = dialectFromDatabaseOptionValue("POSTGRESQL")
	if err != nil || d != databasepb.DatabaseDialect_POSTGRESQL {
		t.Fatalf("got (%v, %v)", d, err)
	}
	if _, err := dialectFromDatabaseOptionValue("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestOutputFormatFromString(t *testing.T) {
	tests := []struct {
		in   string
		want outputFormat
	}{
		{"table", outputFormatTable},
		{"TABLE", outputFormatTable},
		{" Table ", outputFormatTable},
		{"csv", outputFormatCSV},
		{"CSV", outputFormatCSV},
		{"jsonl", outputFormatJSONL},
		{"JSONL", outputFormatJSONL},
		{"unknown", outputFormatTable},
		{"", outputFormatTable},
	}
	for _, tc := range tests {
		if got := outputFormatFromString(tc.in); got != tc.want {
			t.Fatalf("outputFormatFromString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestComposeSpannerDSN(t *testing.T) {
	tests := []struct {
		project, instance, database string
		dialect                     databasepb.DatabaseDialect
		suffix, want                string
	}{
		{"p", "i", "d", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, "", "projects/p/instances/i/databases/d"},
		{"p", "i", "d", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, "usePlainText=true", "projects/p/instances/i/databases/d;usePlainText=true"},
		{"p", "i", "d", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, ";usePlainText=true", "projects/p/instances/i/databases/d;usePlainText=true"},
		{"p", "i", "d", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, "  ;  usePlainText=true  ", "projects/p/instances/i/databases/d;usePlainText=true"},
		{"p", "i", "d", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, "a=1;b=2", "projects/p/instances/i/databases/d;a=1;b=2"},
		{"p", "i", "d", databasepb.DatabaseDialect_POSTGRESQL, "usePlainText=true", "projects/p/instances/i/databases/d;dialect=POSTGRESQL;usePlainText=true"},
		{"p", "i", "d", databasepb.DatabaseDialect_POSTGRESQL, "dialect=POSTGRESQL;usePlainText=true", "projects/p/instances/i/databases/d;dialect=POSTGRESQL;usePlainText=true"},
	}
	for _, tc := range tests {
		got := composeSpannerDSN(tc.project, tc.instance, tc.database, tc.dialect, tc.suffix)
		if got != tc.want {
			t.Fatalf("composeSpannerDSN(..., dialect=%v, %q) = %q, want %q", tc.dialect, tc.suffix, got, tc.want)
		}
	}
}

func TestBuildExecOptions(t *testing.T) {
	prof := buildExecOptions(sppb.ExecuteSqlRequest_PROFILE.Enum())
	if prof.QueryOptions.Mode == nil || *prof.QueryOptions.Mode != sppb.ExecuteSqlRequest_PROFILE {
		t.Fatalf("PROFILE mode = %v", prof.QueryOptions.Mode)
	}
	if !prof.ReturnResultSetMetadata || !prof.ReturnResultSetStats {
		t.Fatal("PROFILE metadata/stats")
	}
	if prof.DecodeOption != spannerdriver.DecodeOptionProto || !prof.DirectExecuteQuery {
		t.Fatal("PROFILE driver options")
	}
	plan := buildExecOptions(sppb.ExecuteSqlRequest_PLAN.Enum())
	if plan.QueryOptions.Mode == nil || *plan.QueryOptions.Mode != sppb.ExecuteSqlRequest_PLAN {
		t.Fatalf("PLAN mode = %v", plan.QueryOptions.Mode)
	}
}

func TestPrepareQuery(t *testing.T) {
	tests := []struct {
		input    string
		wantExec string
		wantMode sppb.ExecuteSqlRequest_QueryMode
		wantKind stmtDisplayKind
	}{
		{"SELECT 1", "SELECT 1", sppb.ExecuteSqlRequest_PROFILE, stmtDisplayQueryResult},
		{"SELECT 1;", "SELECT 1", sppb.ExecuteSqlRequest_PROFILE, stmtDisplayQueryResult},
		{"  select 1 ", "select 1", sppb.ExecuteSqlRequest_PROFILE, stmtDisplayQueryResult},
		{"EXPLAIN SELECT 1", "SELECT 1", sppb.ExecuteSqlRequest_PLAN, stmtDisplayPlanOnlyPlan},
		{"explain\nselect 1", "select 1", sppb.ExecuteSqlRequest_PLAN, stmtDisplayPlanOnlyPlan},
		{"EXPLAIN SELECT 1;", "SELECT 1", sppb.ExecuteSqlRequest_PLAN, stmtDisplayPlanOnlyPlan},
		{"EXPLAIN ANALYZE SELECT 1", "SELECT 1", sppb.ExecuteSqlRequest_PROFILE, stmtDisplayPlanOnlyProfile},
		{"explain analyze select 1", "select 1", sppb.ExecuteSqlRequest_PROFILE, stmtDisplayPlanOnlyProfile},
		{"EXPLAIN ANALYZERS SELECT 1", "ANALYZERS SELECT 1", sppb.ExecuteSqlRequest_PLAN, stmtDisplayPlanOnlyPlan},
		{"EXPLAINSELECT 1", "EXPLAINSELECT 1", sppb.ExecuteSqlRequest_PROFILE, stmtDisplayQueryResult},
		{"EXPLAIN;", "", sppb.ExecuteSqlRequest_PLAN, stmtDisplayPlanOnlyPlan},
		{"EXPLAIN ANALYZE;", "", sppb.ExecuteSqlRequest_PROFILE, stmtDisplayPlanOnlyProfile},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			pq := prepareQuery(tc.input)
			if pq.execSQL != tc.wantExec {
				t.Fatalf("execSQL = %q, want %q", pq.execSQL, tc.wantExec)
			}
			if pq.mode == nil || *pq.mode != tc.wantMode {
				t.Fatalf("mode = %v, want %v", pq.mode, tc.wantMode)
			}
			if pq.kind != tc.wantKind {
				t.Fatalf("kind = %v, want %v", pq.kind, tc.wantKind)
			}
		})
	}
}

func TestPlanExecution(t *testing.T) {
	t.Run("one batch two selects", func(t *testing.T) {
		plan, err := planExecution("SELECT 1; SELECT 2;", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.batches) != 1 || len(plan.batches[0]) != 2 {
			t.Fatalf("want 1 batch of 2, got %v", plan.batches)
		}
		if g, w := joinBatchExecSQL(plan.batches[0]), "SELECT 1; SELECT 2"; g != w {
			t.Fatalf("execSQL = %q, want %q", g, w)
		}
	})
	t.Run("one batch two explain plan", func(t *testing.T) {
		plan, err := planExecution("EXPLAIN SELECT 1; EXPLAIN SELECT 2;", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.batches) != 1 || len(plan.batches[0]) != 2 {
			t.Fatalf("want 1 batch of 2, got %v", plan.batches)
		}
		if g, w := joinBatchExecSQL(plan.batches[0]), "SELECT 1; SELECT 2"; g != w {
			t.Fatalf("execSQL = %q, want %q", g, w)
		}
	})
	t.Run("two batches plan then profile", func(t *testing.T) {
		plan, err := planExecution("EXPLAIN SELECT 1; SELECT 2;", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.batches) != 2 {
			t.Fatalf("want 2 batches, got %d", len(plan.batches))
		}
		if plan.batches[0][0].kind != stmtDisplayPlanOnlyPlan || plan.batches[1][0].kind != stmtDisplayQueryResult {
			t.Fatalf("kinds = %v, %v", plan.batches[0][0].kind, plan.batches[1][0].kind)
		}
	})
	t.Run("one batch explain analyze plus select", func(t *testing.T) {
		plan, err := planExecution("EXPLAIN ANALYZE SELECT 1; SELECT 2;", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.batches) != 1 || len(plan.batches[0]) != 2 {
			t.Fatalf("want 1 batch of 2 (both PROFILE), got %v", plan.batches)
		}
		if plan.batches[0][0].kind != stmtDisplayPlanOnlyProfile || plan.batches[0][1].kind != stmtDisplayQueryResult {
			t.Fatalf("kinds = %v, %v", plan.batches[0][0].kind, plan.batches[0][1].kind)
		}
	})
	t.Run("one batch two explain analyze", func(t *testing.T) {
		plan, err := planExecution("EXPLAIN ANALYZE SELECT 1; EXPLAIN ANALYZE SELECT 2;", databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.batches) != 1 || len(plan.batches[0]) != 2 {
			t.Fatalf("want 1 batch of 2, got %v", plan.batches)
		}
	})
}

func TestSplitIntoStatementsQuotedSemicolon(t *testing.T) {
	got, err := splitIntoStatements(`SELECT 'a;b'; SELECT 2`, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d parts: %q", len(got), got)
	}
	if !strings.Contains(got[0], "a;b") {
		t.Fatalf("first part %q should keep semicolon inside string", got[0])
	}
}

func TestExitCommand(t *testing.T) {
	positive := []string{"exit", "EXIT;", "  quit  ", "quit;"}
	for _, s := range positive {
		if !reExitCommand.MatchString(s) {
			t.Fatalf("%q should match", s)
		}
	}
	negative := []string{"select exit", "exito", ""}
	for _, s := range negative {
		if reExitCommand.MatchString(s) {
			t.Fatalf("%q should not match", s)
		}
	}
}

func TestReplInputComplete(t *testing.T) {
	tests := []struct {
		lines []string
		want  bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{""}, false},
		{[]string{" ", "\t"}, false},
		{[]string{"SELECT 1"}, false},
		{[]string{"SELECT 1;"}, true},
		{[]string{"SELECT 1", ";"}, true},
		{[]string{"SELECT 1;", "SELECT 2;"}, true},
		{[]string{"exit"}, true},
		{[]string{"  EXIT;  "}, true},
		{[]string{"SELECT 1", "exit"}, false},
	}
	for _, tc := range tests {
		if got := replInputComplete(tc.lines); got != tc.want {
			t.Fatalf("replInputComplete(%q) = %v, want %v", tc.lines, got, tc.want)
		}
	}
}

func TestRenderHeader(t *testing.T) {
	fields := []*sppb.StructType_Field{
		{Name: "k", Type: &sppb.Type{Code: sppb.TypeCode_INT64}},
	}
	t.Run("GoogleSQL", func(t *testing.T) {
		h := renderHeader(fields, databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL)
		if len(h) != 1 {
			t.Fatalf("len = %d", len(h))
		}
		if !strings.HasPrefix(h[0], "k\n") {
			t.Fatalf("got %q", h[0])
		}
		wantType := spantype.FormatTypeNormal(fields[0].GetType())
		if !strings.Contains(h[0], wantType) {
			t.Fatalf("got %q, want substring %q", h[0], wantType)
		}
	})
	t.Run("PostgreSQL", func(t *testing.T) {
		h := renderHeader(fields, databasepb.DatabaseDialect_POSTGRESQL)
		if len(h) != 1 {
			t.Fatalf("len = %d", len(h))
		}
		if h[0] != "k\nbigint" {
			t.Fatalf("got %q, want k + newline + bigint", h[0])
		}
	})
}

func TestFinishCSVWriteReportsFlushError(t *testing.T) {
	flushErr := errors.New("flush failed")
	if err := finishCSVWrite(func() error { return flushErr }, nil); !errors.Is(err, flushErr) {
		t.Fatalf("finishCSVWrite error = %v, want flush error", err)
	}
}

func TestFinishCSVWriteJoinsRowAndFlushErrors(t *testing.T) {
	rowErr := errors.New("row failed")
	flushErr := errors.New("flush failed")
	err := finishCSVWrite(func() error { return flushErr }, rowErr)
	if !errors.Is(err, rowErr) || !errors.Is(err, flushErr) {
		t.Fatalf("finishCSVWrite error = %v, want joined row and flush errors", err)
	}
}

func TestSpannerEnvDefaults(t *testing.T) {
	t.Setenv("SPANNER_PROJECT_ID", "proj-env")
	t.Setenv("SPANNER_INSTANCE_ID", "inst-env")
	t.Setenv("SPANNER_DATABASE_ID", "db-env")
	opts, err := parseCLIOpts(nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Project != "proj-env" || opts.Instance != "inst-env" || opts.Database != "db-env" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestSpannerFlagOverridesEnv(t *testing.T) {
	t.Setenv("SPANNER_PROJECT_ID", "proj-env")
	t.Setenv("SPANNER_INSTANCE_ID", "inst-env")
	t.Setenv("SPANNER_DATABASE_ID", "db-env")
	opts, err := parseCLIOpts([]string{"-p", "proj-flag", "-i", "inst-flag", "-d", "db-flag"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Project != "proj-flag" || opts.Instance != "inst-flag" || opts.Database != "db-flag" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseCLIOptsHelpRequestsExit(t *testing.T) {
	var out bytes.Buffer
	_, err := parseCLIOpts([]string{"--help"}, &out, io.Discard)
	var cliExit cliExitError
	if !errors.As(err, &cliExit) || cliExit.code != 0 {
		t.Fatalf("parseCLIOpts(--help) error = %T %v, want cliExitError(0)", err, err)
	}
	if !strings.Contains(out.String(), "Usage: spannersh") {
		t.Fatalf("help output = %q", out.String())
	}
}
