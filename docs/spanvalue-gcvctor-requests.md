# Notes: requests for spanvalue / gcvctor

This memo summarizes **library-side wishes** from experience using [go-sql-spanner](https://github.com/googleapis/go-sql-spanner) + `database/sql` + **`DecodeOptionProto`** in this repo ([spannersh](https://github.com/apstndb/spannersh)). Prioritization and adoption are up to upstream.

**Tracking / implementation:** [apstndb/spanvalue#13](https://github.com/apstndb/spanvalue/issues/13) landed in [v0.1.10](https://github.com/apstndb/spanvalue/releases/tag/v0.1.10) as **`FormatRowColumns` / `FormatRowJSONObjectFromColumns` / `ColumnNames`**. This repo depends on **`spanvalue v0.2.0+`** and uses these for **CSV / JSONL** output (`render.go`; **`ColumnNames` returns an error** on invalid / colliding generated names). v0.1.10 also consolidates default **`FormatConfig`** behind **constructor functions** (e.g. `SpannerCLICompatibleFormatConfig()`).

---

## Background

- Query results are often read from `*sql.Rows` with columns as **`[]spanner.GenericColumnValue`** (GCV).
- Existing **`spanvalue.FormatRowJSONObject`** targets `*spanner.Row`; a thin row-level API for **GCV + metadata only** forces callers to loop `FormatToplevelColumn` per column.
- **Rendering (GCV → string)**, **construction (value → GCV)**, and **parsing (string → GCV)** are different responsibilities.

---

## Requests for spanvalue

### 1. Row-level API for `database/sql` / GCV (JSON)

**Goal:** A counterpart to `FormatRowJSONObject` for **rows of GCVs**.

**Upstream approach ([apstndb/spanvalue#13](https://github.com/apstndb/spanvalue/issues/13)):** Extract column names to `[]string`, then build cell rows and JSON rows.

**Preference:** **`ColumnNames` + `FormatRowJSONObjectFromColumns`** shares more with CSV (`FormatRowColumns`) than a single **`FormatRowJSONObjectFromGCV(fields, values, namer)`** from the first draft of this memo.

**Assumptions**

- `len(columnNames) != len(values)` (and the `FormatRowColumns` side) returns **`error`**.
- `fc` is expected to use **`JSONFormatConfig()`**.
- `namer` and empty keys stay consistent with existing `FormatRowJSONObject` per the issue.

**Optional extensions (outside the issue)**

- A variant that writes to `io.Writer` (JSONL)—not required.

### 2. Thin helper flattening a row to `[]string` (CSV)

**Goal:** Keep CSV in **`encoding/csv`**; spanvalue stops at **enumerating cell strings**.

**Upstream approach ([apstndb/spanvalue#13](https://github.com/apstndb/spanvalue/issues/13)):** `FormatRowColumns` returns the cell row; add the header with `ColumnNames` + `csv.Writer.Write`.

The first-draft name `FormatRowCells` is superseded by **`FormatRowColumns(fc, columnNames, values)`**.

**Notes**

- Loading **JSON values** into cells often works with existing **`JSONFormatConfig` + `FormatToplevelColumn`** (this repo’s `--format csv` uses that pattern).
- **CSV-specific policies** (unquoted numbers, empty NULL cells, etc.) should be nailed down before splitting APIs; the generic helper above is enough to start.

### 3. Documentation (recommended patterns)

**Goal:** A short page with **JSONL / CSV recipes** (`JSONFormatConfig` + `encoding/csv`) for readers using go-sql-spanner + proto decode + GCV would reduce perceived “missing” APIs.

### 4. Clarify non-goals (reverse direction)

**String → `GenericColumnValue`** cannot live in display `FormatConfig` / `FormatComplexPlugins` alone (**parsing + type information** required).

- **Construction** belongs in **gcvctor** (below).
- **Round-tripping CSV/JSON** is a separate layer (dedicated parser or app code).

---

## Requests for gcvctor

### 1. Role

- **`spanvalue`**: GCV → display strings (and row-level JSON assembly above).
- **`gcvctor`** (or equivalent): **build GCVs** to expected types (fixtures, binding, parts of ETL).

### 2. Nature of the ask

From this repo we expect **clear module boundaries**: **GCV construction in gcvctor**, **spanvalue focused on formatting and symmetric row JSON APIs**. Concrete APIs follow gcvctor’s design.

---

## Reference (usage in this repo)

- Table output: `SpannerCLICompatibleFormatConfig` + `FormatToplevelColumn`
- `--format csv` / `jsonl`: `JSONFormatConfig` + `FormatToplevelColumn`; column names via `columnNameForField` (empty names aligned with `spanvalue.IndexedUnnamedFieldNamer`)

---

## Revision history

- 2026-03-28: Initial version (conversation notes)
