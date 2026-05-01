# go-sql-spanner client-side statements reference

[go-sql-spanner](https://github.com/googleapis/go-sql-spanner) supports **client-side statements**: SQL-like text interpreted and executed **in the driver** without sending it to Spanner as ordinary SQL. **Upstream source is always canonical.**

| Reference | URL |
|-----------|-----|
| Module (version, API) | [pkg.go.dev/github.com/googleapis/go-sql-spanner](https://pkg.go.dev/github.com/googleapis/go-sql-spanner) |
| Changelog | [CHANGES.md](https://github.com/googleapis/go-sql-spanner/blob/main/CHANGES.md) |
| Parser (keywords, syntax) | `parser/statement_parser.go`: `clientSideKeywords`, `ParseClientSideStatement`, and `parser/statements.go` |
| Execution (Exec / Query behavior) | Package `statements.go`: `createExecutableStatement` and each `executable*` |

This repo’s `go.mod` assumes **`github.com/googleapis/go-sql-spanner v1.25.1`**. After upgrading, diff behavior in **upstream**.

---

## Connection property names (shared with DSN)

**Property names in `SHOW` / `SET` / `RESET`** are the same identifiers as DSN **`name`** in `name=value`. The full list is in **[generated/connection-properties.generated.md](./generated/connection-properties.generated.md)** (generated), the grouped summary and **alphabetical** table in **[go-sql-spanner-dsn.md](./go-sql-spanner-dsn.md#all-properties-alphabetical)**.

Only **per-statement syntax** (e.g. GoogleSQL `SHOW VARIABLE read_only_staleness`) is specific to client-side statements.

---

## Detecting “client-side” statements

- After skipping hints, the **first keyword** of the statement must be one of: `SHOW`, `SET`, `RESET`, `START`, `RUN`, `ABORT`, `BEGIN`, `COMMIT`, `ROLLBACK` (`clientSideKeywords` in `parser/statement_parser.go`).
- If it matches and the parser accepts it, `DetectStatementType` returns **`StatementTypeClientSide`**.
- Statements starting with **`CREATE` / `DROP`** are classified as **DDL**, not this list. `CREATE DATABASE` / `DROP DATABASE` go through **Admin API DDL** (`execDDL` in `conn.go`, etc.). Parsed types like `ParsedCreateDatabaseStatement` exist, but the normal path is DDL.

---

## Syntax overview (dialect differences)

### `SHOW`

- Form: `SHOW [VARIABLE] [extension.]property`
- **GoogleSQL**: **`VARIABLE` is required** (error if omitted).
- **PostgreSQL**: **`VARIABLE` is omitted** (parser advances to the property name).

`property` is the same identifier as in the DSN docs (**`snake_case`** recommended; implementations may accept names without underscores).

### `SET`

- Form: `SET [SESSION | LOCAL] [extension.]property = <literal>` (GoogleSQL uses `=` only).
- **PostgreSQL** also allows `SET property TO <literal>`. `SESSION` may be ignored.
- **`property`** uses the same namespace as DSN.
- **`SET TRANSACTION ...`**: limited support before starting a transaction (isolation, `READ ONLY` / `READ WRITE`; PostgreSQL may include `[NOT] DEFERRABLE`). Parser normalizes identifiers/literals internally.

### `RESET`

- Form: `RESET [extension.]property` (same identifiers as DSN / `SHOW`).
- Implementation resets values toward `"default"` (`executableResetStatement` in `statements.go`).

### Batching

| Statement | Meaning |
|-----------|---------|
| `START BATCH DDL` | Start DDL batch |
| `START BATCH DML` | Start DML batch |
| `RUN BATCH` | Run pending batch |
| `ABORT BATCH` | Abort batch |

### `RUN PARTITIONED QUERY`

- Form: after `RUN PARTITIONED QUERY`, the **rest of the text** is treated as normal SQL (subquery-like).
- Execution sets the partitioned-query path and runs the inner SQL via `QueryContext`.

### Transactions

| Statement | Notes |
|-----------|-------|
| `BEGIN` … | GoogleSQL; optional `TRANSACTION` and transaction options. |
| `START` / `START TRANSACTION` / `START WORK` | In **PostgreSQL** dialect, parsed like `BEGIN` (`parseStatement` branch). |
| `COMMIT` [ `TRANSACTION` \| `WORK` ] | Dialect-specific optional trailing keywords. |
| `ROLLBACK` [ `TRANSACTION` \| `WORK` ] | Same as above. |

---

## Using `database/sql`: `QueryContext` vs `ExecContext`

The driver allows `queryContext` / `execContext` per statement kind (`statements.go`).

| Kind | `ExecContext` | `QueryContext` |
|------|---------------|----------------|
| `SHOW ...` | **Not allowed** (error) | **Allowed** (single-column result set) |
| `SET ...` / `RESET ...` | Allowed | Allowed (may be empty result set) |
| `START BATCH` / `RUN BATCH` / `ABORT BATCH` | Allowed | Allowed |
| `RUN PARTITIONED QUERY ...` | **Not allowed** | **Allowed** |
| `BEGIN` / `COMMIT` / `ROLLBACK` | Allowed | Allowed (may be empty, etc.) |

**spannersh** runs statements via **`QueryContext`** as documented in the README, so `SHOW` and `RUN PARTITIONED QUERY` work as-is. Generic Go code that only uses `db.Exec` must use **`Query`** for `SHOW`.

---

## Related documentation

- **Connection property list** (DSN keys and `SHOW`/`SET` names): [generated/connection-properties.generated.md](./generated/connection-properties.generated.md) · [go-sql-spanner-dsn.md](./go-sql-spanner-dsn.md) (summary)
