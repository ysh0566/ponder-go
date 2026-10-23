# Contract decoding and native trace scheduling

`events.Decoder.DecodeEvents` implements the contract log and call-trace branches
of the pinned source, alongside existing block/account projection. It uses the
native event decoder for logs (lower-case decoded addresses), and viem-shaped
function arguments/results for traces (checksum addresses, named tuples,
arrays, absent zero-argument/result values and single-result unwrapping).
Matched ABI data that fails decoding is omitted and counted. Debug messages are
deduplicated by event/function selector within one batch, then a failure/success
summary is emitted for each affected kind. `DecodeStats` exposes the counters;
`Options.Logger` can receive the same structured fields from native trace blocks.
The older `DecodePublicEvents` remains the explicit block/account-only API.

`abi.Function` supplies canonical signatures/selectors and source safe handler
names. A unique function uses `C.transfer()`. An overloaded function uses the
formatted ABI item, including named parameters, mutability and returns, matching
source `toSafeName`. `ABI.FunctionEntries` retains ABI order, duplicate entries
and selector collisions; runtime registration deduplicates handler names rather
than discarding functions that share a selector. `ABI.Functions` remains a
convenience lookup by selector and can represent only one entry per selector.

`config.Contract.IncludeCallTraces` enables native contract traces. Registered
contract and account trace callbacks share one BuildEvents call, so unmatched
frames do not advance the trace event index and all callbacks matched by the
same frame share its index. Static/wildcard and existing contract factory targets
use the same address/birth-block predicates. Native handlers receive Args,
Result, Trace and Normalized; raw native Receipt follows the contract receipt
option. Every handler for the block remains inside the existing staged
business/journal/checkpoint transaction.

The actual-EVM fixtures validate zero-value matched contract calls, uint256
argument/result decoding, shared contract/account trace indexes, skipped
unmatched/reverted frames, handler rollback and fork replacement with an
identical transaction hash but different execution output. Explicit nonce and
gas price make that last fixture actually reuse the hash. Cache-enabled and
uncached PostgreSQL/PGlite runs verify old trace/receipt RPC results do not
survive canonical rewind. Invalid replacement output is logged/omitted once,
and repeating Sync does not log or write it again.

## Parity ownership and limits

The additional decoder/name vectors invoke actual pinned source functions with
fresh synthetic ABI items. They cover overloads, named tuple/array, multi-result,
no-result, malformed input/output, log failures and selector dedup/summary fields.
They are supplementary, not replacements for original generated ERC20 ABI tests.
Those original contract declarations are still unmapped until their exact
fixtures can run. Installed Anvil0.2 transaction-tracer fallback remains explicitly
native compatibility; it does not pass the three original ETH helper declarations
that require the absent block-hash tracing method. Foundry1.8.5 is still pending.

The legacy native contract-log scheduling path still decodes through ABI.DecodeEvent
and propagates a decode error; it has not yet been routed through the new complete
batch decoder. Consequently full shared log/trace batch error-log ordering, full
source event include/projection, generated-ABI original trace cases and complete
factory/account/multichain integration remain pending. The generic Decoder API
and connected contract-trace path are the verified scope here.

The RPC action additions provide chunk50 GetLogs and source-style log/transaction
normalization, defaults, duplicate/bound validation and ordering. Native chunks
are sequential while source uses Promise.all; the four mapped source cases retain
all result/request/input assertions. Numeric GetLogs requests bypass immutable
response caching. The main block-hash runtime planner retains its existing
cache-aware log fetch path; generalized historical pagination/receipt actions
and full standardizer integration remain pending. Provider data errors and
permanent remote RPC errors are distinct, with supplementary retry/cache checks.

Fifteen earlier filter/genesis/split source declarations now have their own
successful selective oracle. Exact statements, helper/constant declarations and
all assertions are retained; only the task-loopback endpoint is adapted. Initial
runner failures due to omitted REQUEST_* declarations are preserved and were
fixed by including the exact source declarations. Full original suite collection
status stays separate from these selective results.
