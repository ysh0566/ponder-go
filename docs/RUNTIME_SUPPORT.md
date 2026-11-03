# Runtime and module support

This matrix describes the selected, native Go runtime closures. It does not claim
full Ponder 0.18.2 compatibility. The fixed upstream is
`17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

| Module / mode | Verified selected behavior | Remaining scope |
| --- | --- | --- |
| Default multichain RuntimeRunner | Real RPC acquisition, handlers and PG/PGlite business transactions; selected bounded/continuous history, source initial positions, cache policies, reorg/reopen and PG application exits. Factory logs, transactions, account transfers and contract CALL function traces are covered by the v10–v13 selections. | Other source combinations and provider failure edges, complete upstream runtime/type mappings, full lifecycle/CLI acceptance. |
| Omnichain RuntimeRunner | v14 bounded ranges entirely below the acquired global finalized watermark; cache enabled; exactly one interval1/offset0 block callback per chain; startBlock >= 1. Actual historical coordinator SQL paging and complete merged Groups form business transactions. Source Down and partial block-event checkpoints, handler failure/retry, different starts/ends and cold reopen are compared with actual upstream execution on PG/PGlite. PG abrupt application exits are covered. | Live/pending/reorg execution, factory/non-block sources, cache disabled, setup and start0 are rejected. PGlite abrupt exit and engine failures are not accepted. |
| Isolated ordering | Existing storage predicates and selected primitives. | Actual isolated RuntimeRunner scheduling. |
| Schema / business DB | Native schema, journal, scoped ORM/raw SQL and selected database lifecycle operations on PostgreSQL and PGlite. | Entire upstream schema/type surface, existing database takeover and all role/permission layouts. |
| ABI / events | Selected logs, account callbacks and dynamic contract CALL ABI decoding, including overloaded and dynamic tuple/array results. | Full compile-time function handler bindings and all callback overlap/selector combinations. |
| RPC / sync | Source-shaped actions, cache data, historical/realtime helpers and the selected actual RuntimeRunner pipelines. | Complete provider behavior, all archive/prefix failures and complete original runtime oracle coverage. |
| Recovery | Selected native durable receipts, exact retries, safe progress and application process exits. | Database server/engine/power-loss failures; M45 PGlite abrupt process-exit acceptance. |
| CLI / development modes | Existing implemented commands and configuration compilation. | Complete CLI start/dev/db lifecycle and end-to-end acceptance. |

Formal source mapping remains **543/905**. Of the static source records, 362
remain unimplemented; 7 runtime-passed/type-pending records are not counted as
completed mappings. Original skip/todo states remain unchanged. Native supplement
counts, race repetitions and source probe observations do not increase this map.

The v14 selected plan is **132 groups, 669 unique qualified native leaves**
(456 pure, 144 PostgreSQL, 69 PGlite), plus **276 race executions**, for **945
case executions**. It reruns the entire v13 selection of 612 unique leaves on the
new input. Its 57 additional qualified leaves comprise 36 omnichain/receipt
supplements and 21 affected legacy store controls. These are selection counts,
not whole-project completion. See
[validation](../parity/runs/m45-omnichain-v14-20261103/VALIDATION.md).
Full repository/full-race execution and complete M45 acceptance remain open.
