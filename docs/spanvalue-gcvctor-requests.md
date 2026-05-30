# Notes: spanvalue / gcvctor feedback (spannersh)

This memo records **upstream feedback** from [spannersh](https://github.com/apstndb/spannersh) using [go-sql-spanner](https://github.com/googleapis/go-sql-spanner) + `database/sql` + **`DecodeOptionProto`**. Prioritization is up to spanvalue / gcvctor.

**Adopted in this repo (spanvalue v0.4.2):** [apstndb/spanvalue#13](https://github.com/apstndb/spanvalue/issues/13) (`ColumnNames`, `FormatRowColumns`, `FormatRowJSONObjectFromColumns`), **`writer.NewCSVWriter` / `NewJSONLWriter`** with **`WithMetadata`**, **`WithFormatter`**, **`WithUnnamedFieldNamer`**, and **`Flush`** for header-only zero-row CSV. See `render.go`.

**Explicit non-goal for spanvalue:** **`database/sql` `*sql.Rows` support** (no request to add `sql.Rows` iterators or drivers into spanvalue). spannersh keeps a thin **`scanValues` → `[]GenericColumnValue` → `WriteGCVs`** loop; that boundary is intentional.

---

## Still useful upstream (spanvalue)

### 1. Documentation: go-sql-spanner + GCV recipes

A short doc page (or README section) with end-to-end patterns:

- Proto decode + `ResultSetMetadata` → column names via **`ColumnNames(fields, IndexedUnnamedFieldNamer)`** (same namer as writers).
- **Table / CLI text:** `SpannerCLICompatibleFormatConfig()` + **`FormatRowColumns`** (or per-cell `FormatToplevelColumn`).
- **CSV / JSONL:** `writer.NewCSVWriter` / `NewJSONLWriter` with shared options; **`WriteGCVs`**; call **`Flush`** after iteration so **zero-row** SELECT still emits a **CSV header** (v0.4+); note JSONL **`Flush` is a no-op**.

Reduces duplicate “missing API” questions now that writers exist.

### 2. Shared export-writer option helper

`DelimitedOption` and `JSONLOption` are separate interfaces; callers often repeat the same triple (`WithMetadata`, `WithFormatter`, `WithUnnamedFieldNamer`). A small helper (e.g. options applied to both writer kinds, or a struct copied into each constructor) would avoid duplicated setup in apps like spannersh (`spanvalueDelimitedWriterOptions` / `spanvalueJSONLWriterOptions` in `render.go`).

### 3. Clarify metadata naming vs `ColumnNames`

`WithMetadata` builds internal names from raw `StructType.Field` names; **`resolvedNames()`** applies `UnnamedFieldNamer` on write. Table headers printed outside the writer should use **`ColumnNames`** with the **same** namer—worth stating in writer package docs to prevent `_0` mismatches between header and CSV/JSONL.

### 4. Non-goals (unchanged)

- **String → `GenericColumnValue`** parsing does not belong in display `FormatConfig`; use **gcvctor** (or app-specific parsers) for construction.
- **`*spanner.Row` / `RowIterator` helpers** in spanvalue are fine for native client code; **go-sql-spanner consumers stay on GCV slices** (see non-goal above).

---

## gcvctor (unchanged)

- **`spanvalue`:** GCV → display strings and export writers.
- **`gcvctor`:** build GCVs for fixtures, binding, ETL. spannersh does not re-export construction APIs.

---

## Reference (current spannersh usage)

| Output | Mechanism |
|--------|-----------|
| GoogleSQL **table** | `ColumnNames` + `FormatRowColumns` + `SpannerCLICompatibleFormatConfig` (tuple STRUCT); types via **spantype** |
| PostgreSQL **table** | **spanpg** header types + `FormatColumnSimple` (not spanvalue cells) |
| **csv** / **jsonl** | `writer` + `JSONFormatConfig` + `WriteGCVs` + `finishWriterFlush` |

Row iteration: `forEachResultRow` → `scanValues` → callback (no spanvalue `sql.Rows` API).

---

## Revision history

- 2026-03-28: Initial version (conversation notes)
- 2026-05-27: Mark #13 / v0.4 writer adoption; add post-adoption feedback; document no `sql.Rows` in spanvalue
