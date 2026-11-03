# v16: bounded finalized log-only omnichain

Base b92979aed1ed5f396be3365a905e4414d2b1acb0; fixed Ponder0.18.2 source17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334. Original v15 source/evidence and failures remain read-only.

Selected closure: every configured chain has static ABI log callbacks, no block callbacks, cache=true, finite identical per-chain callback ranges with start>=1, globally finalized and no pending/live events. Sparse middle/trailing ranges, a configured chain with zero logs, all configured chains with zero logs, SQL pagination, complete Groups transactions, handler failure, cold reopen, and PG/PGlite are required. Existing block+log mode remains v11; mixed closing/log-only chains, factory/account/trace, setup/start0/cachefalse/live/reorg are outside this stage.

Actual source observations01..09, independently asserted with archive/content/mode binding (see source-oracle.json):
- Two overlapping ABI log callbacks,32 events across two chains and blocks1/3; no callback at block2. Both query6 and12000 complete and cold replay0.
- One fully empty chain keeps Down(start1) in all three PONDER checkpoints while the application is Ready; the nonempty chain ends Up3.
- All-empty application stays at per-chain Down(start1), no business rows/callbacks, Ready1, cold0.
- Trailing empty blocks2/3 still allow nonempty chains to finish Up(end3).
- Default-query late failure first calls27, commits24; chain1 checkpoint is the raw final log of block1 (eventType5,index3), chain2 Up3. Reopen replays8, then cold0.
- Query6 early failure first calls19, commits16, keeps both chains Up1; retry16, then cold0.

Pinned source runtime/historical.ts: mergeAsyncGeneratorsWithEventOrder omits empty left arrays (1699..1716); when a right suffix exists it persists the last left event checkpoint (1712..1714). getLocalEventGenerator extends the final scan checkpoint to params.to (1083..1095). runtime/omnichain.ts uses one user transaction for complete merged Groups and updates checkpoints only for emitted Groups (371..465).

Implementation must use a separately identified log-only durable receipt grammar, bind actual input event bounds/digests and scan completion, retain exact raw checkpoints, and keep empty-chain Down distinct from synchronized/Ready completion. Do not weaken v11 parsing or create fake block callbacks. The original entry, partition and empty-chain activation red tests are retained with their exact failed inputs; they are not passing evidence.

Formal source mapping543/905 unchanged. These new native supplements do not add source mapping credit. Full/full-race/M45 remain open.


Default-query early failure (observation08) invokes19 callbacks, commits0, preserves both Down(start1) positions, replays32 after reopen, then cold0. Observation09 combines the raw-log checkpoint with a fully empty chain:11 calls/8 committed, chain1 raw block1/index3, chain2 Down1, retry8/cold0. These distinguish complete cross-chain Groups transactions from per-physical-block commits.

The implementation/receipt budget is explicit: the sum of all configured chains' inclusive physical block ranges, counting each chain once regardless of overlapping callbacks, must be <=12000+6*N (N=configured chains). This bound also applies to fully empty ranges and is checked before RPC/storage admission. It is an implementation/evidence size limit, not upstream behavior or a QuerySize rule. Larger sparse spans remain unsupported; Groups are never split into independently committed pieces.

The source position and scan completion are independent. v16 runtime-serve-logonly-v16 evidence binds immutable filter identity, every fragment's actual cached interval coverage, current cursor-to-fresh-finalized header lineage, the owned sync-ledger head, generation and exact before state. Where the logical cursor has not reached end, every remaining source SQL page is checked for raw matched logs; a first empty page alone is insufficient. ABI-skipped nonempty tails are rejected pending broader support. Scan endpoint identity is retained separately; no fabricated Up position or callback is added for an empty chain. Legacy formats reject v16 wire fields, including explicit null/case aliases.

Negative/recovery boundaries include changed or proof-free activation retries, missing/foreign filter or chain evidence, interval gaps, omitted/skipped/forked headers, wrong scan bounds, endpoint rejection cleanup, and unchanged old mixed closing/log-only rejection. Native application-exit tests keep PostgreSQL running while the application exits during a handler or after activation; PGlite abrupt/engine crashes remain outside acceptance.
