# spannersh — agent context

This file collects assumptions and practices for working on **spannersh** (a `package main` interactive Spanner shell) in code and reviews. Use it together with your global rules (e.g. home `AGENTS.md`).

**Cursor:** `.cursor/rules/spannersh.mdc` points here as the source of truth and scopes via `globs` (Cursor-only notes stay in the `.mdc` file).

## Language (repository files)

All material **saved in this repository**—source code, comments, tests, Markdown under `docs/`, `README.md`, this file, generated documentation, and similar—**must be written in English.** User-visible CLI strings and errors should be English unless an external requirement says otherwise.

## Project goals

- Stay a **simple client** that delegates to **go-sql-spanner + database/sql**.
- Prefer **`package main` split by filename** over many packages “for show” (see `doc.go` package comment).

## Layout and types

- **`app`** — holds `ctx`, `out`, `db`, `format`, `dialect`; drives `executeAndRender` and friends via `*app` methods.
- **`preparedQuery`** — result of `prepareQuery` (SQL to run / `QueryMode` / display kind). Prefer this named struct over a bare triple.
- **`queryHead`** — `*sql.Rows` and `ResultSetMetadata` after consuming the metadata result set.**Caller closes `rows`.** Document the **metadata → data rows → stats** order assumed by `buildExecOptions`. For multiple statements, **`displayResults` advances with `readMetadataAndAdvanceToData`** to the next metadata; repeat with a **blank line** between blocks.

## Spanner / EXPLAIN display rules

- **Multiple statements and batching** — split with **`Split`** from **`github.com/googleapis/go-sql-spanner/parser`** (switch GoogleSQL / PostgreSQL parsers via **`--dialect`**; for PostgreSQL the DSN gets **`dialect=POSTGRESQL`** by default unless **`--dsn-suffix` already sets `dialect=`**). **Consecutive statements with the same `QueryMode`** are joined with **`"; "`** into **one `QueryContext`** (batch implicit transactions). **Batch boundaries** occur when **`EXPLAIN` (PLAN)** alternates with **everything else (PROFILE = normal `SELECT` / `EXPLAIN ANALYZE`)**. Per-statement display uses **`stmtDisplayKind`** (plan only / PROFILE plan / normal table); `displayResults` inserts **blank lines** between blocks.
- **`EXPLAIN` (PLAN)** — usually **no `QueryStats`**. Plan table only; no `N row(s) in set` and no stats-derived block.
- **`EXPLAIN ANALYZE` (PROFILE)** — **plan table → row summary → `query_stats` (only when the API returns it)**. If there is no `query_stats`, **do not synthesize stats from the plan**.
- **`QueryPlan` (protobuf / go doc)** — `plan_nodes` is pre-order from the root. Assume each **`PlanNode.Index` matches its slice position** so `plan_nodes[i]` is valid (out of range → nil).

## Protobuf generated code and nil receivers

- **`Get*` on `cloud.google.com/go/spanner/apiv1/spannerpb`** does **not panic** on a `nil` receiver; it returns zero values / nil messages (normal for generated code). For `GetPlanNodes()`, `GetExecutionStats()`, `GetQueryPlan()` / `GetRowCountExact()`, etc., you can often **drop redundant `if x != nil`**.
- **`AsMap()` on `google.golang.org/protobuf/types/known/structpb.Struct`** uses `GetFields()` internally and returns an empty `map[string]any` even when `*Struct` is nil, so around `writeQueryStatsLines(out, qs.AsMap())` you may **reduce checks other than `qs == nil`** (keep **`GetQueryStats() != nil`** when branching on presence of `query_stats`).
- **Do not remove / cases where behavior changes**
  - When **user-facing text** must stay fixed (e.g. early return with a different message when all of `ResultSetStats` is `nil`).
  - When you must distinguish **“field unset”** vs **empty struct** for `query_stats` (nothing vs `(empty)`).

## go-sql-spanner / driver

- Interactive path: request **`DecodeOptionProto`, metadata, stats** via `ExecOptions`. Warm-up / **`--dialect auto`**: read **`database_dialect`** from the driver via client-side **`SHOW`** (see `queryDriverDatabaseDialect`), without `ExecOptions`. The connector already ran **`INFORMATION_SCHEMA`** once during connect; the shell does not duplicate that query. **`--dialect auto`**: only synchronous **`detectDatabaseDialect`** (no duplicate background warm-up). **Explicit dialect**: background warm-up only.
- **`multiline.Editor`** contains a mutex — **do not copy by value** (`go vet` copylocks). Configure on `*multiline.Editor`.
- **REPL submit** (`replInputComplete`) — join and trim the **whole buffer**, not a single line; submit on Enter when the buffer **ends with `;`** or is **only `exit` / `quit`**. Multiple statements follow [go-sql-spanner v1.22.0](https://github.com/googleapis/go-sql-spanner/releases/tag/v1.22.0) **multi-sql / `NextResultSet`**.

## Standard library (`maps` / `slices` / `cmp`)

- **`maps.Keys(m)`** order is **unspecified**. For stable display order use something like **`slices.Sorted(maps.Keys(m))`**.
- **`slices.MaxFunc`** **panics on an empty slice** — check length first.
- Match the **`go` version in `go.mod`**. Prefer **`t.Context()`** in tests when you need a `Context` (Go 1.24+).

## When “reducing line count”

- Do **not** add helpers like **`samber/lo`** only to save lines; dependencies and reader cost usually outweigh gains here.
- Short `if` declarations, **`switch` on kind / prompt stage**, **`for` + small tables** for duplication are fine. Avoid **wrapping whole functions in huge `else`**.

## Tests

- Default: `go test ./...`
- Emulator: `go test -tags=integration ./...` (`integration_test.go` has `//go:build integration`).

## Documentation

- When behavior or design changes affect user-facing docs, update **`README.md` / `docs/design-philosophy.md`** (when the user asks or when required for the change).
- When referencing a **pull request or issue in another repository**, write **`owner/repo#number`** (for example `googleapis/go-sql-spanner#778`), optionally as a Markdown link—**not** a bare `#number`, which is ambiguous on GitHub.

### Index (details in each file)

| Topic | Path |
|------|------|
| Design intent and boundaries | `docs/design-philosophy.md` |
| DSN and connection properties (summary + alphabetical table) | `docs/go-sql-spanner-dsn.md` |
| Connection property list (generated) | `docs/generated/connection-properties.generated.md` (`go generate ./...`) |
| Client-side statements | `docs/go-sql-spanner-client-side-statements.md` |
| Usage entry point | `README.md` |

Do **not** re-implement go-sql-spanner behavior in this repo. The main tool-specific logic is **stripping `EXPLAIN`** and **rendering results**.

## Out of scope (by default)

- **Driver fixes** — belong in **[googleapis/go-sql-spanner](https://github.com/googleapis/go-sql-spanner)** upstream (this repo uses the module from the proxy, **no `replace`** for the driver).
- Avoid **drive-by refactors** and **unrelated new Markdown** outside the request.
