package dbsqlrows

// DataMode controls whether scanned data rows invoke WriteDataRow.
type DataMode int

const (
	// DataModeProcess calls WriteDataRow for each data row (table, csv, jsonl).
	DataModeProcess DataMode = iota
	// DataModeDrain counts data rows without WriteDataRow (EXPLAIN / EXPLAIN ANALYZE).
	DataModeDrain
)
