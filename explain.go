package main

import (
	"fmt"
	"regexp"
	"strings"

	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/googleapis/go-sql-spanner/parser"
)

// preparedQuery is the SQL and mode after client-side EXPLAIN handling, ready for go-sql-spanner.
type preparedQuery struct {
	execSQL string
	mode    *sppb.ExecuteSqlRequest_QueryMode
	kind    stmtDisplayKind
}

// stmtDisplayKind controls whether we print the data result set, execution summary, and/or the plan tree.
type stmtDisplayKind int

const (
	// stmtDisplayQueryResult: normal statement — PROFILE + data rows + execution summary (no plan tree).
	stmtDisplayQueryResult stmtDisplayKind = iota
	// stmtDisplayPlanOnlyProfile: EXPLAIN ANALYZE — PROFILE + plan tree, row summary, stats.
	stmtDisplayPlanOnlyProfile
	// stmtDisplayPlanOnlyPlan: EXPLAIN — PLAN + plan tree only (no row summary / stats).
	stmtDisplayPlanOnlyPlan
)

// Strip Spanner EXPLAIN / EXPLAIN ANALYZE on the client and use ExecuteSqlRequest QueryMode (PLAN / PROFILE) instead.
// The leading (EXPLAIN\s+ANALYZE\b|…) branch detects ANALYZE (\b keeps EXPLAIN ANALYZERS … on the PLAN path).
var reStripExplainPrefix = regexp.MustCompile(`(?is)^(?:(EXPLAIN\s+ANALYZE\b)|EXPLAIN)(?:\s+(.*))?\s*;?\s*$`)

func trimExecSQL(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 0 && s[len(s)-1] == ';' {
		s = strings.TrimSpace(s[:len(s)-1])
	}
	return s
}

// prepareQuery returns SQL for go-sql-spanner (EXPLAIN prefix stripped) plus execution/display mode.
func prepareQuery(sql string) preparedQuery {
	s := strings.TrimSpace(sql)
	if m := reStripExplainPrefix.FindStringSubmatch(s); m != nil {
		execSQL := trimExecSQL(m[2])
		if m[1] != "" {
			return preparedQuery{execSQL, sppb.ExecuteSqlRequest_PROFILE.Enum(), stmtDisplayPlanOnlyProfile}
		}
		return preparedQuery{execSQL, sppb.ExecuteSqlRequest_PLAN.Enum(), stmtDisplayPlanOnlyPlan}
	}
	return preparedQuery{trimExecSQL(s), sppb.ExecuteSqlRequest_PROFILE.Enum(), stmtDisplayQueryResult}
}

// splitIntoStatements splits on top-level ';' using go-sql-spanner's parser (strings in literals/comments are respected).
func splitIntoStatements(input string, dialect databasepb.DatabaseDialect) ([]string, error) {
	sql := strings.TrimSpace(input)
	p, err := parser.NewStatementParser(effectiveStatementDialect(dialect), 0)
	if err != nil {
		return nil, err
	}
	multi, parts, err := p.Split(sql)
	if err != nil {
		return nil, err
	}
	if !multi {
		return []string{sql}, nil
	}
	var out []string
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no statements after split")
	}
	return out, nil
}

// executionPlan is a sequence of driver batches. Consecutive statements that share the same
// QueryMode (PLAN vs PROFILE) are joined with "; " and run in one QueryContext. Normal SELECT
// and EXPLAIN ANALYZE are both PROFILE and batch together; EXPLAIN (plan-only) is PLAN and
// splits batches from PROFILE. Display still uses each statement's own kind (plan vs table).
type executionPlan struct {
	batches [][]preparedQuery
}

func sameBatchKey(a, b preparedQuery) bool {
	if a.mode == nil || b.mode == nil {
		return a.mode == b.mode
	}
	return *a.mode == *b.mode
}

// groupIntoBatches merges consecutive preparedQuery values with the same batch key.
func groupIntoBatches(steps []preparedQuery) [][]preparedQuery {
	if len(steps) == 0 {
		return nil
	}
	var batches [][]preparedQuery
	var cur []preparedQuery
	for _, s := range steps {
		if len(cur) == 0 {
			cur = []preparedQuery{s}
			continue
		}
		if sameBatchKey(cur[len(cur)-1], s) {
			cur = append(cur, s)
		} else {
			batches = append(batches, cur)
			cur = []preparedQuery{s}
		}
	}
	return append(batches, cur)
}

// joinBatchExecSQL builds the SQL string sent to the driver for one batch (stripped inner SQL only).
func joinBatchExecSQL(batch []preparedQuery) string {
	parts := make([]string, len(batch))
	for i, s := range batch {
		parts[i] = s.execSQL
	}
	return strings.Join(parts, "; ")
}

// planExecution splits input with the driver parser, prepares each statement, then groups into batches.
func planExecution(raw string, dialect databasepb.DatabaseDialect) (executionPlan, error) {
	parts, err := splitIntoStatements(strings.TrimSpace(raw), dialect)
	if err != nil {
		return executionPlan{}, err
	}
	steps := make([]preparedQuery, len(parts))
	for i, p := range parts {
		steps[i] = prepareQuery(p)
	}
	return executionPlan{batches: groupIntoBatches(steps)}, nil
}
