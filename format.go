package main

import "strings"

// outputFormat selects how result rows are printed (query result sets only; EXPLAIN plan trees stay ASCII tables).
type outputFormat string

const (
	outputFormatTable outputFormat = "table"
	outputFormatCSV   outputFormat = "csv"
	outputFormatJSONL outputFormat = "jsonl"
)

func outputFormatFromString(s string) outputFormat {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "csv":
		return outputFormatCSV
	case "jsonl":
		return outputFormatJSONL
	case "table", "":
		return outputFormatTable
	default:
		return outputFormatTable
	}
}
