# Runtime historical original capture

The original full `runtime/historical.test.ts` runs unchanged through the approved
loopback Anvil startup adapter. All 14 cases/29 assertions are executed, but only
the first 11 cases/24 assertions are M36 native candidates. The final three are
explicitly unimplemented; the original “omnichain” title actually calls the
single-chain Multichain entrypoint and does not prove multi-chain behavior.

`source-runtime-historical-original.mjs` requires an explicit backend and new
scoped output directory. `source-runtime-historical-fixture.mjs` accepts the two
complete source directories and prepares the native fixture only after checking
source declarations, actual calls, captures, context hashes and initially empty
physical tables. Later nonempty tables must be reproduced by executing preceding
calls in the same case, never imported from recorded physical rows.

Each targeted call records `operation`, identity-marked `args`, raw `providers`,
consumer `steps`, all iterator `results` including terminal done, `event_builds`,
`after`, and physical rows before/after. Marker encoding is shared with previous
historical/index capture: `$id/$ref/$object/$map/$set/$bigint/$undefined` and
`$number`. Logger/function values in raw request context are explicitly opaque
`$function` names; they do not become native callbacks. Inputs include actual
progress/cache helper outputs and callback filter references. Returned events,
BuildEvents inputs/results and physical rows are verification targets only.

The single per-case RPC observer routes requests only while the actual generator
is consumed. It preserves RPC object identity and suppresses duplicate wrapper
observations when later original spies wrap the same request method. Physical
snapshots add read-only queries outside the generator; they do not alter inputs,
returns, assertions, or database writes.

## Supplemental scheduling-order probe

`source-runtime-historical-order.test.ts` runs actual source and real databases
with 27 mined blocks and a 300 ms first-block-data delay. Both backends show the range phase for
26–27 starts before range 0–25 completes its QB transaction, with second
block-data processing starting only after that first QB completion. The block-only callback has no range RPC, so this proves phase ordering only,
not actual RPC prefetch. This has zero source credit. The historical event label `transaction_committed` means the QB
Promise resolved. PostgreSQL uses a real SQL transaction; pinned PGlite QB calls
`callback(db)` without a SQL transaction. `order-interpretation.json` binds the
queryBuilder source and explains this difference. The probe is not rollback or
cancellation evidence, and its event label must not be read as a PGlite COMMIT.

Failed/previous captures are retained separately; do not rewrite their context
roots or hashes. Current results are environment-adapted, not pristine.

## Source error and early-return boundary

Static inspection of pinned historical.ts lines 1624–1655 finds a detached async
interval loop calling `syncInterval(...).then(successHandler)` without a rejection
handler. Its result-consumer loop has no `try/finally` to cancel or join the
producer on early return, and the interval generator is not closed on completion.
`error-return-static-review.json` preserves exact source hash and excerpt. No
intentional hang/error probe was run. Native error propagation and cancellation
with joined work are safety extensions, not claimed exact source failure behavior.

The additional `source-runtime-historical-log-order` probe uses block and ERC20 log
callbacks. On both actual backends it explicitly asserts an `eth_getLogs` request
for 26–27 starts before the first QB transaction resolves, while second block-data
processing starts after it. Its events use the precise `transaction_resolved`
label and include `transaction_mode`. The M36 aggregate reruns this actual-RPC
probe for both backends before freezing native inputs.

## Aggregate acceptance

`runtime-historical-full-verify.mjs` preserves all previous module, original,
compiler and SDK checks, then adds both complete historical-file captures, both
actual-RPC order probes and fixture assembly before the native-input snapshot.
There are 28 mandatory stage commands, including source-metadata negative controls. Existing M35 source capture and proof
collector path guards admit only the explicit M35 or M36 directories; no arbitrary
output root is permitted and old reports are not rewritten.

The new result collector requires all 28 stages, current source/harness/context
hashes, both full original captures, normal/race full-module executions and all
four real-backend candidate runs. The mapper uses the existing newline-joined
original assertion text digest and refuses credit for the final three cases.
Neither tool has been invoked to add source credit during implementation.
