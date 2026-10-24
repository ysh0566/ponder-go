# Original event and filter declarations

M25 adds the remaining six `runtime/events.test.ts` declarations and nine
`runtime/filter.test.ts` declarations:15 source records,38 original assertion
expressions, and one separate compiler-error obligation. Exact source, contexts,
inputs, assertions and native mappings are retained in
`parity/events-filter-completion-source-cases.json`. Focused race checks pass27
leaves. Complete verification exits0: normal Go and race each pass1740 leaves
with no failures/skips, across19 packages/126 top-level functions/46 disjoint
sequential batches per mode. Vet, CLI/example builds, codegen and compiler
fixtures, parameter types, retained SDK, applicable original oracles and all
supplementary differential checks pass. Frozen evidence is
`parity/runs/batch-24.json` and `batch-24-evidence/`.

The cumulative ledger is317/905 mapped and passed (313 Go plus4 retained SDK),
with588 static records unimplemented. These counts do not prove dynamic
discovery exhaustiveness or complete feature parity.

The fixture recorder runs each complete original file with its original
`globalSetup`, real deploy/mint/transfer helpers, generated ERC20/factory ABI and
unchanged Vitest assertions. Call-through spies record selected exported
functions; they return the actual original results and do not replace helpers,
RPC methods, trace responses or assertions. The original files pass11 and23
cases respectively. These instrumented captures are separate from the earlier
pristine original backend reports. Context, recipe, generated-ABI and raw-report
hashes are checked before accepting the captured fixtures.

Native tests replay every complete recorded call, compare full results and
post-call input state, and retain the original explicit expectations. These
include checksum versus lowercase addresses, amount/result values, malformed
ABI data, absent outputs, per-transaction trace indexes, complete checkpoints,
both DELEGATECALL/CALLCODE loop frames, address/topic unions and disjoint range
membership. The EVM setup executes in the original fixture recorder; Go consumes
those exact materialized inputs. Separate real Go EVM tests continue to check
PG/PGlite transaction rollback and same-hash fork replacement.

The no-output directive rejects `abiItem` access on the general source
`EventCallback` union before narrowing; it is not a readonly-ABI error. Native
`EventCallback` is a closed contract/account/block interface, with
`ContractCallback[abi.Event|abi.Function]` exposing ABI metadata after narrowing.
Actual runtime log/trace construction uses these descriptors. The positive
compiler fixture narrows the public union, copies the ABI and replaces outputs;
the original ABI stays intact. The negative union-level `ABIItem` assignment
fails with the specific missing-member diagnostic. Both use the real Go compiler.
This verifies that property boundary; dynamic filter/metadata fields, callback
function types and the legacy Object API do not claim full TS type equivalence.

A separate native regression caught account projection rounding a supplied JSON
integer9007199254740993 to9007199254740992. Log and account projections now share
`json.Decoder.UseNumber`. Hex quantities, raw handler input and each path's
existing default-field behavior are preserved. The fixed-code red result is one
pass/one failure; focused race green is8/8. These regressions add no source credit.

Factory account targets, full source configuration/include/history planning,
multichain scheduling, complete queries/API/CLI and remaining type/simulation
work continue in the feature ledger. Complete event/filter test mapping does
not make the overall port complete or prove runtime discovery exhaustiveness.
