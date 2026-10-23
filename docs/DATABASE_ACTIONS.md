# Database actions and durable boundaries

The fixed upstream database actions use staged persistence. The native atomic
runtime remains available through `Store.Apply`; `NewActions` supplies the source
action lifecycle. A namespace fingerprint includes the write mode and refuses
opening a staged namespace as an atomic store, or the reverse.

`Actions.RawBatch` commits business writes and their trigger journal together.
Every captured operation has the 75-character MAX checkpoint until `CommitBlock`
assigns a checkpoint. The journal also persists the operation kind and original
row data: NEW for insert, OLD for update/delete. `Journal` reads those persisted
values and returns source-shaped rows; the physical storage is a native global
journal, not an adoption of the original per-table reorg layout.

| Step | Durable business state | Journal | Latest checkpoint |
| --- | --- | --- | --- |
| Before RawBatch commit | Previous state | Previous state | Previous value |
| After RawBatch | New state | MAX pending entries | Previous value |
| After separate CommitBlock | New state | Assigned checkpoint | Previous value |
| After AdvanceLatest | New state | Assigned checkpoint | New value |
| After Finalize | New state | Safe undo prefix removed | Preserved |

For isolated ordering, CommitBlock updates only pending entries for the target
chain. A commit for a different chain leaves its MAX sentinel untouched. Default
multichain rollback still removes the global operation suffix beginning at the
first affected target-chain operation; it does not merely undo that chain.

The same-transaction alternative is explicit: `Writer.CommitBlock` and
`Writer.AdvanceLatest` can run inside a RawBatch callback. An exit before the outer
commit then rolls back business, checkpoint assignment and latest together. A
write after advancing latest cannot leave a newly pending journal at commit.
Actions methods open their own transactions and belong outside that callback.

Initialize chain checkpoint metadata before nonempty finalization. Finalization
and ordinary revert check their pending precondition while holding the same
namespace transaction lock. Isolated checks only the target chain. Empty-schema
finalization follows the source's special branch: update an existing matching
chain's safe/finalized values, leave latest and other chains unchanged, and
succeed when no checkpoint row exists.

CrashRecovery restores all retained undo records, including assigned checkpoints
and MAX sentinels. It intentionally leaves checkpoint metadata unchanged, just as
the source primitive does. The subsequent runtime migration step must reset
latest to safe with AdvanceLatest. If interrupted during undo, recovery can be
retried; its data and journal changes share one transaction. RawBatch retry after
an uncertain durable outcome must account for this recovery boundary.

Capture triggers can be dropped for maintenance. A permanent BEFORE guard stays
installed and rejects ordinary business writes without an active capture trigger.
Only controlled internal undo can write under the reverting context. Dropping
capture triggers never creates a public path for unjournaled DML or TRUNCATE.

Storage format is `json-v3`; default namespaces are
`ponder_go_shadow_v3_<hash>` or `ponder_go_actions_v3_<hash>`. Existing earlier
formats require a new shadow. No in-place journal conversion or old database
takeover is implemented. Lossless JSON before-images and raw numeric values from
the v2 implementation remain preserved.

Real process-exit tests cover raw transaction rollback, same-transaction block
assignment rollback, durable MAX, assigned block, latest, finalization and an
interrupted undo on PostgreSQL and PGlite. These are application-process tests;
they do not establish server/host power-loss behavior.

The automatic runtime/migration scheduler still uses the atomic path. Full staged
runtime wiring and the source's large-payload LISTEN/NOTIFY action case remain
unfinished. See the source test ledger and continuation document for exact scope.
