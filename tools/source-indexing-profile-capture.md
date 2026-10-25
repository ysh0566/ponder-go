# M40 source capture contract

`source-indexing-profile-original.mjs` executes **unchanged whole files**
`indexing/profile.test.ts` (10) and `indexing/client.test.ts` (14), not selected
bodies copied into a harness. Only profile10 and the two prefetch originals are
targeted; remaining12 client originals do not earn native mapping credit.
`live.setup.ts` observes actual exported record/recover calls and preserves hint
reference identity with indexes. Source callback, chain, event data and field order
are serialized before calls, with tagged undefined/bigint. Live actions record
ordered setEvent/readContract/prefetch operations and eventCount snapshots. Raw RPC
boundary responses drive native transport; observed profile, return and physical
cache state are only assertions. Store writes already admitted are awaited only
for post-test physical observations. Original setup hooks, assertions, skips and
inputs are unchanged. The loopback startup adapter is explicitly environment
adaptation, not pristine execution.

The enabled-cache prefetch original starts at block2 and then sets block2 again;
there is one actual RPC, followed by a real persistent-cache prefetch hit. The
cache-disabled original makes two RPCs, including an unawaited prefetch future.
Neither test should be rewritten to force the alternative branch.

`source-indexing-profile-probes.test.ts` separately exercises actual source
profile and client code against **controlled RPC/store boundary doubles**, not
real RPC or real databases. Both backend-labelled suite environments run it,
but those labels do not turn its doubles into backend coverage. The original
suite provides that real database evidence. Supplemental inputs include initial
event and all actions; no profile/cache state is injected. Twenty-one profile
vectors and 21 client scenarios add zero source credit. Explicit promise gates,
not sleeps, establish prefetch return before future completion, independent
chunk and chain admission, aggregate-failure write deferral, and fail-fast
prefetch with continuing sibling effects. The pending-chunk probe waits for actual
second-chunk persistent insertion admission before releasing the first future;
RPC admission alone does not establish write-completion order. The earlier
race03 ordering failure and its pre-edit inputs remain preserved. Preserve source
quirks: global request dedupe precedes chain grouping, trace matches can produce
two arguments, noargs hints return before cache compatibility checking, profile
keys omit ABI/cache, multicall outer immutable option is not profiled, and encoded
empty prefetched responses can be persisted although ordinary raw empty results
are excluded.

All wrappers require explicit new output paths below
`parity/runs/indexing-profile-m40/`; fixture/collector outputs are explicit.
Run source/postgres and any legacy suite global cleanup inside the same
`with-postgres.sh` execution namespace. An initial controlled postgres probe run
passed both assertions but exited1 at global teardown because PostgreSQL was not
running; this failure remains `source-probes-postgres-01` and is not acceptance.
Probe initial_event and two extra field-order/stringification inputs were subsequently recorded explicitly; fresh captures are required after each added causal vector,
rather than relabelling old input results. Whole original24 source01 captures are
unchanged and accepted independently.
