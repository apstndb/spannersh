package main

import (
	"strings"
	"testing"

	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
)

func TestPrepareQueryCommentsAndBoundaries(t *testing.T) {
	google := databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL
	pg := databasepb.DatabaseDialect_POSTGRESQL
	tests := []struct {
		name     string
		dialect  databasepb.DatabaseDialect
		input    string
		wantExec string
		wantMode sppb.ExecuteSqlRequest_QueryMode
		wantKind stmtDisplayKind
	}{
		{
			name:     "leading block comment",
			dialect:  google,
			input:    "/* note */ EXPLAIN SELECT 1",
			wantExec: "SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PLAN,
			wantKind: stmtDisplayPlanOnlyPlan,
		},
		{
			name:     "comment between explain and analyze",
			dialect:  google,
			input:    "EXPLAIN /* note */ ANALYZE SELECT 1",
			wantExec: "SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PROFILE,
			wantKind: stmtDisplayPlanOnlyProfile,
		},
		{
			name:     "hash comment before explain",
			dialect:  google,
			input:    "# note\nEXPLAIN SELECT 1",
			wantExec: "SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PLAN,
			wantKind: stmtDisplayPlanOnlyPlan,
		},
		{
			name:     "hash is not a postgresql comment",
			dialect:  pg,
			input:    "# note\nEXPLAIN SELECT 1",
			wantExec: "# note\nEXPLAIN SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PROFILE,
			wantKind: stmtDisplayQueryResult,
		},
		{
			name:     "nested postgresql comment before explain",
			dialect:  pg,
			input:    "/* outer /* inner */ still */ EXPLAIN SELECT 1",
			wantExec: "SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PLAN,
			wantKind: stmtDisplayPlanOnlyPlan,
		},
		{
			name:     "googlesql does not nest block comments",
			dialect:  google,
			input:    "/* outer /* inner */ still */ EXPLAIN SELECT 1",
			wantExec: "/* outer /* inner */ still */ EXPLAIN SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PROFILE,
			wantKind: stmtDisplayQueryResult,
		},
		{
			name:     "unterminated comment is not explain",
			dialect:  google,
			input:    "/* EXPLAIN SELECT 1",
			wantExec: "/* EXPLAIN SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PROFILE,
			wantKind: stmtDisplayQueryResult,
		},
		{
			name:     "literal is not explain",
			dialect:  google,
			input:    "SELECT 'EXPLAIN SELECT 1'",
			wantExec: "SELECT 'EXPLAIN SELECT 1'",
			wantMode: sppb.ExecuteSqlRequest_PROFILE,
			wantKind: stmtDisplayQueryResult,
		},
		{
			name:     "partial identifier",
			dialect:  google,
			input:    "EXPLAINSELECT 1",
			wantExec: "EXPLAINSELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PROFILE,
			wantKind: stmtDisplayQueryResult,
		},
		{
			name:     "analyzers is not analyze",
			dialect:  google,
			input:    "EXPLAIN ANALYZERS SELECT 1",
			wantExec: "ANALYZERS SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PLAN,
			wantKind: stmtDisplayPlanOnlyPlan,
		},
		{
			name:     "comment preserved before select",
			dialect:  google,
			input:    "EXPLAIN /* keep */ SELECT 1",
			wantExec: "/* keep */ SELECT 1",
			wantMode: sppb.ExecuteSqlRequest_PLAN,
			wantKind: stmtDisplayPlanOnlyPlan,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pq := prepareQuery(tc.input, tc.dialect)
			if pq.execSQL != tc.wantExec || pq.mode == nil || *pq.mode != tc.wantMode || pq.kind != tc.wantKind {
				t.Fatalf("got exec %q mode %v kind %v", pq.execSQL, pq.mode, pq.kind)
			}
		})
	}
}

