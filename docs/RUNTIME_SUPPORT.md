# Runtime and module support

This matrix describes the selected, native Go runtime closures. It does not claim
full Ponder 0.18.2 compatibility. The fixed upstream is
`17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

| Module / mode | Verified selected behavior | Remaining scope |
| --- | --- | --- |
| Default multichain RuntimeRunner | Real RPC acquisition, handlers and PG/PGlite business transactions; selected bounded/continuous history, source initial positions, cache policies, reorg/reopen and PG application exits. Factory logs, transactions, account transfers and contract CALL function traces are covered by the v10–v13 selections. | Other source combinations and provider failure edges, complete upstream runtime/type mappings, full lifecycle/CLI acceptance. |
| Omnichain closing-block RuntimeRunner | v15 bounded ranges below the acquired global finalized watermark; cache enabled; exactly one interval1/offset0 closing block callback per chain covering all static contract ABI log ranges; startBlock >= 1. Actual RPC acquisition, ABI decoding, historical SQL paging and complete merged Groups transactions preserve same-block/overlap callback order and exact source failure/cold recovery on PG/PGlite. Historical.QuerySize 1..12000 (0 defaults12000). PG application exits are covered. | Live/pending/reorg, factory, mixed closing-block/log-only chains, account transaction/transfer/function trace sources, cache disabled, setup and start0 are rejected. Other ABI/provider/source combinations need broader omnichain coverage. PGlite abrupt exit and engine failures are not accepted. |
| Omnichain uniform log-only RuntimeRunner | v16 static contract ABI logs on every configured chain; finite identical callback ranges per chain with start>=1, cache enabled and global finality. Actual RPC acquisition, ABI callbacks, full Groups transactions, pagination, middle/trailing empty ranges, one/all empty chains and cold recovery preserve exact source checkpoints. Empty-chain Down(start1) remains distinct from proved scan completion and Ready. | Total configured physical range <=12000+6*N (each chain once, including empty chains); larger sparse spans, mixed closing/log-only chains, ABI-skipped nonempty tails, live/reorg, factory/account/trace, cachefalse, setup/start0, PGlite abrupt and engine crashes remain outside this closure. |
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

The v15 selected plan is **72 groups,559 unique qualified native leaves**
(473 pure,64 PostgreSQL,22 PGlite), plus **250 race executions**, for **809
case executions**. It selects528 v14 leaves again and adds31 native supplements
(17 capability,10 PG/PGlite source business,4 PG application-exit cases). The141
v14 leaves omitted on this snapshot are listed individually in
[v15 commands](../parity/runs/m45-omnichain-events-v15-20261103/commands.json).
These are changed selection counts, not an increased formal mapping or whole-project
completion. See [v15 validation](../parity/runs/m45-omnichain-events-v15-20261103/VALIDATION.md)
and [source contract](../parity/runs/m45-omnichain-events-v15-20261103/SOURCE-CONTRACT.md).

The v16 source/receipt boundary is specified in [source contract](../parity/runs/m45-omnichain-logonly-v16-20261103/SOURCE-CONTRACT.md). Its supplemental cases and race repetitions are separate from the formal 543/905 mapping. Final native commands and counts are recorded by the v16 validation report.
