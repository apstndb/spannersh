# spannersh

[![CI](https://github.com/apstndb/spannersh/actions/workflows/ci.yml/badge.svg)](https://github.com/apstndb/spannersh/actions/workflows/ci.yml)

Yet another interactive tool for [Google Cloud Spanner](https://cloud.google.com/spanner), following [cloudspannerecosystem/spanner-cli](https://github.com/cloudspannerecosystem/spanner-cli) (the basis of the current official Spanner CLI) and [apstndb/spanner-mycli](https://github.com/apstndb/spanner-mycli): run SQL from the terminal, print result tables (with column types), and show the query plan.

It stays intentionally simple by reusing existing Go packages for the driver, REPL, plan rendering, and value formatting instead of re-implementing that machinery in this repository.

## Prerequisites

- Go 1.26.2+
- Google Cloud credentials with access to the target instance (for example Application Default Credentials from `gcloud auth application-default login`).

The [go-sql-spanner](https://github.com/googleapis/go-sql-spanner) driver detects whether the database uses **GoogleSQL** or **PostgreSQL** dialect after connect unless you override this via DSN parameters (see **`--dsn-suffix`** and the driver documentation). The shell’s **`--dialect`** aligns **client-side statement splitting** with that choice; use **`--dialect postgresql`** when working against a PostgreSQL-dialect database so semicolons inside PostgreSQL-specific literals are handled like the driver’s parser.

## Install

```bash
go install github.com/apstndb/spannersh@latest
```

For `go install module@version`, `--version` prints the module version embedded in the binary ([`runtime/debug.ReadBuildInfo`](https://pkg.go.dev/runtime/debug#ReadBuildInfo) `Main.Version` — the tag you asked for, or a pseudo-version for commits without a tag). Local `go build` / `go install` from a checkout usually reports `dev` because `Main.Version` is `(devel)` unless you pass `-ldflags` (see below).

If you clone this repository, build from the module root:

```bash
go build -o spannersh .
```

To force an explicit version string (for example from `git describe` in CI or release builds), override the link-time default:

```bash
go build -o spannersh -ldflags "-X main.version=$(git describe --tags --always --dirty)"
```

## Usage

```text
spannersh -p PROJECT -i INSTANCE -d DATABASE [--dialect DIALECT] [--dsn-suffix PARAMS] [--format FORMAT]
```

### `spannersh --help`

<!-- spannersh-help begin -->
```text
Usage: spannersh [flags]

Interactive shell for Google Cloud Spanner.

Flags:
  -h, --help                 Show context-sensitive help.
      --version              Print version and exit.
  -p, --project=PROJECT      Google Cloud Project ID. Default: SPANNER_PROJECT_ID.
  -i, --instance=INSTANCE    Spanner instance ID. Default: SPANNER_INSTANCE_ID.
  -d, --database=DATABASE    Database ID. Default: SPANNER_DATABASE_ID.
      --dsn-suffix=PARAMS    Extra go-sql-spanner DSN parameters (snake_case; semicolon-separated). See docs.
      --dialect=DIALECT      Client-side SQL parser dialect: auto, google-standard-sql, or postgresql. PostgreSQL adds dialect=POSTGRESQL to the DSN unless --dsn-suffix already sets dialect=.
      --format=FORMAT        Output format: table, csv, or jsonl. EXPLAIN plan output is always a text plan tree.
```
<!-- spannersh-help end -->

| Flag | Meaning |
|------|---------|
| `--version` | Print version and exit. |
| `-p`, `--project` | GCP project ID (required unless set). Default: env `SPANNER_PROJECT_ID`. |
| `-i`, `--instance` | Spanner instance ID (required unless set). Default: env `SPANNER_INSTANCE_ID`. |
| `-d`, `--database` | Database ID (required unless set). Default: env `SPANNER_DATABASE_ID`. |
| `--dialect` | SQL dialect for client-side parsing: `auto` (default), `google-standard-sql`, or `postgresql`. |
| `--dsn-suffix` | Extra [go-sql-spanner](https://pkg.go.dev/github.com/googleapis/go-sql-spanner) DSN parameters as `;`-separated `name=value` pairs. See **[docs/go-sql-spanner-dsn.md](docs/go-sql-spanner-dsn.md)** and **[docs/go-sql-spanner-client-side-statements.md](docs/go-sql-spanner-client-side-statements.md)**. |
| `--format` | Result output format: `table` (default), `csv`, or `jsonl`. |

End each statement with a **semicolon** (`;`) and press Enter to run it. Multiple lines are supported until the line ending is `;`.

**Leave the shell:** type **`exit`** or **`quit`** (case-insensitive, optional trailing `;`) and press Enter—they submit even without a semicolon. **Ctrl-D** on an **empty** first line sends EOF and exits cleanly (as in [go-multiline-ny](https://github.com/hymkor/go-multiline-ny) examples). **Ctrl-J** inserts a newline without submitting (same idea as Enter when the line does not end with `;`).

Press **Ctrl+C** to cancel an in-flight query or interrupt the session (see context cancellation below).

## Behavior notes

- After connect, the shell **warms the session** by reading the driver’s **`database_dialect`** property (client-side **`SHOW`**, no PROFILE `ExecOptions`), so the first interactive query is less likely to pay full cold-start cost. With **`--dialect auto`**, that read runs **once** as part of dialect detection; with an **explicit** `--dialect`, it runs **in the background** while the REPL starts. Errors are printed to **stderr** and the REPL still runs.
- Statements are executed through `database/sql` **QueryContext**. The driver routes **SELECT**, **DML**, and **DDL** appropriately (including long-running DDL on the admin API). **Multiple statements in one input** (semicolon-separated, as supported by [go-sql-spanner](https://github.com/googleapis/go-sql-spanner) from v1.22+) are printed **one result block per statement**—same table / **`csv`** / **`jsonl`** shape and execution summary as a single query—with a **blank line** between blocks.
- Normal queries use **`ExecuteSqlRequest_PROFILE`** with **`ReturnResultSetStats` enabled**. After the result table (or **`csv`** / **`jsonl`** output), the shell prints an **execution summary** from Spanner’s **`ResultSetStats.query_stats`** when it is present (e.g. `elapsed_time`, `read_timestamp`, `cpu_time`, `rows_scanned`, optimizer metadata)—similar to spanner-mycli’s block after **`EXPLAIN ANALYZE`**. The pinned **`go-sql-spanner`** release includes **[googleapis/go-sql-spanner#778](https://github.com/googleapis/go-sql-spanner/pull/778)** so **`QueryStats`** is forwarded into **`ResultSetStats`** (see also **[googleapis/go-sql-spanner#779](https://github.com/googleapis/go-sql-spanner/issues/779)**). The query plan tree is **not** printed unless you use **`EXPLAIN`** / **`EXPLAIN ANALYZE`**.
- **`EXPLAIN`** / **`EXPLAIN ANALYZE`** are not sent to Spanner as SQL keywords. The shell strips them client-side and runs the inner statement with **`QueryMode = PLAN`** or **`PROFILE`**. **`EXPLAIN`**: **plan tree** only (via [spannerplan](https://github.com/apstndb/spannerplan)) when plan nodes exist—no **`N row(s) in set`** line and no stats block, since PLAN typically does not populate **`QueryStats`**. **`EXPLAIN ANALYZE`**: plan tree first, then **`N row(s) in set`** (and **`elapsed_time` in parentheses when `query_stats` includes it**), then a **`query_stats`** key/value block **only when `query_stats` is returned**—the shell does not reconstruct stats from the plan tree. Plan output ignores **`--format`**. For **multi-statement** input, the shell **batches** consecutive statements that share the same **`QueryMode`** into **one** `QueryContext` (so **normal `SELECT` and `EXPLAIN ANALYZE`**, both **PROFILE**, can run together); **`EXPLAIN` (PLAN)** starts a **separate** batch from **PROFILE**. Statements are split with [go-sql-spanner’s `parser.Split`](https://pkg.go.dev/github.com/googleapis/go-sql-spanner/parser#StatementParser.Split); **per-statement display** (plan vs table) still follows each statement’s role.

## More documentation

| Document | Contents |
|----------|----------|
| [docs/go-sql-spanner-dsn.md](docs/go-sql-spanner-dsn.md) | DSN shape and connection properties |
| [docs/go-sql-spanner-client-side-statements.md](docs/go-sql-spanner-client-side-statements.md) | Client-side statements (`SHOW` / `SET`, etc.) |
| [docs/generated/connection-properties.generated.md](docs/generated/connection-properties.generated.md) | Generated connection-property table from upstream `go-sql-spanner` |

## Development

Processing is delegated as much as possible to **[go-sql-spanner](https://github.com/googleapis/go-sql-spanner)** and `database/sql`; there is no layered architecture beyond that. Work specific to this repo is the **REPL**, **client-side `EXPLAIN` / `EXPLAIN ANALYZE` rewriting**, and **result / plan rendering**. See **[docs/design-philosophy.md](docs/design-philosophy.md)** for intent and boundaries.

Run the default tests with:

```bash
go test ./...
```

Integration tests require Docker and start the Spanner emulator via [spanemuboost](https://github.com/apstndb/spanemuboost):

```bash
go test -tags=integration ./...
```

Generated documentation is refreshed from the module root with:

```bash
go generate ./...
```

`go generate` runs [`ptyhelp patch`](https://github.com/apstndb/ptyhelp) through **mise** using the project-level [`mise.toml`](mise.toml). The generator runs `go run . --help` in a PTY, writes `docs/generated/spannersh-help.txt`, and replaces the `<!-- spannersh-help begin/end -->` block in this file. It also regenerates [docs/generated/connection-properties.generated.md](docs/generated/connection-properties.generated.md) after `go-sql-spanner` updates.

CI runs formatting, vet, build, version, unit, race, vulnerability, and integration checks on pushes and pull requests. Releases are created by pushing a `v*` tag; the GoReleaser workflow builds archives and checksums for GitHub Releases.

## License

**spannersh** is released under the [MIT License](LICENSE).

The [go-sql-spanner](https://github.com/googleapis/go-sql-spanner) driver is **Apache License 2.0**; see the [upstream LICENSE](https://github.com/googleapis/go-sql-spanner/blob/main/LICENSE).
