# Draft: GitHub Issue (`googleapis/go-sql-spanner`)

Use this as the body when opening an issue (see [CONTRIBUTING.md](https://github.com/googleapis/go-sql-spanner/blob/main/CONTRIBUTING.md)). Adjust the **Related PR** / **Related issue** lines if numbers change. When referencing another repo’s PR or issue in prose, use **`owner/repo#number`** (not bare `#number`).

**Suggested title:** `database/sql: surface QueryStats in ResultSetStats (RowIterator already decodes them)`

---

## Problem

When using **go-sql-spanner** through `database/sql`, `ResultSetStats` from the Spanner API does not include **`query_stats`**, even though the underlying `spanner.RowIterator` already decodes **`Stats.QueryStats`** from the **last `PartialResultSet`** in the stream (as a `map` on `QueryStats`).

Upstream `createResultSetStats` in `transaction.go` had a **TODO** noting that only **`RowCount`** and **`QueryPlan`** were being forwarded, not the full stats surface.

## Impact (downstream tool)

We are building **[spannersh](https://github.com/apstndb/spannersh)** — a small interactive Spanner shell that prints execution summaries (e.g. `elapsed_time`, optimizer metadata) from **`ResultSetStats.query_stats`**, similar to other CLIs. Without `query_stats` on the `database/sql` path, that summary block stays empty even though the native client stream already carried the data.

## Ask

Forward **`RowIterator.QueryStats`** into **`sppb.ResultSetStats.QueryStats`** when building stats for the driver path, so `database/sql` consumers see the same aggregates as code using the Spanner client directly.

**Related PR:** [googleapis/go-sql-spanner#778](https://github.com/googleapis/go-sql-spanner/pull/778) (proposed fix).

---

## Tracking (this repository)

The issue opened from this draft: **[googleapis/go-sql-spanner#779](https://github.com/googleapis/go-sql-spanner/issues/779)**. Use **`owner/repo#number`** when pointing at it from other repos (for example **apstndb/spannersh**).
