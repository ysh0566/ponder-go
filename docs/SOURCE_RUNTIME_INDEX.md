# Runtime index helpers (M35, locally accepted; publication pending)

The pinned source is `ponder-sh/ponder` revision
`17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.
`packages/core/src/runtime/index.test.ts` has **15 original cases, 30 runtime
assertions, and 6 TypeScript error directives**. The ten cache/interval cases
contain **18**, not 19, assertions. Supplemental tests do not increase this census.

## Implemented production boundaries

- `CachedSourceBlock`: object-identity cache lookup, per-fragment intersection,
  factory dependency invalidation, minimum contiguous cached height, and the
  source's undefined/infinity distinction.
- `RequiredSourceIntervalUnion`: sparse missing intervals without the bounding
  expansion used by the existing `RequiredSourceIntervals` filter-recovery API.
- `GetSourceCachedIntervals` and `GetSourceChildAddresses`: disabled caches do not
  read stale persisted data; factory insertion order and repeated-key replacement
  remain distinct from earliest-birth merging.
- `GetLocalSourceSyncProgress`: concurrent start/current requests, retained block
  references, absent versus bounded/future end, inclusive start checkpoints, and
  explicit retry-null request context. The production RPC adapter implements the
  source's nine cancellable exponential null-block retries. Transport-error retry
  classification still belongs to the supplied requester.
- `MergeSourceEventsWithEventOrder`: per-generator checkpoint partitioning,
  original event identity, original range starts, only the first minimum generator
  advancing, and next-before-yield scheduling. Child generator lifetime remains
  owned by its caller.

These are reusable production APIs. They do not implement the complete
`runtime/historical.ts` historical/omnichain/isolated/catchup/refetch/crash-recovery
scheduler, nor constitute full project parity.

## Original source execution

`tools/source-runtime-index-original.mjs` runs the complete original file with
all original setup hooks, input declarations, assertions, snapshots, and cleanup.
It uses the explicit-loopback Anvil startup adaptation implemented by
`tools/loopback-adapter.mjs`. Therefore these runs are **environment-adapted, not
pristine**. The original checkout is unchanged. Before/after hashes and the sole
startup difference are retained in each run's manifest and diff.

The observer captures 28 public calls across all 15 cases, including raw RPC
responses and retry context, real SQL insertion/query inputs, child-address cache
inputs, and generator next ordering. It does not feed expected outputs into Go.
The native driver independently invokes the Go helpers, production cache store,
and event conversion and compares results, own properties, input mutation and
object identity. PostgreSQL and PGlite remain distinct real backends.

Both original source backend runs passed 15/15:

- `parity/runs/runtime-index-m35/source-postgres-01/`
- `parity/runs/runtime-index-m35/source-pglite-01/`

Each native database backend now consumes its own complete source backend capture.
The collector retains all original source bodies and the full thirty assertions.
The final aggregate prepares fresh captures before the native input snapshot;
old capture attempts remain separate historical evidence.

## Focused verification, not full-suite acceptance

`focused-05` passed normal and race modes: the complete RPC package had 17
top-level tests and 157 test nodes per mode; runtime helpers had 11 top-level
tests and 115 nodes per mode, with no failures or skips. Vet passed for both
packages. The unchanged native inventory was 1078 files with hash
`469935cdd15d63486a6f58755d07ea40df9d54e6562d49287f00fb0077f22639`.
This includes original-file replay, ten isolated pure declarations, and explicit
supplemental ownership/concurrency/retry tests. At that focused checkpoint no new ledger source credit had
been awarded. Complete aggregate acceptance is documented below. Eleven cases
without native type blockers are mapping candidates; the four cases containing
the six inference directives remain unmapped while native type treatment is pending.

The isolated pure-declaration runner has zero source credit. It keeps all ten
original declarations and eighteen assertions, but deliberately does not run the
original Anvil/database hooks, and cannot substitute for the full-file runs.

## Type obligations

`tools/source-runtime-index-types.mjs` runs fresh actual TypeScript compiler
programs. On the identical pinned checkout with verified generated fixtures, the
results were **0 / 6 / 0** diagnostics: original directives retained; only six
comment tokens removed in memory; supplemental explicit `Filter | Factory` Map
key annotations. The six diagnostics are TS2769 at the original constructor
locations, with no unrelated diagnostics. Original files are not rewritten.

These are heterogeneous `new Map` inference limitations, not a contract forbidding
factory keys. The Go runtime must positively accept the same mixed keys and retain
identity. No unrelated Go compile failures were invented as replacements. The
current dynamic `filter.Object` cache boundary does not establish a closed static
Go `Filter | Factory` union; that broader typed-boundary limitation remains
explicit and is not concealed by the TypeScript audit.

## Ordered request ownership

A deterministic same-height regression exposed the original Go goroutine-based
request startup: start/current objects could swap. The actual red run and old
input snapshots are retained in
`parity/runs/runtime-index-progress-order-red-20261025T054500Z/`. A probe of the
pinned original function confirms synchronous request registration with
independent, reverse-order completion and exact object ownership.

`rpc.AsyncRequester.BeginCall` now provides explicit synchronous FIFO admission
for asynchronous calls. One dispatcher reserves shared concurrency, rate slots
and first-attempt request identity before concurrent network work. This is not
a promise of network arrival order, nor a new ordering guarantee for legacy
synchronous `Call`. Progress uses an explicit `BeginBlockByNumber` boundary;
there is no goroutine-based fallback for a synchronous requester. Controls cover
queued/admitted cancellation, released capacity, rate-wait cancellation, retry
classification, reverse completion and 128 same-height identity iterations.

## Retained unsuccessful attempts

- `focused-02`: the initial pure-case census incorrectly required 19 assertions;
  native replay exposed test-only schema-prefix and JSON-number representation
  errors. Source assertions were not changed. The first TypeScript audit against
  the main checkout lacked generated contract fixtures, producing seven unrelated
  diagnostics; its timestamped report is retained.
- The pure extraction initially failed to resolve `viem` outside the upstream
  package tree. A dedicated resolver now uses the same pinned installed package.
  The failed invocation log and observed error are recorded; its first canonical
  Vitest JSON report was overwritten by the successful retry, so it is not
  presented as a retained full report.
- `focused-03`: all pure cases and compiler obligations passed, while native SQL
  output comparison exposed the decoder's float64-versus-int64 issue. Switching
  the replay decoder to the existing identity registry's `json.Number` contract
  fixed the test adapter, without changing SQL inputs, expected values or
  production implementation.

- `full-verify-01`: aggregate PG 53398 differed from the adapted source runners
  requiring 55432. Historical, realtime and sync-store PG entry points rejected
  the configuration. The partial normal run was interrupted with exit 130; race
  was not started. Original failure logs and the interrupted adapted context
  archive remain. The task PostgreSQL database underwent recorded automatic
  recovery, a successful query, and a clean shutdown. No database data was deleted.
- A launch-control check without sourcing the task environment correctly hit
  the default temporary filesystem's 5 GiB capacity gate. Its log remains; the
  same seven controls passed with the intended workspace TMPDIR.

The focused evidence above is historical. The complete acceptance below supersedes
its pending-verification status, not its explicit scope limits.


## Complete aggregate acceptance and remaining scope

`parity/runs/runtime-index-m35/full-verify-04/result.json` completed successfully at
2026-10-25 07:47:30 UTC, with all 22 stages exiting zero. Normal and race each
passed 2783 test nodes (2565 leaf cases), zero failures or skips, across 60 disjoint
batches. The same 1078-file prepared native fingerprint was retained through both
modes and final collection: `23d113072444ffa0e81955676d47430adef5097b5e1441bf63dcb70dbf2cfdea`.
Both complete original backend captures passed 15 cases; all four native
backend/mode combinations passed. Exactly eleven cases with twenty-four assertions
are now mapped. The four cases with six source-language inference directives stay
unmapped, despite their runtime success. Total formal coverage is 497/905; 408
records remain unimplemented. This does not complete historical/multichain scheduling.

Run 02 was interrupted by the execution environment during race; its 2783 normal
passes and partial race are historical only. Run 03 completed normal/race but failed
the final React type stage because `tsc` was absent from PATH. The installed pinned
TypeScript 5.4.5 resolves it; a fail-fast exact-compiler preflight and missing-compiler
negative control were added before the fresh successful run 04. No failed run was
rewritten as successful. Acceptance-only collection then preserved fixed-M34 old
SQL/events/filter/bloom harness bytes in a bounded historical sidecar. It never
substitutes for current execution context; tampered fixtures/raw files, deleted or
renamed references, wrong commit/blob/hash and current-context misuse are rejected.
The mapper assertion digest was corrected to the existing manifest contract (joined
original assertion texts), retaining the rejected JSON-serialization attempt.

The new batch 34 freeze preserves original evidence bytes and a SHA256 inventory.
Context archives intentionally retain absolute execution/project/upstream roots.
Compiler binaries and installed dependencies are not published; their provenance
hashes and compile transcripts are retained. Static archive inspection is portable,
but direct acceptance replay from a different path requires fresh pinned-tool/source
preparation and a new full run/context. Do not rewrite old roots or claim an archive
is a self-contained runnable environment. Publication and safe cache cleanup require
separate verified closeout; the full project remains unfinished.
