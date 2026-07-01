# spannersh design philosophy

This tool is an **interactive shell for Google Cloud Spanner** and aims to stay readable as a **minimal example that delegates to go-sql-spanner**.

## Delegation boundaries

| Area | Policy |
|------|--------|
| Connection, DSN, dialect, transactions, routing SELECT/DML/DDL | **[go-sql-spanner](https://github.com/googleapis/go-sql-spanner)** (and the Spanner client behind it). No custom SQL engine or connection state machine here. |
| Execution path | Prefer a single **`database/sql` `QueryContext`**. The driver interprets `SELECT`, DML, DDL, and client-side statements. |
| Connection property names and meanings | **Upstream definitions** are canonical. The local docs describe driver properties first and DSN usage second; **mechanical extraction** of names is available via `tools/genprops` from `connection_properties.go`. |

## Responsibilities specific to this repository

- **REPL**: multiline input, submit on semicolon, `exit` / `quit`, history, Ctrl-C / Ctrl-J, etc.
- **`EXPLAIN` / `EXPLAIN ANALYZE`**: do not send these keywords to Spanner; **strip on the client** and use `ExecuteSqlRequest` `QueryMode` (`PLAN` / `PROFILE`) plus stats where applicable.
- **Rendering**: normal queries show a typed header and table, then an execution summary from **`ResultSetStats.query_stats`** when present. Row iteration over **`*sql.Rows`** is delegated to [spanvalue/dbsqlrows](https://github.com/apstndb/spanvalue/tree/main/dbsqlrows) (**`ReadMetadataAndAdvanceToData`**, **`RunRowsAtData`** + **`SQLRowsHooks`** for table/EXPLAIN, **`WriteRowsAtData`** for csv/jsonl). For **`--dialect postgresql`**, **`table`** mode uses [spantype](https://github.com/apstndb/spantype) for **PostgreSQL type strings** in the header and [spanvalue](https://github.com/apstndb/spanvalue) **`SimpleFormatConfig`** for cells; **`csv` / `jsonl`** use **`writer.NewCSVWriter` / `writer.NewJSONLWriter`** with **`DelimitedGCVExportOptions` / `JSONLGCVExportOptions`** (via **`WriteRowsAtData`**). **`csv`** cell text uses the same **Spanner CLI–compatible** formatter as **GoogleSQL `table`** cells (tuple **`STRUCT`** parentheses); **`jsonl`** uses **`JSONFormatConfig`** for machine-oriented round-trip. Cloned format presets call **`FormatConfig.Validate`** after customization. For **GoogleSQL** (default), the table uses [spantype](https://github.com/apstndb/spantype) + [spanvalue](https://github.com/apstndb/spanvalue) **`SpannerCLICompatibleFormatConfig`**, **`ColumnNames`** for headers (including **`_0`**-style unnamed columns), and **`FormatRowColumns`** for cells; **`columnNamesForRender`** falls back to raw field names when name resolution fails. **`EXPLAIN` (`PLAN`)** uses [spannerplan](https://github.com/apstndb/spannerplan) for the plan tree only (no row summary or stats line because `QueryStats` is usually absent). **`EXPLAIN ANALYZE` (`PROFILE`)** prints the plan tree, then **`N row(s) in set`**, then a **`query_stats` block only when returned** (do not infer stats from the plan when `query_stats` is missing).

## Intentionally out of scope

- Multi-layer **domain / use-case / repository** architectures.
- **Re-implementing** Spanner or go-sql-spanner behavior (retries, batches, partitioned DML, etc.—delegate to the driver).
- Relying **only** on hand-maintained connection property lists (use generation to reduce drift on upstream updates).

## Code organization

Split **`main`** across **several files** so **filenames hint at roles**. Growing many packages under `internal/` “for cleanliness” is not a priority for this project.

## Related documentation

- [README.md](../README.md) — usage and design entry
- [go-sql-spanner-dsn.md](./go-sql-spanner-dsn.md) — driver properties, DSN shape, and shared `SHOW` / `SET` names
- [generated/connection-properties.generated.md](./generated/connection-properties.generated.md) — connection property list (`go generate` / `tools/genprops`)
- [go-sql-spanner-client-side-statements.md](./go-sql-spanner-client-side-statements.md) — client-side statements
