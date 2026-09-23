package main

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
)

// Omitted from printed query_stats: user already typed the statement; it can be long or multiline.
const queryStatsOmitQueryTextKey = "query_text"

var defaultQueryStatsKeys = map[string]struct{}{
	"cpu_time":             {},
	"deleted_rows_scanned": {},
	"optimizer_version":    {},
	"rows_scanned":         {},
}

// formatExecutionSummary prints the exact or lower-bound row summary and,
// when present, query_stats lines after it.
func formatExecutionSummary(out io.Writer, rss *sppb.ResultSetStats, dataRowCount int, verbose bool) {
	stats := queryStatsMap(rss)
	fmt.Fprintln(out, rowsInSetLine(rowCountFromStats(rss, dataRowCount), stats))
	if rss == nil {
		fmt.Fprintln(out, "No execution statistics returned.")
		return
	}
	writeQueryStatsDetails(out, stats, verbose)
}

func writeQueryStatsDetails(out io.Writer, stats map[string]any, verbose bool) {
	if stats != nil {
		writeQueryStatsLines(out, stats, verbose)
	}
}

func queryStatsMap(rss *sppb.ResultSetStats) map[string]any {
	if qs := rss.GetQueryStats(); qs != nil {
		return qs.AsMap()
	}
	return nil
}

func writeQueryStatsLines(out io.Writer, m map[string]any, verbose bool) {
	if len(m) == 0 {
		fmt.Fprintln(out, "QueryStats: (empty)")
		return
	}
	keys := queryStatsKeysForDisplay(m, verbose)
	if len(keys) == 0 {
		return
	}
	maxKeyLen := len(slices.MaxFunc(keys, func(a, b string) int { return cmp.Compare(len(a), len(b)) }))
	for _, k := range keys {
		fmt.Fprintf(out, "%-*s: %v\n", maxKeyLen, k, m[k])
	}
}

func queryStatsKeysForDisplay(m map[string]any, verbose bool) []string {
	return slices.DeleteFunc(slices.Sorted(maps.Keys(m)), func(k string) bool {
		if k == queryStatsOmitQueryTextKey {
			return true
		}
		if verbose {
			return false
		}
		_, ok := defaultQueryStatsKeys[k]
		return !ok
	})
}

type rowCountKind int

const (
	rowCountFromData rowCountKind = iota
	rowCountExact
	rowCountLowerBound
)

type rowCount struct {
	kind rowCountKind
	n    int64
}

// rowCountFromStats uses the protobuf row-count oneof. Exact zero stays exact.
// A lower bound is never reported as exact. An absent oneof uses drained data rows.
// The count stays int64 so a 32-bit build does not truncate it.
func rowCountFromStats(rss *sppb.ResultSetStats, dataRowCount int) rowCount {
	switch rc := rss.GetRowCount().(type) {
	case *sppb.ResultSetStats_RowCountExact:
		return rowCount{kind: rowCountExact, n: rc.RowCountExact}
	case *sppb.ResultSetStats_RowCountLowerBound:
		return rowCount{kind: rowCountLowerBound, n: rc.RowCountLowerBound}
	default:
		return rowCount{kind: rowCountFromData, n: int64(dataRowCount)}
	}
}

func rowsInSetLine(count rowCount, stats map[string]any) string {
	rowWord := "rows"
	if count.n == 1 {
		rowWord = "row"
	}
	line := fmt.Sprintf("%d %s in set", count.n, rowWord)
	if count.kind == rowCountLowerBound {
		line = fmt.Sprintf("at least %d %s in set", count.n, rowWord)
	}
	if elapsed := elapsedForSummaryLine(stats); elapsed != "" {
		return line + " (" + elapsed + ")"
	}
	return line
}

// elapsedForSummaryLine returns query_stats.elapsed_time when the field is present.
func elapsedForSummaryLine(stats map[string]any) string {
	if v, ok := stats["elapsed_time"]; ok {
		return fmt.Sprint(v)
	}
	return ""
}
