# Native account events and normalized construction

`config.AccountSource` adds single-chain static/wildcard account addresses and
start/end bounds. Native handlers use `Name:transaction:from`,
`Name:transaction:to`, `Name:transfer:from` or `Name:transfer:to`. An empty explicit
address slice matches nothing; nil matches all. Factory account targets and the
complete original config DSL remain unfinished.

The existing native `runtime.Event` keeps its raw RPC Block/Transaction/Receipt
fields. Account events additionally expose `Trace`, `Transfer` and `Normalized`.
`Normalized` is the source-shaped `events.RawEvent` boundary, with bigint values
as `*big.Int`, lower-case protocol addresses, typed transaction/receipt status
and the full75-digit checkpoint. Native Transfer/Args contain from/to/value.
The raw native Receipt is available on account events; the normalized/public
projection obeys source receipt inclusion. These APIs do not claim arbitrary TS
source compatibility or complete contract-event projection.

`events.BuildEvents` accepts sorted normalized blocks/transactions/logs/traces
and callback filter metadata. It matches receipt by block+transaction index,
keeps the receipt object reference, rejects required missing receipts, excludes
reverted transactions, applies factory birth-block predicates and permits a log
without a matching transaction (the source zkSync boundary). A trace advances
its event index only when some callback matches; the index resets per block and
transaction. Multiple callbacks for one frame share that index. The final sort
is stable by the full checkpoint. Block events use the source end-of-transaction
sentinel. Five sync-to-internal conversion functions preserve optional/null
fields, large bigint quantities and legacy/2930/1559/deposit/unknown tx types.
`DecodePublicEvents` currently handles block and account events only and returns
an explicit error for contract events. Full source contract decoding/logging
counters and original generated-ABI trace fixtures remain unfinished.

## RPC trace capability

`rpc.TraceBlock` uses the original `debug_traceBlockByHash` method when available.
Only method-not-found (-32601) enables its explicit native per-transaction
`debug_traceTransaction` fallback. Both paths flatten callTracer trees, rebuild
traceAddress from sibling positions, and discard reverted frames with their
whole subtrees. Numeric `TraceBlockByNumber` bypasses immutable request caching.
The fallback checks transaction provenance against the requested canonical
block, and the runtime checks receipt block/hash/index before normalization.
The existing conservative request-cache rewind and normalized trace-table rewind
apply on reorg, including cold restarts and cache setting changes.

Actual capability probing on installed Anvil0.2.0 found block-by-hash and
block-by-number tracing unavailable, but transaction callTracer available. The
unchanged upstream transferEth helper requires block-by-hash; consequently its
three filter source declarations still fail before assertions. No helper method
or assertion was changed to make those original tests pass. Foundry1.8.5 remains
pending approval and was not installed.

## Source verification and adaptations

`parity/event-construction-source-cases.json` contains six exact source records
and12 original assertion expressions. Four event declarations run as unchanged
source text with actual upstream functions/setupCommon/helper implementations in
a selective Vitest runner; the original full events suite remains unable to
collect because of its unrelated generated ABI import. The common logger is
actual at execution and opaque only when serializing call inputs, since it is
cyclic. The source contexts, helper text/hashes and all captured call results
remain available. This is explicitly a selective source oracle, not a successful
full-suite collection or Foundry1.8.5 verification.

The complete original rpc/actions.test.ts runs unchanged:16 tests passed. Two
call-tree records are ported here using the same constants/frame trees/request
method/params and every path assertion through a native local HTTP mock. The
other14 original records are not counted as Go ports.119 source-generated event
vectors and four real-EVM backend leaves are additional evidence with zero
source-test credit. The native backend tests cover zero value, root and child
revert, nested CALL sibling positions, DELEGATECALL/CALLCODE exclusion, matched
trace numbering, receipt/status/checkpoint order, handler failure rollback,
actual fork replacement, cold disk/connection reopening and opposite cache
setting after restart. Application crash/journal coverage is inherited from the
existing full regression; no server/host power-loss guarantee is added.

Native runtime scheduling remains single-chain and per-block. Full trace
contract sources, factory account/trace targets, complete include/projection,
source history batching/profile/metrics, multichain and query/CLI work remain in
the feature ledger.

A follow-up four-vector regression covers deposit optional fees as undefined,
null, empty string and0x0. The source treats the first three as undefined;0x0
remains bigint zero. The first run exposed two native mismatches, then the fix
and full regression passed. See parity/runs/batch-20.json; source credit stays
unchanged.