func TestPlanExecutionRejectsNonExplainable(t *testing.T) {
	google := databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL
	tests := []struct {
		input   string
		wantErr string
	}{
		{"EXPLAIN CREATE TABLE T (Id INT64 NOT NULL) PRIMARY KEY (Id)", "EXPLAIN does not support DDL"},
		{"EXPLAIN /* c */ DROP TABLE T", "EXPLAIN does not support DDL"},
		{"/* c */ EXPLAIN ALTER TABLE T ADD COLUMN C INT64", "EXPLAIN does not support DDL"},
		{"EXPLAIN ANALYZE CREATE TABLE T (Id INT64) PRIMARY KEY (Id)", "EXPLAIN ANALYZE does not support DDL"},
		{"EXPLAIN TRUNCATE TABLE T", "EXPLAIN does not support this statement"},
		{"EXPLAIN SET statement_timeout = '1s'", "EXPLAIN does not support client-side statements"},
		{"EXPLAIN BEGIN", "EXPLAIN does not support client-side statements"},
		{"EXPLAIN START BATCH DDL", "EXPLAIN does not support client-side statements"},
		{"EXPLAIN /* c */ ANALYZE SET AUTOCOMMIT = TRUE", "EXPLAIN ANALYZE does not support client-side statements"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			_, err := planExecution(tc.input, google)
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPlanExecutionAllowsExplainableStatements(t *testing.T) {
	google := databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL
	inputs := []string{
		"EXPLAIN SELECT 1",
		"EXPLAIN WITH t AS (SELECT 1) SELECT * FROM t",
		"EXPLAIN @{OPTIMIZER_VERSION=1} SELECT 1",
		"EXPLAIN INSERT INTO T (Id) VALUES (1)",
		"EXPLAIN ANALYZE UPDATE T SET Id = 1 WHERE Id = 1",
		"EXPLAIN ANALYZE DELETE FROM T WHERE Id = 1",
		"SELECT 1; EXPLAIN CREATE TABLE T (Id INT64 NOT NULL) PRIMARY KEY (Id)",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			_, err := planExecution(input, google)
			wantErr := strings.Contains(input, "CREATE TABLE")
			if wantErr {
				if err == nil {
					t.Fatal("expected DDL rejection before any batch would run")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJoinBatchPreservesLineCommentNewline(t *testing.T) {
	google := databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL
	pg := databasepb.DatabaseDialect_POSTGRESQL
	tests := []struct {
		name    string
		dialect databasepb.DatabaseDialect
		input   string
		want    string
	}{
		{
			name:    "googlesql dash comment",
			dialect: google,
			input:   "SELECT 1 AS x -- trailing comment\n; SELECT 2 AS y",
			want:    "SELECT 1 AS x -- trailing comment\n; SELECT 2 AS y",
		},
		{
			name:    "googlesql hash comment",
			dialect: google,
			input:   "SELECT 1 AS x # trailing\n; SELECT 2 AS y",
			want:    "SELECT 1 AS x # trailing\n; SELECT 2 AS y",
		},
		{
			name:    "postgresql dash comment",
			dialect: pg,
			input:   "SELECT 1 AS x -- trailing comment\n; SELECT 2 AS y",
			want:    "SELECT 1 AS x -- trailing comment\n; SELECT 2 AS y",
		},
		{
			name:    "postgresql hash is not a comment",
			dialect: pg,
			input:   "SELECT 1 AS x # trailing\n; SELECT 2 AS y",
			want:    "SELECT 1 AS x # trailing; SELECT 2 AS y",
		},
		{
			name:    "comment marker inside a string",
			dialect: google,
			input:   "SELECT '-- not a comment'; SELECT 2",
			want:    "SELECT '-- not a comment'; SELECT 2",
		},
		{
			name:    "semicolon only separator is not used for comments",
			dialect: google,
			input:   "SELECT 1 -- c\n; SELECT 2 -- d\n; SELECT 3",
			want:    "SELECT 1 -- c\n; SELECT 2 -- d\n; SELECT 3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planExecution(tc.input, tc.dialect)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.batches) != 1 {
				t.Fatalf("batches = %d", len(plan.batches))
			}
			if got := joinBatchExecSQL(plan.batches[0]); got != tc.want {
				t.Fatalf("join = %q, want %q", got, tc.want)
			}
		})
	}
}
