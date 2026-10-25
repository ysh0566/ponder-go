# M37 source capture boundaries

The pinned upstream `historical.test.ts` is executed unchanged in full (14 tests,
29 static assertions). The final three declarations (five assertions, no type
error directives) are the M37 native candidates; the first eleven are separately
mapped by M36. The original test named `getHistoricalEvents() omnichain` actually
calls single-chain `getHistoricalEventsMultichain`. Its name is preserved, and it
is not treated as proof of actual omnichain behavior.

`source-runtime-multichain-original.mjs` uses the previously authorized loopback
Anvil startup adapter. The capture observer records original ordered chain and
callback objects, per-chain progress/cache/child state, crash checkpoint, raw RPC
request/response streams with chain IDs, each iterator operation/result, and nine
physical table snapshots. Full input objects retain opaque function names and
object reference identities; functions are not evaluated by native replay.
Direct `Date.now()` calls from the source historical coordinator are observed
without altering their values. RPC/library clock calls are not included in the
coordinator clock ledger. Inputs and raw RPC can seed native replay; generated
outputs, event-build observations and physical after-snapshots are targets only.

The supplemental probe suite uses two separate real Anvil worker processes. The
second process is explicitly configured to chain ID 2. Both real PostgreSQL and
PGlite are exercised. It controls clocks and explicit causal response gates without
replacing source coordinator, decoder, database, or event-generator behavior.
Sixteen tests cover mixed cache completion order, omnichain pending/catchup,
multichain catchup, three finalized-backward mode rules, interleaved refetch,
isolated early return, all-filter refetch positional truncation, sparse refetch
slots, checkpoint aggregation, cross-chain crash safe-block cache/uncached fallback, bounded end with an empty event chain, and parallel latest-block admission with deliberately reversed completion. Actual captured coordinator setup invocations populate the database before crash probes; expected physical rows are never replay seeds. The backward/isolated scenarios intentionally
select one active chain; other scenarios exercise both chains.

Supplemental cases have zero original-case credit. These sixteen probes do not by
themselves establish all boundary
conditions, real-time handoff, top-level indexing transactions, readiness, or
triggers. PGlite source transaction behavior remains distinct from the native
project's stronger transaction invariant, as documented in M36.

Raw reports are never overwritten. Initial probe startup selected unsupported
worker ID 101 and was interrupted before test execution; the correction selects
the existing adapter-supported next worker ID, without changing the adapter or
business test. Its failure/interruption record is retained.

Fixture commands require explicit fresh PostgreSQL and PGlite evidence roots,
validate current capture inputs plus durable context references, and retain
separate backend cases. Context roots remain absolute and require fresh preparation
and execution after relocation. No historical evidence root is rewritten.

Revision after native replay found a harness defect: each capture now restores
its prior RPC request wrapper in `finally`, preventing a priming call ledger
from absorbing later checkpoint lookups and main-call requests. Earlier raw
captures and the first gated run (14/16, nested observer recursion) are retained.
The corrected gated runs use no timing sleeps: chain 2 event-query completion
waits for an observed chain 1 consumer yield through the query input end block;
the mixed-cache chain 1 first block response waits for chain 2 consumer progress
through its input finalized block. Latest chain 1 response waits for chain 2
response completion. Control events record actual waits, releases, and yields.
Unconstrained admission interleavings are not promised identical, but each gate
release must be causally after its input-defined boundary; generated outputs
are never used to choose replay scheduling. Isolated immediate return remains
immediate, without a consumer pause or weakened unused-RPC checks.

The latest-refresh test also captures a nested `error_call` named
`latest-first-error`, run against the preceding call's real persisted database.
Chain 1's real latest response is held while chain 2's latest request rejects
with the explicit injected `M37 injected latest failure`. The source iterator
must reject before cleanup releases chain 1. Cleanup drains the observed
requests before recording final state and restoring wrappers. The full nested
input, raw response/error, clocks, steps, results, and before/after snapshots
are captured; this is a concrete Promise.all first-error probe, not another
original test credit. Source pending-promise cleanup and native cancellation
cleanup are intentionally distinguished, not claimed identical.

The isolated immediate-stop test retains its original input and appends a
separate `memory_error_call` on the actual resulting database. This nested call
uses the pinned upstream account transfer callback factory, holds the real
block-zero response, and injects `M37 injected trace failure` only for trace-zero.
Its first next must reject before cleanup releases the block, establishing the
in-memory block/trace Promise.all first-error boundary. All admitted requests
are drained and captured, with no synthetic snapshot seeding or consumer pause.
