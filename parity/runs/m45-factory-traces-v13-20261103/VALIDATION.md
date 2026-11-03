# Factory contract function traces

This milestone admits factory **contract CALL function callbacks** compiled by
SourceIndexingBuild. FactorySources, SourceCheckpoints and Live remain required;
the selected closure uses default multichain ordering with real PG and PGlite.
Account trace registration, arbitrary call types and a complete compile-time
function handler DSL are not accepted by this milestone.

The fixed Ponder 0.18.2 source at 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334
actually executes callTracer RPC flattening, buildEvents, decodeEvents, viem ABI
decoding, realtime reorg, SQL extraction/cold reopen and cache=false historical
prefix reconstruction. Native expectations come from that output, with the same
four contract callbacks and matched-frame checkpoint domain. The raw provider
fixtures are deep snapshots before source synchronization mutates transactions.

The ABI cases include overloaded run functions, tuple/address/uint256[] and bytes
arguments, zero arguments, zero/single/multiple results, bad input and bad output.
Malformed matched ABI events are omitted after consuming their raw checkpoint
indices. Wrong selectors, non-CALL frames and failed parent subtrees are excluded;
original traceAddress is preserved. Zero and missing provider value can still
match CALL. Source conversion represents a missing trace value as null.
Receipt status is not an additional trace filter: the source oracle includes
a non-error CALL with a reverted receipt to verify this independent rule.

Every real business handler compares ABI args/result, all selected trace fields,
transaction identity and its complete checkpoint against the source oracle before
writing SQL. The fixture covers birth-block calls before the discovery log,
membership after discovery ends, canonical/orphan birth replacement, cache on/off,
start1/start3 dependency-prefix recovery, historical number and live hash tracing,
reopen deduplication, handler rollback, default absent receipt with zero receipt
RPC, explicit receipt projection, trace and per-transaction receipt fallback retry.
PG application exit83 covers prepare/business/ack with both cache policies while
the PG server stays running. All ordinary and race child logs require independent
inspection; exit83 alone cannot certify absence of a data race.

The frozen selected plan contains612unique native leaves
(431pure,118PostgreSQL,63PGlite), plus219focused race repeats adding no unique
credit:831case executions in98groups. Compile-only checks add zero test credit.
All573v12 leaves are rerun, with39new trace supplements. Previous smoke or stage
passes transfer zero credit. Compared with v11,521selected leaves are shared,
1is omitted and91are additional to that selection. Compared with v10,333are
shared,248are unselected and279are additional. COVERAGE.json retains exact sets.
All source bytes, modes, commands, runner, patch and base/tree are archived per
group and must match one final fingerprint. At most two groups run in parallel,
with unchanged15-minute Go and1050-second outer budgets.

Evidence:
 /root/repo/ponder-go/m45-factory-traces-z4-20261103T020734Z-3g6cxuok
Base:
 597375f566fb75f24f651ea4b8e3f44cfcaa377a
Final receipts, independent audit and SHA256SUMS in that directory determine
acceptance; this document describes the planned scope and does not replace them.

Development history is preserved: the first oracle probe incorrectly expected
undefined for absent trace value; actual source events.ts:896 emits null.
The next probe corrects that assertion and adds receipt/frame independence.
The first native invocation failed before Go execution because tool metadata
was absent; its retry intentionally fails at the original factory trace guard.
The minimal production guard admits contract CALL only. Subsequent native
smokes verify actual acquisition, ABI callbacks, business rows and reopen/reorg.

Original mapping remains543/905, with362unimplemented and7runtime-passed records
still lacking type obligations. These native supplements and repeated race runs
add no original mapping credit. Other runtime modes/CLI, complete original
oracle/type mappings, PGlite abrupt exit, DB engine/server/power-loss crash and
full repository/full-race/M45 acceptance remain open. Prefix recovery still
requires archival provider access even when the ordinary cache is warm.
