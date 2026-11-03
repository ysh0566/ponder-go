# Next development after v16 acceptance

The selected v16 stage has passed its frozen queue and independent result audits. Start from the commit containing this report; the exact remote main/commit and CI observations are recorded separately in the release receipt.

The next blocking dependency on this chain is long sparse log-only ranges beyond the current total physical block budget (12000+6*N). The pinned Ponder0.18.2 coordinator pages actual source rows/events; empty physical heights do not necessarily consume QuerySize. The current native driver expands an emitted Groups interval into per-height authenticated headers and receipt segments. Splitting that Groups into separately committed batches would violate the observed cross-chain rollback contract, so removing the admission guard alone is insufficient.

Start from the published v16 ancestor in a fresh /root/repo worktree and preserve v16 seals. Read pinned upstream 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334 runtime/historical.ts and runtime/omnichain.ts again, then run actual source oracles for sparse gaps above the current budget, one completely empty long-range chain, all-empty ranges and overlapping callbacks under default and small pagination. Derive source SQL/CP/Ready/migration and transaction rollback behavior before changing storage representation.

Design a bounded-memory authenticated sparse/streamed physical proof that preserves one complete Groups business transaction, actual callback input/digest/order, lineage/finality anchoring, empty-chain Down(source start), exact restart/activation evidence and legacy v11 isolation. Broader ABI-skipped nonempty tails also need actual decoding/source-completion proof; the current raw-log absence check intentionally rejects them.

Acceptance needs real RuntimeRunner/RPC/ABI/SQL on PG and PGlite, cross-chain failure discriminators (full-batch0 versus a previously committed prefix), cold reopen, application exits, strict malformed/stale/gapped witness rejection, same-snapshot affected regression/race, dual code/result reviews and exact baseline retained/omitted/new sets. Do not count supplemental native/source observations as formal mapping additions.

Other work remains: mixed closing-block/log-only chains; omnichain account/factory/trace/live/pending/reorg/cachefalse/setup/start0 combinations; actual isolated runner; complete CLI modes and source/type mappings; PGlite abrupt/engine/database-server crashes; full repository/full-race and complete M45 acceptance. Formal mapping stays 543/905 until original records gain their required evidence.
