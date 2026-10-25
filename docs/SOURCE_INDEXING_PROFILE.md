# Source RPC profiling and predictive prefetch (M40 verified subset)

The bounded target is the ten unchanged `indexing/profile.test.ts` declarations
(21 assertions) and two prefetch declarations in `indexing/client.test.ts`
(9 assertions), zero TypeScript directives. Both full original files run unchanged
on PostgreSQL and PGlite: 24 cases total, only 12 newly accepted mappings. The other
12 client raw-transport/action originals remain unmapped. Prior static gaps remain.
Publication remains pending parent review; no full cachedViemClient claim follows.

## Actual implementation

`indexing/profile.go` records/reconstructs RPC patterns independently of existing
`store/profile.go` database-row profiling. It preserves event derivation priority,
primitive comparison, actual hint identity, opaque ABI input metadata, function
and immutable-cache metadata. The profile key includes address/function/args,
not ABI or cache. Object/null call arguments reject profiling; undefined is a
source primitive and can derive from an absent required event field. Log/trace
array event arguments are not searched. Trace matches in both args and result
can append two argument paths; a result address match overwrites an args match.
No-argument matching hints return before the immutable-policy comparison.

ClientSet samples when the caller-owned successful event count modulo ten is one,
not during setup or explicit-block actions. ReadContract records on each retry;
multicall records fewer than ten child contracts, not the outer immutable option.
Per-event ordered patterns retain count and an independently refreshed constant
LRU of ten. Prediction score is count*10/eventCount, with strict DB >0.2 and RPC
>0.8 cutoffs. Actual event, chain and block recover into ABI-encoded requests.

Canonical request deduplication is deliberately before chain grouping and excludes
chain ID: the first matching prediction wins, even across chains or differing
scores. This is the pinned source quirk, not an improved multichain algorithm.
Per-chain ephemeral entries contain DB values or admitted, unawaited RPC futures.
Prefetch completion is not RPC completion; disabling persistent caching still
warms RPC. Clear removes ephemeral entries but preserves patterns/LRU and does
not cancel outstanding work. Close owns cancellation/join; WaitPrefetch is an
explicit native lifecycle helper, separate from WaitCacheWrites.

Single-call future rejection is thrown; multicall ignores a rejected prefetched
child and falls through to eventual transport. The source compares serialized
future results against raw empty sentinels: prefetched JSON `"0x"` and `null`
therefore persist even though ordinary responses exclude them. This quirk is
preserved and directly probed, not silently normalized.

## Integration and boundaries

The real Service supplies raw event bindings, successful dispatch counts and
optional insertion-order metadata, rather than precomputed predictions. Historical
profiling bypasses property-view instrumentation; subsequent realtime dispatch
can consume recovered future-event requests. Explicit clients set event/counts
through SetEvent/SetEventCounts. Counts are snapshots, not an implicitly shared
mutable Go map. The caller must finish dispatch before changing event context.

Go field-order metadata is required to reproduce JavaScript args/result insertion
order; integer keys come first. Without metadata, deterministic alphabetic order
is a native facade, not JavaScript insertion-order equivalence. ABIValue carries
opaque raw ABI input independently of executable parsed ABI. Malformed recovered
addresses require strings in this typed facade; arbitrary JS TypeError text and
arbitrary object coercion are not claimed equivalent.

Strict standalone AsyncCache prepared write admission remains the M39 boundary.
The optional `rpc.AsyncCacheReader` adds genuine synchronous admission of validated,
snapshotted SELECT jobs to the existing standalone adapter’s owned read queue.
All configured chains’ reads are admitted before waiting for a lookup result;
per-chain completion jobs are independent. No goroutine-start handshake substitutes
for read admission, and a blocked first-chain lookup does not impose completion
on another chain. The first lookup error rejects Prefetch without canceling
sibling jobs, as with source Promise.all; ClientSet retains ownership, and Close
cancels and joins those jobs. Synchronous-only custom cache adapters retain weaker
invocation-prefix/order guarantees. Native reads are per key, not source batch
invocation grouping; the source whole-chain lookup completion boundary is retained
before RPC misses are admitted. This adds no database pool/schema topology, and
transaction-bound adapters do not gain detached readers. Database read-visibility
and commit-order equivalence remain unclaimed. Synchronous custom RPC
fallback has weaker invocation-prefix timing than real AsyncRequester admission.
Existing PGlite shared-lock and in-flight cancellation constraints, source/native
cache insertion grouping differences, ABI anonymous tuple limitations and full
Viem error wrapper/custom-error decoding gaps remain. There is no outer runner,
readiness, cutover, topology change, backup or historical schema cleanup here.

## Evidence contract

Original inputs and actual source raw RPC tapes drive native replay; expected
returns/rows never seed execution. Actual standalone PG/PGlite cache rows and
original spy assertions are checked. Supplemental real source execution uses
explicit controlled RPC/database doubles for 21 profile edge vectors and 21 causal
client scenarios; it is not represented as real network/database execution and
adds no original credit. A separate real-store Service integration tests counts,
derivation, next-block prediction and realtime consumption on both backends.

The aggregate retains all 54 M39 stages and adds eight stages (62 total). All
normal/race tests, old/new source oracles, types, SDK/React and eleven explicit
canonical collectors must accept the same frozen input/harness fingerprints.
Failed and superseded focused captures remain evidence, not successful proof.

## Accepted execution

`parity/runs/indexing-profile-m40/full-verify-01` completed all 62 stages at
2026-10-25T15:13:03.642Z. All eleven explicit-output canonical collectors accepted.
Normal/race each passed 3,286 test nodes / 2,994 leaves with no test failure/skip.
The 1,139-file native fingerprint was
`10119df4147f45fbddb57e60ee75925bef14899a6f21468f3ab5831d166f8d00`;
94 harness hashes remained unchanged. Exact mapping/manifest/ledger commands are
in `full-verify-01/ledger-closeout.log`. Coverage is 543/905 (539 Go + 4 SDK),
362 pending and seven unchanged runtime-only static gaps. The twelve new mappings
carry thirty original assertions and no static directives. New batch 39 freezes
current and failed/focused evidence without changing published batch 38.

Semantic, pipeline and asynchronous-reader ownership reviews cleared the final
source05 inputs before the full run. The pending-chunk source barrier waits for
two actual cache writes, not merely RPC admission. Counterfactual overlays of
pre-review code fail all four added causal controls; current code passes them.
Earlier source/focused failures remain diagnostic evidence, never pass credit.
