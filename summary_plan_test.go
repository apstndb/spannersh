package main

import (
	"bytes"
	"strings"
	"testing"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestRowsInSetLine(t *testing.T) {
	tests := []struct {
		name  string
		rows  int
		stats map[string]any
		want  string
	}{
		{
			name:  "singular row with elapsed time",
			rows:  1,
			stats: map[string]any{"elapsed_time": "1 ms"},
			want:  "1 row in set (1 ms)",
		},
		{
			name: "plural rows without elapsed time",
			rows: 2,
			want: "2 rows in set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rowsInSetLine(tt.rows, tt.stats); got != tt.want {
				t.Fatalf("rowsInSetLine(%d, %v) = %q, want %q", tt.rows, tt.stats, got, tt.want)
			}
		})
	}
}

func TestFormatExecutionSummary(t *testing.T) {
	t.Run("nil stats falls back to data row count", func(t *testing.T) {
		var out bytes.Buffer
		formatExecutionSummary(&out, nil, 3, false)

		want := "3 rows in set\nNo execution statistics returned.\n"
		if got := out.String(); got != want {
			t.Fatalf("summary = %q, want %q", got, want)
		}
	})

	t.Run("exact row count and elapsed query stats", func(t *testing.T) {
		var out bytes.Buffer
		formatExecutionSummary(&out, testResultSetStats(t, 1, map[string]any{
			"cpu_time":     "4 ms",
			"elapsed_time": "5 ms",
			"query_text":   "SELECT 1",
			"rows_scanned": "9",
		}), 42, false)

		want := "1 row in set (5 ms)\ncpu_time    : 4 ms\nrows_scanned: 9\n"
		if got := out.String(); got != want {
			t.Fatalf("summary = %q, want %q", got, want)
		}
	})
}

func TestRenderQueryPlanFromStatsNilStatsReturnsError(t *testing.T) {
	var out bytes.Buffer
	err := renderQueryPlanFromStats(&out, nil, 0, stmtDisplayPlanOnlyPlan, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no result set for query stats") {
		t.Fatalf("error = %q, want query stats result-set error", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("output = %q, want empty", got)
	}
}

func TestRenderQueryPlanFromStatsPlanOnlyEmptyStatsPrintsNothing(t *testing.T) {
	var out bytes.Buffer
	err := renderQueryPlanFromStats(&out, &sppb.ResultSetStats{}, 0, stmtDisplayPlanOnlyPlan, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("output = %q, want empty", got)
	}
}

func TestRenderQueryPlanFromStatsProfilePrintsSummaryAndStats(t *testing.T) {
	var out bytes.Buffer
	err := renderQueryPlanFromStats(&out, testResultSetStats(t, 2, map[string]any{
		"cpu_time":     "3 ms",
		"elapsed_time": "11 ms",
		"query_text":   "SELECT 1",
		"rows_scanned": "7",
	}), 99, stmtDisplayPlanOnlyProfile, true)
	if err != nil {
		t.Fatal(err)
	}

	want := "2 rows in set (11 ms)\ncpu_time    : 3 ms\nelapsed_time: 11 ms\nrows_scanned: 7\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func testResultSetStats(t *testing.T, rowCountExact int64, queryStats map[string]any) *sppb.ResultSetStats {
	t.Helper()

	return &sppb.ResultSetStats{
		RowCount:   &sppb.ResultSetStats_RowCountExact{RowCountExact: rowCountExact},
		QueryStats: testQueryStats(t, queryStats),
	}
}

func testQueryStats(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()

	qs, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return qs
}
