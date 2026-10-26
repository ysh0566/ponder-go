# Managed finite historical execution

`historical.OpenManagedPostgres` and `historical.OpenManagedPGlite` opt into a
**new managed namespace format**. Existing unmanaged constructors and namespaces
are unchanged. Managed namespaces are not adopted by the legacy lifecycle, and
legacy namespaces are not automatically migrated into managed ones.

This API supports finite, default multichain history only. It does not implement
live cutover, realtime polling/reorg orchestration, or HTTP readiness serving.
Use `historical.ManagedRunner` for the outer-run/join/readiness guarantee. The
managed store and indexing constructors are advanced orchestration primitives:
their caller must seal admission and join descendants before `Activate`. They
retain the existing low-level backend access contract and are not a sandbox
against arbitrary caller-issued SQL or legacy administrative actions.

The original-source test accounting remains 543 / 905; managed tests are
supplemental and do not grant additional original-source credit.

## Identity and ownership

`ManagedOptions.BuildID` is the caller's version contract for handler code and
executable schema callbacks. Change it when that code changes; Go function
pointers cannot establish a stable code identity. The runner independently hashes
the executable plan after acquiring its exclusive executable session. It binds
chain order and configuration, filters, finite ranges, contracts/ABI and callback
names. Only chain `rpc` / `ws` endpoints, the exact executable `fn` fields, learned
`callback.filter.include`, and dynamic finalized heads are excluded. Unknown
semantic fields remain bound, and unsupported/cyclic values fail closed.
The store additionally binds schema, namespaces, ordering, chain endpoints and
optional alias namespace. Restart with a different identity is rejected.

PostgreSQL retains separate business and sync schemas, pools and fixed owning
sessions. A restricted control-pool lane renews the app heartbeat independently
of long business statements. Ownership checks bind session PID, backend start
and every retained advisory lock, as well as the app token. Defaults are a
10-second renewal interval and 25-second TTL, measured using database time.
Expired or lost ownership is not silently resurrected; historical transactions
recheck the managed lease before committing. This is cooperative application
ownership, not protection against a database administrator changing catalog or
rows arbitrarily.

PGlite retains separate persistent business and sync directories and processes.
The managed durable path rejects `memory://` and missing directories.
Its ownership contract is **actual filesystem exclusion and child-process
lifetime**, not source heartbeat expiry/takeover parity. A serialized long SQL
operation does not expire the filesystem owner. Heartbeat interval/TTL settings
do not permit takeover of a still-owned PGlite directory. Caller deadlines and
cancellation remain effective; `Close` timing out only ends that caller's wait.
The retained cleanup task keeps exclusion until unfinished children and storage
processes have actually exited. During PGlite business/activation SQL,
`Metadata` and `Ready` may return `store.ErrManagedFiniteBusy` instead of waiting
past the caller's deadline. A concurrent direct `ManagedFinite.Activate` also
returns this busy error; already-canceled callers retain their context error.

## Readiness and recovery

Opening does not publish readiness or create secondary activation indexes,
live-query triggers or optional alias views. Schema-defined base views and guards
remain part of initialization. Historical capture triggers are transaction-local
and removed before historical commit; durable capture starts during activation. `ManagedRunner.Run` is once per owner, including
failed attempts. A successful outer historical run must join extraction,
prefetch, cache and persistence descendants before activation. Activation seals
new indexing and escaped-client roots and joins already admitted client work.
Managed asynchronous cache-write failures and final prefetch lookup failures are
retained and prevent readiness, even if a handler already returned. Supported
speculative RPC failures may still use their normal successful fallback; merely
joining a queue is not treated as proof that its writes succeeded.
It then reads and fences the actual durable endpoint state.

Activation stages are secondary indexes, capture triggers, live-query triggers,
optional aliases, then durable ready. Each stage verifies exact owned catalog
objects and records its marker transactionally. A failed COMMIT acknowledgment
is not proof of rollback: readback may establish the committed stage, prove
absence, or leave a `store.ManagedStageError` with `Outcome == "unknown"`.
Do not blindly retry an unknown attempt on the same owner. Close and reopen;
unsupported or mismatched state fails closed. Initialization COMMIT uncertainty
is also reported as unknown rather than fabricated rollback.

Restart supports only a truly uninitialized empty namespace or the exact
configured chain set with an empty journal and equal safe, cursor, latest and
finalized checkpoints. Activation additionally requires exact finite endpoint
checkpoints. It does not apply legacy blanket undo to ambiguous states. Catalog
ownership/markers must agree before owned activation objects are reset for a
new run. A completed finite restart must not replay completed handlers/setup.

`Ready(ctx)` checks durable ready and current managed ownership, rather than an
in-memory completion flag. `Metadata(ctx)` exposes persisted metadata only; its
heartbeat fields must be interpreted according to the backend contract above.
