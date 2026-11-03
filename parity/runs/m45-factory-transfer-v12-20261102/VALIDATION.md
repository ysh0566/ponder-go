# Managed factory account transfers

This milestone admits opt-in FactorySources + SourceCheckpoints + Live account
factory transfer:from/to in the default multichain RuntimeRunner. Historical
debug_traceBlockByNumber and realtime debug_traceBlockByHash callTracer requests,
provider receipts, factory discovery, source SQL event selection and actual
business handlers execute through existing production acquisition paths.
Factory contract-function trace callbacks remain rejected.

The actual fixed Ponder oracle at 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334
executes trace RPC actions, realtime synchronization, SQL roundtrip/cold reopen,
cache=false historical acquisition and restart prefix reconstruction. It confirms
birth-block inclusion before the creation tx/log, membership after discovery ends,
canonical reorg membership, failed-frame subtree removal, zero/missing value
exclusion, DELEGATECALL/CALLCODE exclusion, from/to matching and original traceAddress.
Expected native checkpoints use only transfer callbacks: broad trace callbacks
change the matched-event index domain and are not mixed into this native oracle.
Provider input fixtures are deep snapshots before upstream mutation.

Both explicit receipt=true and the default receipt=false policies are tested.
Absent receipt remains source undefined, represented by filter.Undefined in Go.
Default tests require zero receipt RPC calls; other tests require actual receipt
acquisition and validate projection, complete checkpoint, traceAddress/from/to/
value/type and exact SQL rows. The controlled trace failure and per-transaction
receipt fallback failure verify Ready=false during retry and clean reopen.
PG application exit83 tests prepare/business/ack for each cache policy, while
its database server remains running. The external audit checks every ordinary
and race child log; application exit alone is not proof of no race.

Selected frozen validation plan:573 distinct native leaves
(426pure,98PostgreSQL,49PGlite), plus198 focused race repeats adding no unique
credit (771accepted case executions in74groups). Compile-only all-package and
race warmup groups add zero native test credit. Groups run at most two in parallel,
with original1050-second outer and15-minute Go budgets. Race PG groups are split
by lifecycle/reorg/failed handler, crash, acquisition fault and default policy;
cold race compilation is a separate zero-credit prerequisite. Exact Package/Test
names, command selections and baseline name provenance are in commands.json.
All results must match the final input's bytes, modes, tracked patch and base/tree;
earlier smoke passes do not transfer.

Compared with v11's522-leaf selection,521are rerun. Its negative transfer
capability leaf is removed because transfers are now accepted. Added52leaves
are38native transfer supplements and14existing trace/identity regressions.
Compared with v10's581selection,333are selected again and248remain unselected
(7pure,126PostgreSQL,115PGlite). The240other selected leaves are new to that
selection; these counts do not transfer passes across snapshots.
Adjacent V11-V10-COVERAGE.md/json retain the exact prior-stage difference.
The new evidence V12-COVERAGE-COMPARISON-EXPECTED.json retains exact sets for
this stage versus both predecessors. No original-source mapping is added.

Evidence and final receipts/seal:
/root/repo/ponder-go/m45-factory-transfer-z4-20261102T224328Z-xchx540q

Base:6d2b88ac6b6843ed4b5d650ec278a78207d2329b.
Original mapping remains543/905 and M45 remains unaccepted.
Single dual-endpoint registration, factory contract-function trace RuntimeRunner,
PGlite abrupt application exit, database-engine/server/power-loss crash,
other runtime modes/CLI, complete original upstream translations and full
repository/full-race acceptance remain separate. Prefix restoration still
requires archival RPC; a warm RPC cache does not remove that requirement.


The first full frozen attempt stopped after6groups because its test helper
required historical number traces even for a cache=false business start3 above
the initial finalized head2. Business rows, checkpoints and reopen had passed;
only that routing assertion failed. Such a prefix only needs factory discovery
logs, while eligible blocks3/4 use live hash trace acquisition. The helper now
requires historical traces when historical business blocks exist and continues
to require live traces, receipts when enabled, exact rows and checkpoints.
The original failed attempt, all raw artifacts and diagnosis remain intact.
A new full snapshot and v2-prefixed74-group plan reruns every selected group;
no prior result, including the six completed groups, transfers.
