# Filter and fragment parity subset

`filter` provides native equivalents of the pinned runtime predicates, fragment
IDs/adjacency/recovery and log-request merge utilities. `Object` is an explicit
dynamic utility API, separate from the native typed config/schema API.
`Undefined` is distinct from JSON null. Fixture JSON uses tagged undefined and
non-finite values; `RPCValue` omits undefined object properties and maps undefined
array entries to null before an RPC request. Fixture tags must never reach RPC.

The M20 source audit is `parity/filter-fragments-source-cases.json`: all18
original fragments records and14 filter records (13 static cases plus the
actual genesis-block case). Their38 static assertion expressions instantiate75
runtime assertions. The source's factory-decode loop and inline snapshots remain
complete; loops do not become additional source test records. Generated traces
in `testdata/runtime/` retain inputs, call results, original expectations,
snapshots, source hashes, contexts and the exact block helper expression.

The unchanged original fragments suite runs successfully through Vitest. The
original filter suite still cannot collect because `_test/generated.js` is
missing; Foundry1.8.5 installation remains pending. Native filter fixtures use
existing Anvil0.2.0 on task-local loopback. This does not satisfy that original
suite's version/contract fixture prerequisite. No generated ABI, source import or
original assertion was substituted. Original deployment/mint/transaction/trace
and three range-request source records remain unmapped.

867 additional vectors execute actual pinned filter/fragments functions with
deterministic synthetic inputs, including undefined/null/empty/array addresses,
factory parents and intervals, lower-case compiled filters with mixed-case event
addresses, all predicates, native-transfer call types/value rejection, fragment
recovery and stable include unions. Range requests check80% inclusive overlap,
one-dimensional merges, factory thresholds and disjoint intervals. Four source
invalid-value exceptions require native rejection; JavaScript exception class
and message are retained in the vector but are not claimed as Go exception-type
compatibility. Extra dynamic invalid factory inputs are also retained. These
vectors are extra evidence, with zero original source credit.

The first vector run found undefined-topic recovery diverging from source;
`RecoverFilter` now follows source `recoverTopic` for dynamic undefined input.
It also exposed JS object string coercion for extra invalid nested factory
addresses; the explicit dynamic helper now returns the source's object string.
The first failing log remains available. Do not interpret dynamic acceptance as
a typed-native configuration contract.

## Runtime connection

Single-chain contract logs use the source predicate after RPC fetch. An explicit
empty address slice requires no logs; a nil slice is wildcard. More than50
addresses widens the one-block RPC request, then local address/topic/range
predicates discard the resulting superset. Discovery uses the factory predicate
and checks child birth-block provenance before event dispatch. Actual canonical
block hash is checked before normalized-log cache insertion.

Native scheduling discovers factories first and then requests child logs. This
uses separate calls rather than the source's fully coalesced history planner.
Real EVM regression fixtures verify greater-than50 address widening, extra
address/topic rejection, repeated Sync, same-transaction Created/Transfer events,
pre-discovery child exclusion, actual fork replacement and orphan-cache deletion
on both PostgreSQL and PGlite with cache enabled/disabled. Their8 leaves are
native boundary evidence, not original Foundry records.

The complete source config filter DSL, receipt/include event projection,
transaction/native-transfer/trace scheduling, full sync-store paging/planner and
multichain engine remain unfinished. Original `factory_log` decode behavior is
preserved even where its current underscore split is not a round trip; no silent
format correction or old-database adoption is implied.
