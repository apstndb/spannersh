// Package dbsqlrows prototypes a database/sql row streaming layer for spannersh,
// modeled on [github.com/apstndb/spanvalue/writer.RunRowIterator] and
// [writer.RowIteratorWriter] but driven by [*database/sql.Rows] and
// []spanner.GenericColumnValue scans (go-sql-spanner DecodeOptionProto).
//
// # writer vs dbsqlrows
//
// | Path | Iterator | Row shape | sink |
// |------|----------|-----------|------|
// | Native client | [*spanner.RowIterator] | [*spanner.Row] | [writer.RowIteratorWriter] |
// | go-sql-spanner (this package) | [*sql.Rows] at data rows | []GCV | [StatementSink] |
//
// [StatementSink] covers table ([TableRenderer]), CSV/JSONL ([GCVStreamWriter] via
// [SQLRowsHooksFromGCVWriter]), and custom sinks. [DataModeDrain] skips [WriteDataRow]
// while still counting rows and optionally reading [SQLRowsResult.Stats] for EXPLAIN.
//
// spannersh usually consumes the metadata pseudo-row before calling [RunAtData];
// [RunFromStart] reads metadata → data when [RunOptions.ReadMetadataPseudoRow] is true.
// [RunBatch] and [ReadMetadataAndAdvanceToData] mirror multi-statement display.
//
// This package is an in-repo prototype for upstream [apstndb/spanvalue#178]; it may
// move or shrink once spanvalue/dbsqlrows matures.
package dbsqlrows
