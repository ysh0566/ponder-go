# M37 historical extraction coordination

Status: complete local M37 verification passed; pending parent independent review
and publication. Formal mapping is 511/905 (507 Go + 4 SDK), with 394 original
records still unimplemented. This is not full-project parity.

## Source and public boundary

Pinned Ponder commit: `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.
The new historical extraction API ports the three generator coordinators in
`packages/core/src/runtime/historical.ts`, its refetch helpers, and
`getOmnichainCheckpoint` in `runtime/omnichain.ts`:

- `HistoricalSourceMultichain` merges ready batches by completion, not by event
  checkpoint. A yielded generator is rotated to the tail and its next operation
  is started before the consumer receives the current result.
- `HistoricalSourceOmnichain` uses the existing ordered merge, retains events
  beyond the global finalized/end boundary as pending, releases eligible events
  in catchup, and emits the remaining pending array at completion.
- `HistoricalSourceIsolated` uses the selected chain's local history and does not
  query another chain's timestamp-safe crash recovery block.
- `RefetchLocalSourceEvents` reads all callback filters from the real sync store,
  rebuilds raw events, and preserves pagination. `RefetchHistoricalSourceEvents`
  decodes them and fills the original interleaved chain positions. It deliberately
  preserves surplus-result truncation and sparse output slots rather than matching
  event checkpoints or fabricating a one-to-one input/output relationship.
- `GetSourceOmnichainCheckpoint` preserves minimum start/finalized, optional
  maximum end, and the completed-chain rules for current checkpoints.

`SourceHistoricalCoordinatorOptions.Chains` retains the source `perChainSync`
iteration order. `BuildChains`, when supplied, retains the distinct indexing-build
order for finality request admission and query sizing. Chain, callback, block and
progress objects retain actual input identities. Decoding uses the existing ABI,
block and account decoder, not expected decoded test output.

This is not the outer `runOmnichain`, `runMultichain` or `runIsolated` indexing
orchestrator. User handler transactions, realtime cutover, reorg coordination,
readiness and triggers remain separate work. The new extraction APIs do not claim
source logger/metrics instrumentation or application lifecycle integration.

## Observable scheduling and safety boundaries

Catchup is triggered when at least one build chain reaches
`max(reorgWindow seconds, 30 seconds)`. Refresh registers all initial `latest`
block actions synchronously in build order using `SourceSyncProgressRequester`,
then lets their completion/finality searches overlap. Refresh observes the first
completed failure rather than selecting an error by chain order; it cancels and
joins remaining waits before returning, and applies no partial refresh progress.
Production callers may
supply that interface explicitly or use an `rpc.AsyncRequester`; there is no
implicit goroutine-around-synchronous-request fallback. The action adapter
standardizes the actual returned block, retaining identity and rejecting missing
required fields. Omnichain throws on backward finality, Multichain ignores that
chain's backward result, and Isolated ends. Isolated's initial latest action does
not enable the source null-block retry flag.

Initial local-generator acquisition contains different asynchronous database and
filter paths, so cross-chain network arrival order is not promised. The merge's
Next admission, already-ready tie rotation, completion and checkpoint semantics
are explicit and separately tested.

All iterators exclusively own mutable progress, callbacks and shared maps for the
whole iteration lifetime, including time between yielded values. The caller must
not read or mutate them concurrently. Abort paths cancel the shared context and
join child acquisition before returning. This explicit Go resource/cancellation
contract is a safety boundary, not a claim that the TypeScript helpers cancel all
prestarted promises identically. The internal async wrapper alone cannot cancel a
sequence that closes over another context; coordinator stop paths cancel their
shared parent context before joining.

M36's explicit database distinction remains: source PGlite query-builder
transaction callbacks are not real SQL transactions, while native persisted
range/block/interval writes use the project's atomicity invariant. Cancellation
and refetch non-advancing-cursor guards are supplemental safety behavior, not new
source mappings.

## Source case accounting

Only these three historical original records are newly mapped:

- `d1f4e702f072c3b5eae1`: basic historical Multichain, two assertions.
- `983c1f8526efc67c34aa`: name says omnichain, but its unchanged body invokes
  single-chain Multichain, two assertions.
- `a2f204a8ab7598691e12`: crash-recovery filtering, one assertion.

They contain five source assertions and zero type directives. All fourteen
original historical cases are still captured unchanged on both databases; the
prior eleven retain separate M36 mappings backed by fresh current-run proofs. Passing the above three alone does not prove
real multi-chain or Omnichain behavior. Supplementary real two-chain probes and
native controls are recorded separately and add no source credit.

## Evidence and environment

New evidence lives under `parity/runs/runtime-multichain-m37/`. Original input/RPC
capture, decoded-result comparisons, database snapshots, source declarations,
source-clock trajectories and function descriptors remain distinct. Function
names are opaque nonexecuted descriptors, not a claim of function-body or
same-named-function identity equivalence.

Failed setup/compilation and superseded focused runs are retained, not rewritten
as success. Source startup adaptations remain explicit and do not modify original
business inputs/assertions. A complete frozen aggregate and freshly generated
proof closure are required before mapping or publication. Absolute execution roots
in historical contexts are retained honestly; replay from another root needs a
fresh pinned-source/tool run, not rewritten original context paths.

## Corrections exposed during focused verification

The first combined supplemental replay is retained as red evidence. Ungoverned
completion-order goldens differed across independent database schedules; the
revised probes use explicit provider/query response gates released by recorded
consumer actions, not sleeps, output sorting or arbitrary permutation matching.
A source observer originally remained attached after a crash-prime invocation;
restoring its exact prior request boundary separates the eight prime requests
from the later checkpoint lookup and main invocation. Old captures remain intact.

Immediate isolated stop exposed a production admission gap inherited from the
earlier in-memory acquisition implementation: goroutine scheduling could defer
requests that the source registers before its first yield. Its bounded correction
registers each initial block/trace prefix through the supported async requester
before yielding. A per-acquisition context is created before admission; the first
block/trace failure cancels its sibling and joins both response owners. Plain
synchronous-only requesters remain serialized compatibility, not an async
scheduling-equivalence claim. Original-stage focused regression and the complete normal/race run both passed;
this finding is not a claim that previous milestone coverage established every
scheduling boundary. The source stop input and exact raw-request comparison are
retained unchanged.

The local store dependency is the three-method `SourceLocalHistoricalStore`
capability interface. `SourceSyncStore` still owns the same actual transactions,
mutex, SQL and bound callback pointers; nil and typed-nil stores are rejected at
the existing required-store boundaries. Supplemental response gates wrap a real
`GetEventData` call only after it fully returns, outside SQL and store locks. The
rejected backend-Commit gate retained the outer store mutex and deadlocked; its
interrupted run remains evidence, not a pass.

Both source first-error probes deliberately hold one actual provider response
while another fails, then verify iterator rejection precedes explicit cleanup
release. Native cancels/joins its pending sibling before returning; source
promises are drained by the observation harness after rejection. This preserves
the temporal error boundary while documenting—not concealing—the different
resource cleanup contract.

## Complete acceptance

`parity/runs/runtime-multichain-m37/full-verify-01` passed all 35 stages on
2026-10-25 at 10:25 UTC. Normal and race each passed 2,983 test nodes / 2,736
leaves with no test failures or skips; PostgreSQL 405 and PGlite 402 leaves per
mode. All 202 listed top-level functions across 22 packages ran in 65 disjoint
serial batches per mode with original timeouts. The prepared and final 1,096-file
native SHA256 is `740fa67bb628fa820aa991b8ed31889c12af9a54b06fa081e6409fdf17bee639`;
all 33 frozen harness hashes remained unchanged. Fresh source capture, compiler
controls, SDK client and React, vet and builds passed.

Eight canonical proof collectors point to this exact run. Only the three original
records above added credit, five assertions and no type directives. The two real
first-error calls are nested within the existing sixteen supplemental probes;
they do not increase the original source census.

`collection-01/commands.txt` retains collection commands. Prior collectors need
explicit canonical `--output=parity/runs/<name>-verification.json` in adapted
mode: omitting it writes a run-local proof. The first record-results invocation
correctly rejected the unchanged older canonical proof; recollection with explicit
outputs and current-run verification passed without tool or source changes. Old
evidence is retained; new frozen closure is batch-36, never a rewrite of batch-35.
