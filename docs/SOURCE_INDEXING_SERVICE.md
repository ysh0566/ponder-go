# Source-shaped indexing handler service (M38 verified subset)

Pinned source: `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`,
`packages/core/src/indexing/index.ts` and `indexing/index.test.ts`.

## Boundary

`indexing.NewService` dispatches setup, historical and realtime handlers using a
caller-owned **real** `store.IndexingStore` and its transaction-local cache.
The build keeps source-shaped callback, chain, contracts and filter objects.
Handlers receive reused argument/context/chain objects; clients and contracts
switch by chain, while DB and per-chain client identity remain stable. Setup
starts with a nil event (source `undefined`); like the pinned implementation,
setup does not erase a previously dispatched event. The legacy single-chain
`runtime.Engine` guard is not removed.

This is **not** an outer historical runner, automatic multichain indexing
transaction, readiness/trigger installation, realtime cutover or reorg service.
The caller still owns transaction entry, commit and rollback. The M37 stream
composition tests prove real extraction/decoding can feed this handler boundary,
not that a complete production outer runner exists.

## Explicit Go property facade

Go has no transparent JavaScript Proxy. A historical handler uses:

```go
transaction, ok := args.Event.Property("transaction")
if !ok { return errors.New("missing transaction") }
gas, err := transaction.Get("gas")
if err != nil { return err }
_ = gas
```

`Get`, `Set`, `Delete`, `Has`, `Keys` and `Inspect` are explicit equivalents of
the pinned proxy operations. An ordinary Go map read is **not** a tracked field
read and is not used to claim proxy parity. Historical event outer maps are
shallow copies; tracked components hold stable `*PropertyView` identities rebound
between callbacks. Raw realtime event maps retain their original identity and
are not profiled. Retaining a historical component retains its live binding, not
a snapshot. Missing fields and present `nil`/`filter.Undefined` remain distinct.
The special source `Has` checks default-column membership, not the actual map;
`Keys` corresponds to `Reflect.ownKeys`, not `Object.keys` descriptors.

Known fields record ordered first accesses even after convergence. An absent
known field creates an actual `InvalidEventAccessError`, resets filter includes
and the sampling count, and stores the **same error object** in the shared sticky
handler. Catching it inside a callback cannot suppress retry. This is not an
error synthesized by the test runner. The default message is empty, with `Key`
separate, matching the source. Includes resolve after 100 successful historical
samples; original replay preserves the full 1001-event input. Include order is
access-set order then required fields with stable deduplication.

## Errors, metrics, cache and RPC

Known domain errors use explicit `BaseError` inheritance/owned types, not an
arbitrary `ErrorName` or `Retryable` display trait. The callback's ordinary
returned error and sticky retryable errors are distinct;
sticky errors win even when user code catches or replaces them. Setup checks
sticky state before observing duration; event handlers observe duration first.
Invalid event access bypasses ordinary event-error logging/HasError, but setup
errors do not. Completed metrics and eventCount advance only after success;
setup does not increment eventCount. Historical processing updates the real cache
profile, whereas realtime leaves that profile binding untouched. `FieldOrder`
can supply JS args/result insertion order to the cache's existing profile API;
without it the existing native deterministic field-order fallback applies.

`Client.GetBalance` performs actual JSON-RPC requests with the setup/event block,
explicit override, or immutable/latest semantics. It does not pretend to provide
all Viem actions, profiling or prefetch. Optional persistent RPC caches use the
existing source request-cache adapter. Cache insertion is asynchronous and ignores late failures. The actual source
probe enters the insertion function before `GetBalance` resolves; a Go cache
writer goroutine can enter `CachePut` after `GetBalance` returns. That observable
invocation-prefix ordering remains **open**, not exact trace equivalence.
The tested common contract is nonblocking completion, actual request/cache
arguments, release-before-settlement and ignored late errors. Native timelines
are preserved unsorted, never rearranged to match the source. Existing
`rpc.AsyncRequester` has real ordered admission, but `rpc.Cache` and the source
request-cache adapter expose only synchronous `CachePut`; a goroutine wrapper
is not a genuine asynchronous cache-admission capability. The client set owns every cache writer; callers use
stop and await in-flight RPC/cache-read requests, then use `WaitCacheWrites`
before ending a transaction-bound cache lifetime or `Close` to cancel and join
registered writers. `Close` does not join already-running RPC/cache reads. Cache implementations must honor context cancellation.
Neither cleanup call is hidden inside successful `GetBalance` completion. The last eight client
facade original tests remain outside M38's formal native mapping.

Source query-builder classification is used for real backend failures. Actual
PostgreSQL connection termination is tested, not an injected error string.
PGlite has no corresponding client process to terminate; no fake dual-backend
connection-loss claim is made. Source PGlite query-builder transaction callbacks
are not SQL transactions; native caller-owned store transactions are stronger
and their rollback behavior is a documented safety extension.

Dispatch is serial and rejects reentrancy/concurrent calls. Native context
cancellation is an explicit resource-safety extension; observers of service
state must wait until dispatch returns. No background callback lifetime is
promised. Timing values use a monotonic clock; deterministic tests inject a
clock, not fabricated duration results. JS stack formatting is not a Go ABI. `OnError` receives the original envelope
and original error before wrapping; it is a Go diagnostic hook, not a claim to
reproduce source proxy-wrapper identity or formatted JavaScript error metadata.

## Original test credit

The unchanged original file contains 23 cases and 42 assertions. M38 targets its
first 15 service cases / 33 assertions and executes all of their runtime inputs.
Three cases retain TypeScript-only obligations and must remain unmapped unless
an exact genuine static counterpart is proved:

- `d75ae6d953a7bb8ea73c`, `afb6150d1f928a58bf24`: a normal callback function type
  does not have the mock-only `mockRejectedValue` property (not a return-type
  error).
- `8a08a48e2a99b23fa016`: deleting required
  `transaction.maxPriorityFeePerGas` (not assigning null).

Runtime success alone therefore permits at most 12 new formal mappings. All
proxy/reflection, identity, lifecycle, cancellation and cross-module composition
controls are supplemental and carry zero additional original credit. Current
execution outcomes and final hashes belong to the accepted aggregate report,
not this design document.


### Supplemental evidence limits

The genuine source reflection probe has 23 operations. Native replay executes
22 supported facade operations; the `Object.keys` descriptor operation remains
an explicit syntax/API gap (no fake empty result and no test skip). `Inspect`
compares semantic fields and actual read order, not Node-specific formatting.
Neither difference earns source credit or weakens an original assertion.

The service capture archives nine source synchronization-table snapshots before
and after methods. Those snapshots are **archival-only** in M38's service replay;
it does not compare native synchronization-table snapshots or claim persistent
client-cache parity from them. Original business writes and external `db.find`
assertions do execute against real native IndexingStore/backends. Separate
M37-stream composition checks actual native business rows, journal, event and
caller checkpoint/progress boundaries.


## Accepted execution

`parity/runs/indexing-service-m38/full-verify-02` completed all 46 stages on
2026-10-25 at 11:43:06 UTC. Both full native modes passed 3070 test nodes / 2807
leaves without test failure or skip; all nine canonical collectors accepted the
current proof. Only twelve complete original cases were mapped: formal coverage
is 523/905, not full parity. The three service static gaps and four earlier
runtime-index static gaps remain unmapped. See batch-37 and summary for frozen
hashes; this local accepted candidate still requires parent publication review.
