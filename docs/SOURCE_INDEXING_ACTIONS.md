# Bounded indexing client actions (M39)

This milestone targets the last eight unchanged `indexing/index.test.ts`
declarations: getBalance, getCode, getStorageAt, three readContract variants,
and two multicall variants. They contain nine assertions and zero TypeScript
error directives. The entire original 23-case file runs on PostgreSQL and PGlite;
only these eight additional cases can earn new credit after complete acceptance.
No verification or publication is implied by this design document.

## Actual action boundary

Original ABI, address, args, explicit/immutable options and dispatched event seed
native execution. Actual source RPC request/response tapes supply the transport
boundary; native code performs ABI encoding/selection/decoding, block selection,
cache lookup/write and miss-only aggregate3 batching. Observed source return
values and physical cache rows are assertions, never initial state. Duplicate
calls preserve order. Successful nonempty child results are persisted separately.
Real source supplemental vectors cover partial hits, all hits, duplicates,
ordering, failure modes, immutable reads and uncached empty code on both databases.
Typed contract-revert data/status and zero-data causes are compared; full Viem
wrapper/custom-error decoding/messages are not claimed byte-equivalent.
Empty-data retry uses ten total attempts and 125 ms exponential backoff, including
the source returned-no-data message predicate; other not-found actions are outside
this facade. Multicall chunks use real BeginCall admission and all-settled
completion when the requester exposes rpc.AsyncRequester; synchronous-only custom
requesters have weaker invocation-prefix timing. Existing ABI.Parse anonymous
tuple encoding limitations remain; decoding preserves unnamed tuple metadata.
Source batched sync-store insert grouping and native per-child calls are not
claimed invocation-grouping equivalent. Supplemental controls add no original-case credit.

## Narrow cache admission guarantee

`NewStrictClientSet` requires the rpc.AsyncCache capability, not a concrete-type
check. The production implementation is the standalone async source-RPC adapter.
Its BeginCachePut path snapshots input, validates and prepares rows, and admits
work into its owned queue synchronously before returning a completion future.
This closes only the **strict standalone prepared-admission prefix** gap. It is
not a goroutine-start handshake and does not imply SQL completion before return.
Owned work is drained/canceled by the documented client/adapter lifecycle.

Transaction-bound Writer and SourceSyncStore adapters remain synchronous and
reject async construction; they do not acquire a detached transaction writer.
Source persistent sync cache normally commits independently of a business
rollback. `NewClientSet` permits synchronous-only custom adapters and retains
weaker invocation-prefix timing. No broad transaction or scheduling parity is
claimed from strict standalone admission.

Source-versus-native **concurrent cache-read visibility is not established**.
Native Store mutex serialization blocks a read behind an executing write. M38's
synthetic held-completion probe is separate evidence, not an actual delayed
source database insertion/concurrent-get comparison. No commit-order equivalence
or full concurrency parity is claimed. PGlite cross-Store cancellation retains
its underlying limitations: Backend.Begin waits for the shared backend mutex
without context-aware acquisition, in addition to in-flight IO constraints.

## Reflection and remaining scope

PropertyView queries a separate target's own string-property descriptors and
filters virtual own keys by actual enumerability. All 23 source reflection
operations, including Object.keys, have native assertions. There is no public
DefineOwnProperty API or claim for arbitrary symbol descriptor behavior; ordinary
Go maps are not transparent JavaScript proxies.

The three M38 TS-only mock-property/delete obligations remain unmapped, as do the
four earlier runtime-index static gaps. No unrelated Go negative compile tests
substitute. Full cachedViemClient profiling, prefetch/LRU, all of
`indexing/client.test.ts`, account/state overrides and chain deployment discovery
are outside this bounded facade. The outer runner, readiness/cutover/triggers
remain separate unfinished work.

## Acceptance contract

The M39 aggregate retains all 46 M38 stages and adds eight current source actions,
probe, fixture and evidence stages (54 total). Complete normal/race module runs,
old/new source oracles, type controls and SDK client/React checks must pass with
fresh input and harness fingerprints. Ten canonical collectors use explicit
outputs, and exact original mappings are added only after their proof is accepted.
Old batch-37 and all failed run records must remain preserved.

## Accepted local execution (publication pending)

`parity/runs/indexing-actions-m39/full-verify-02` completed all 54 stages at
2026-10-25 13:39:39 UTC. Ten explicit canonical collectors accepted it. Both full
native modes passed 3,146 nodes / 2,870 leaves, with zero failures or skips;
PostgreSQL/PGlite each cover 450/446 backend-labelled leaves. There are 23 packages,
245 top-level functions and 76 disjoint batches per mode. All 1,126 native inputs
and 77 harness hashes remained unchanged. Exactly eight mappings were added to
the previous 523, giving 531/905 formal coverage; seven old static gaps remain.

The first complete diagnostic run is retained. Only two older supplemental cache
key-envelope expectations failed in each mode because they preserved checksum
case. Their raw source fixture and separate exact RPC comparison were unchanged;
independent expected-key normalization plus a literal mixed-case control passed
review and focused tests before the fresh full02. Its 1,215 pre-edit inputs are
archived under full01/failure-inputs. No failed evidence is relabelled successful.
New batch-38 preserves batch-37; parent review and publication are still pending.
