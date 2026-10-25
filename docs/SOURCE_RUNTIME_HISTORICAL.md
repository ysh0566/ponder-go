# Source local historical orchestration (M36, verified subset; publication pending)

Pinned source: `ponder-sh/ponder` commit
`17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`,
`packages/core/src/runtime/historical.ts`.

## Boundary

`LocalSourceSync`, `LocalSourceEvents`, and `LocalSourceInMemoryEvents` connect
existing source range acquisition, source cache intervals/progress, raw event
construction, and the real `SourceSyncStore`. They do not call the native
`Engine.ReadSourcePage` or seed outputs from source expectation records.
`SourceRawEventBatch` carries raw events before handler decoding. Existing
`SourceEventBatch` is a distinct, decoded-public-event merge boundary.

The implemented original replay scope is the first eleven `historical.test.ts` cases,
with twenty-four static assertions and fourteen generator invocations. The
entire unchanged original fourteen-case file runs separately on PostgreSQL and
PGlite. The last three cases remain unimplemented/unmapped in this stage.
The source case titled `getHistoricalEvents() omnichain` calls the multichain
function with only one chain; it is not proof of real omnichain behavior.
The complete 28-stage aggregate, fresh collectors, strict mapper and manifest
have accepted these eleven mappings: cumulative 508/905, 397 unimplemented.
This is local acceptance, not publication or full scheduler completion.

## Persistence and scheduling

`SourceSyncStore.WithTransaction` binds all source store operations and its RPC
cache adapter to one callback-scoped transaction. Ordinary calls outside the
callback retain existing per-batch commits. Bound calls are serialized to protect
PGlite and escaped handles are rejected; nested callbacks join the transaction,
not a savepoint. A failed nested operation prevents outer commit.

The store transaction owner serializes transactions. To retain source range
prefetch, the scheduler first acquires RPC data and shared in-memory child
addresses without database writes, then persists child rows, block data and
intervals atomically in order. The next range acquisition starts before the
previous block phase finishes; its writes wait for the prior commit. This is not
a claim of concurrent database transactions. Source range acquisition performs
no database reads; its database operation is insertion of discovered children.
Existing `SourceHistoricalSync.SyncBlockRangeData` remains a wrapper that writes
those children immediately after the same acquisition.

Shared child-address discovery is mutated during acquisition, as upstream does.
Database rollback does not rewind this shared in-memory map. The owner must treat
an aborted synchronization as aborted rather than claiming its discoveries were
committed. Cancellation cancels and joins in-flight producer work. Progress
`current` retains the exact final/start input object identity; cached progress is
yielded immediately, and an entirely cached range makes no new RPC or writes.
The adaptive window starts at 25 and uses the source rounding/clamping formula.

## Explicit compatibility limits

- Upstream PostgreSQL query-builder transactions are real SQL transactions.
  Upstream PGlite `queryBuilder.transaction` invokes the callback without a real
  SQL transaction. Native PGlite atomicity is a stronger project invariant, not
  equivalent to upstream failure behavior. Successful original assertions remain
  the only mapping basis; rollback controls are supplemental, zero-credit checks.
- Upstream's detached local-sync producer does not attach rejection handling to
  `syncInterval(...).then(...)` and does not forward consumer return to its
  interval producer. Native error propagation/cancellation/join are deliberate
  safety extensions, not claims that the source has those guarantees.
- Native APIs currently do not expose the full upstream logger/Prometheus metric
  surface. `IsCatchup` is retained as input context; upstream logging differences
  for this flag are not evidence of complete metrics/logging equivalence.
- Startup is the approved loopback adapter, not pristine execution. Stored
  contexts preserve absolute roots; new-root replay requires fresh preparation.

## Evidence

`parity/runs/runtime-historical-m36/source-{postgres,pglite}-01` preserves the
first complete source captures. `source-transaction-focused-01` contains normal
and race transaction controls. `native-focused-01` preserves the initial native
failure: checkpoint construction used the RPC-only quantity parser on normalized
bigint block rows. The production parser was corrected without changing source
inputs or assertions. Later complete evidence must supersede, not erase, this run.

`order-{postgres,pglite}-01` uses block-only filters and proves range-phase order,
not real range RPC prefetch. Its historical `transaction_committed` event label
means query-builder promise resolution; the interpretation sidecar explains the
PGlite distinction. The separate `log-order-{postgres,pglite}-01` probe includes
a real ERC20 log filter and verifies actual next-range eth_getLogs starts before the prior query
builder promise resolves, with next block acquisition after that resolution.
It preserves source transaction-mode metadata and grants zero source credit.

The native iterator exclusively owns shared progress, child, and callback/filter
maps for its entire active lifetime, including between yields. Prefetch continues
in the background; callers inspect final identity only after completion or stop
has joined. Cancellation takes priority over already queued range cursors.

`native-focused-02` passes all eleven original cases on both real backends in
normal and race modes (twenty-two original backend cases per mode).
`local-controls-focused-01` preserves a canceled-buffered-queue red control;
production now checks cancellation before returning buffered cursors. These
focused results do not replace the mandatory complete aggregate.

`local-controls-focused-02` passes all 22 supplemental leaves in normal and race.
`local-controls-mutations-01` separately compiles Go overlays without modifying
production or assertion files: both databases reject sequential scheduling,
suppressed prefetch errors, and unbound persistence at the exact intended test
leaves. The six matching controls pass again without overlays. This establishes
that prefetch overlap, error propagation and rollback assertions detect the
corresponding defects; it is not original-source test credit.

Complete acceptance: `parity/runs/runtime-historical-m36/full-verify-01`;
normal/race each 2863 nodes / 2634 leaves, no named failures or skips.
All 1084 native input hashes stayed fixed through preparation and both modes.
Fresh canonical collectors were rerun before mapping in `collection-01`.
