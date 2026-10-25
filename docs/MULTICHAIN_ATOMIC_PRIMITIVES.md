# Atomic multichain setup and historical-group primitives (M42)

This milestone adds **finite primitives**, not `RunHistoricalMultichain`, extraction/refetch orchestration, readiness, views, realtime notifications or cutover. M43 must feed real M37 generator/refetch/decode output into these primitives. No new original-test credit is claimed: the formal ledger remains 543/905, with 362 pending.

## One setup transaction, one persistent binding

`indexing.Owned.InitializeMultichain` accepts one `InitialChainCheckpoint` per configured chain. It validates unique configured IDs/names, exact membership, chain identities, decimal checkpoint encoding and the source start/down and finalized/up block tails before invoking handlers. It chooses the lexical minimum of the **complete encoded** start/finalized checkpoints; genesis still carries the real chain ID. Setup RPC block selection remains the callback's independently configured block. A setup block must never substitute for sync progress.

`store.Actions.InitializeMultichain` requires an uninitialized business domain (no chain/progress/finality/private-cursor/journal rows). Its callback receives one Writer and a transaction-local attribution function. The service's internal hook calls attribution before each callback in the original nested build/group order, including consecutive repeated-chain callbacks. Attribution explicitly flushes buffered writes and seals the previous segment before changing chain. Consequently the existing global-MAX `commitBlock` cannot relabel earlier callbacks as the next chain. Setup uses the same service, handler arguments, context/database facade and per-chain clients throughout; it does not regroup callbacks or construct one service per chain.

All setup business writes, journal segments, latest/finalized/safe metadata, private runtime cursors and safe-prefix pruning share one SQL transaction. The single immutable binding is revoked when dispatch ends, but `Finish` receives only the actual outer transaction result. No per-chain success is mistaken for commit. Failure discards cached rows/buffers while preserving learned access profiles. Revocation is not context cancellation: admitted independent RPC/cache work can settle after normal completion or business rollback.

## One historical group

`Owned.HistoricalGroup` accepts an already-extracted/decoded group and a target checkpoint. It checks the complete configured versus durable chain set, event chain/checkpoint identities, ordered event checkpoints and absence of overlap with the durable private cursor. The underlying `Actions.ProcessHistoricalGroup` validates actual chain name/ID, all checkpoint identities/order and mandatory private cursor in the transaction. It does not use the older single-chain runtime guard or initialize metadata in a separate transaction.

Committed replay skips handlers. Empty groups advance without fake handlers. Real dispatch is followed by an explicit cache flush, journal sealing, latest/finalized/safe metadata and private cursor update, then prefix pruning, all before commit. SQL notification suppression is enabled and no Go realtime notification is published. There is no automatic retry or refetch in this API.

Only completed-event metrics are restored for callback/flush/metadata failure. Successful-dispatch attempt counters and learned profile counts survive, as observed in the pinned source's second-event failure. A failure of outer COMMIT occurs outside that source catch boundary; completed metrics are retained on the typed ambiguous-commit error rather than falsely asserting rollback. Row caches are conservatively invalidated on any unsuccessful transaction result.

`store.CommitOutcomeUnknownError` means COMMIT was attempted and no reliable acknowledgment obtained. It is neither rollback proof nor commit proof. Callers must inspect durable `Owned.Checkpoints` (or reopen a failed owner) before replaying. A commit-then-lost-ack followed by replay observes the durable cursor and does not dispatch handlers again. No cross-domain transaction is implied.

## Recovery boundary

`Owned.RevertBusiness` admits work through the owner lifecycle gate and invalidates session predictions before invoking the silent business-only rollback primitive. It retains the existing multichain global-operation-suffix undo and safe-prefix refusal. It does **not** rewind the independently owned sync domain or reset historical runner cursors. Existing `RevertRuntime` remains single-chain; its guard is not removed. Existing low-level raw external writes still require explicit session invalidation and ownership coordination.

`Owned.Checkpoints` participates in lifecycle admission/Close joining. No raw sync backend pair or replacement sync implementation is exposed. Extraction ownership remains for the later runner stage.

## Source oracle and intentional backend differences

Pinned upstream is unchanged at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. The new source harness invokes real `runMultichain` with real two-chain builds, handlers, databases and RPC. It wraps the real historical iterator only at the consumer boundary, yields a real selected prefix, then throws a branded nonretryable harness stop on the next pull outside the business transaction and before cutover. Harness stops are recorded separately from handler/metadata failures; owned work is drained/closed. No expected outputs seed database rows or fake coordinator.

The source PGlite userQB transaction is `callback(db)`, not SQL atomicity. Native PGlite retains the previously approved separate business/sync directories and actual SQL transactions. Stronger native rollback and topology are intentional safety differences, not source rollback/layout parity. Actual source metadata-failure captures are used to show this distinction. Source setup has no native undo-journal attribution protocol; native journal sealing is an explicit safety extension, not an invented source behavior.

## Verification scope

Supplemental native tests cover actual PostgreSQL and persistent PGlite setup success/second-chain failure, interleaved shared rows, repeated-chain attribution, flush/seal/metadata failures, commit-before/lost-ack boundaries, real handler failure/sticky/refetch-relevant error, empty groups and durable replay, close/reopen, independent RPC cache persistence, dead old bindings, cancellation with actual learned prefetch, and silent suffix rollback/safe-prefix refusal. Causal controls use channels and SQL notification sentinels, not arbitrary sleeps.

Every test cluster is a fresh M42 task-owned fixture. The wrapper records its newly allocated data directory, initial/final inventory and shutdown; driver preflights check receipt and `SHOW data_directory` before old Vitest fixture DDL. Historical PostgreSQL schemas and both preserved preexisting data directories are outside scope and untouched. All failed attempts, old batch40 evidence and source assertions are retained.

Full acceptance: `parity/runs/multichain-atomic-m42/full-verify-03` finished at 2026-10-25 19:38:14.379 UTC. All 69 stages, 13 explicit canonical collectors and the actual summary closeout succeeded; the fresh fixture stopped with exit code 0. Normal/race each passed 3405 nodes / 3087 leaves, with no failures/skips, across 23 packages / 288 top-level functions / 87 serial batches. All 1156 prepared/finished inputs match fingerprint `95062ec28dce44addc309dabf2bf7c580f22876767de0e9d500991dc11c875c6`; all 119 harness hashes are unchanged during this run. Batch41 freezes the successful proof and preserves batch40.

Diagnostic full01 retained its two failing race cleanup leaves: the intended independent cleanup context edit had not applied, so already-completed Close could select the canceled operation context. All 1275 native/harness inputs were archived before the exact test-only two-hunk correction, independent review and focused normal/race verification. Full02 passed all 69 stages and 13 collectors but its summary guard omitted M42 in four legacy path arrays. All 1288 inputs/outputs were archived before the four exact additions and whole-real-guard regressions; 407 old/new controls passed before fresh full03. Neither failed receipt nor frozen manifest was rewritten as success. The full02→03 prepared-input delta records exactly 11 regenerated source fixtures and no native code changes.

Cancellation can cause PostgreSQL's driver to discard the fixed lifetime-owner connection. Such an owner remains fail-closed rather than reconnecting with stale cache/lock assumptions. The cancellation proof accepts only the specific closed-connection diagnostic leaves during teardown, joins all prefetch work, then reopens the same storage to establish durable rollback. PGlite is also closed/reopened for the durable check. This is not a promise that an owner remains reusable after driver-level connection loss.
