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

// formatExecutionSummary prints "N row(s) in set" and, when present, query_stats lines after it.
func formatExecutionSummary(out io.Writer, rss *sppb.ResultSetStats, dataRowCount int) {
	stats := queryStatsMap(rss)
	n := effectiveRowCount(rss, dataRowCount)
	fmt.Fprintln(out, rowsInSetLine(n, stats))
	if rss == nil {
		fmt.Fprintln(out, "No execution statistics returned.")
		return
	}
	writeQueryStatsDetails(out, stats)
}

// writeExecutionStatsDetails prints query_stats key/values only when the API populated it.
func writeExecutionStatsDetails(out io.Writer, rss *sppb.ResultSetStats) {
	writeQueryStatsDetails(out, queryStatsMap(rss))
}

func writeQueryStatsDetails(out io.Writer, stats map[string]any) {
	if stats != nil {
		writeQueryStatsLines(out, stats)
	}
}

func queryStatsMap(rss *sppb.ResultSetStats) map[string]any {
	if qs := rss.GetQueryStats(); qs != nil {
		return qs.AsMap()
	}
	return nil
}

func writeQueryStatsLines(out io.Writer, m map[string]any) {
	if len(m) == 0 {
		fmt.Fprintln(out, "QueryStats: (empty)")
		return
	}
	keys := slices.DeleteFunc(slices.Sorted(maps.Keys(m)), func(k string) bool { return k == queryStatsOmitQueryTextKey })
	if len(keys) == 0 {
		return
	}
	maxKeyLen := len(slices.MaxFunc(keys, func(a, b string) int { return cmp.Compare(len(a), len(b)) }))
	for _, k := range keys {
		fmt.Fprintf(out, "%-*s: %v\n", maxKeyLen, k, m[k])
	}
}

func effectiveRowCount(rss *sppb.ResultSetStats, dataRowCount int) int {
	if exact := rss.GetRowCountExact(); exact > 0 {
		return int(exact)
	}
	return dataRowCount
}

func rowsInSetLine(n int, stats map[string]any) string {
	elapsed := elapsedForSummaryLine(stats)
	rowWord := "rows"
	if n == 1 {
		rowWord = "row"
	}
	if elapsed != "" {
		return fmt.Sprintf("%d %s in set (%s)", n, rowWord, elapsed)
	}
	return fmt.Sprintf("%d %s in set", n, rowWord)
}

// elapsedForSummaryLine returns query_stats.elapsed_time when the field is present.
func elapsedForSummaryLine(stats map[string]any) string {
	if v, ok := stats["elapsed_time"]; ok {
		return fmt.Sprint(v)
	}
	return ""
}
