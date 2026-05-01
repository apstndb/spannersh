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
- **Rendering**: normal queries show a typed header and table, then an execution summary from **`ResultSetStats.query_stats`** when present. For **`--dialect postgresql`**, **`table`** mode uses [spanpg](https://github.com/apstndb/spanpg) for **PostgreSQL type strings** in the header and **`FormatColumnSimple`** for cells; **`csv` / `jsonl`** use [spanvalue](https://github.com/apstndb/spanvalue) **`writer.NewCSVWriter` / `writer.NewJSONLWriter`** (v0.3.0+; unnamed-field naming and JSON assembly validation live in spanvalue). For **GoogleSQL** (default), the table uses [spantype](https://github.com/apstndb/spantype) + [spanvalue](https://github.com/apstndb/spanvalue) **`SpannerCLICompatibleFormatConfig`** as before. **`EXPLAIN` (`PLAN`)** uses [spannerplan](https://github.com/apstndb/spannerplan) for the plan tree only (no row summary or stats line because `QueryStats` is usually absent). **`EXPLAIN ANALYZE` (`PROFILE`)** prints the plan tree, then **`N row(s) in set`**, then a **`query_stats` block only when returned** (do not infer stats from the plan when `query_stats` is missing).

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
