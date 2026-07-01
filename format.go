package main

import (
	"fmt"
	"strings"
)

// outputFormat selects how result rows are printed (query result sets only; EXPLAIN plan trees stay ASCII tables).
type outputFormat string

const (
	outputFormatTable outputFormat = "table"
	outputFormatCSV   outputFormat = "csv"
	outputFormatJSONL outputFormat = "jsonl"
)

func outputFormatFromString(s string) (outputFormat, error) {
	trimmed := strings.TrimSpace(s)
	switch strings.ToLower(trimmed) {
	case "csv":
		return outputFormatCSV, nil
	case "jsonl":
		return outputFormatJSONL, nil
	case "table":
		return outputFormatTable, nil
	default:
		return "", fmt.Errorf("invalid format %q (use table, csv, or jsonl)", trimmed)
	}
}
