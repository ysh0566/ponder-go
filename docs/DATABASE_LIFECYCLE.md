# Native database lifecycle

`NewLifecycle` describes a staged database without touching it. `Migrate` creates
a fresh task-owned namespace, or acquires the lease of that same native managed
namespace. It rejects unmanaged schemas, incompatible fingerprints, metadata
versions and changed build IDs. It does not adopt an upstream Ponder database.
The build ID is explicit; automatic hashing of handler/config contents is still
the runtime toolchain's unfinished responsibility.

Application metadata uses the source's version `6` and fields for build ID,
table/view names, readiness, lock state and heartbeat milliseconds. It lives in
the real singleton `_ponder_go_app` relation, separately from the storage-format
ownership marker. Source per-table reorg relations have the native persisted
global journal as their explicit physical-storage adaptation. These are not
claims of raw old-layout compatibility.

Startup DDL and the initial app row share a transaction. Initial migration creates
tables, enums, views, permanent write guards and live-query procedures. Secondary
indexes, journal capture and live-query triggers are explicit later actions. A
failed startup leaves neither the indexing nor sync schema partially created.

## Lease and shutdown

Default heartbeat interval/timeout are the source's 10s/25s. A locked app causes
one wait until its observed expiration, then one retry. A renewed heartbeat makes
that retry fail with `MigrationError`. The wait respects its context. A clean
`Close` stops heartbeat and unlocks only the current token's app row; the backend
remains owned by the caller. Close lifecycle objects before closing the backend.

Each acquisition gets a persisted random fencing token. Business actions,
revert/finalize, sync block/interval/factory writes and RPC-cache writes check that
token and lease inside the namespace transaction lock. An expired owner cannot
invoke its handler or perform maintenance through these APIs. Its delayed
heartbeat/Close cannot renew or unlock the replacement owner. This fencing token
is a native safety addition; it is not an upstream database credential or a role
permission system. Direct backend privileges and full readonly-role setup remain
a separate unfinished track.

Lease age is refreshed at publication after migration work, so slow initial DDL
does not return an already-expired owner. PostgreSQL provides advisory transaction
locking and a row lock. PGlite tests share one existing instance; multiple
concurrent PGlite processes using the same data directory are unsupported.

## Recovery

Same-build restart requires the new finalized block for every persisted chain.
Its timestamp cannot precede the saved finalized checkpoint's timestamp. Recovery
returns each chain's saved safe checkpoint. In one transaction it drops journal
capture/live-query triggers and declared secondary indexes, reverses all retained
assigned and MAX journal entries, clears the journal, and publishes fresh locked,
not-ready metadata. Primary indexes and permanent write guards remain.

Latest/safe/finalized checkpoint rows are deliberately unchanged, matching the
source migration primitive. The caller must subsequently reset latest to the
returned safe checkpoints and install capture/triggers before replay. Fresh
namespaces need explicit checkpoint initialization. API steps are:

1. Construct lifecycle and call `Migrate` with an explicit build ID, chains and
   their corresponding finalized blocks.
2. Read persisted checkpoints; initialize fresh ones or reset recovered latest
   values with `Actions.AdvanceLatest`.
3. Install journal capture, indexes and live-query triggers with the Actions APIs.
4. Execute writes/checkpoint assignment/latest advancement in `RawBatch`, using
   its transaction-scoped Writer methods when they belong to one commit.
5. Mark ready and eventually close the lifecycle before its backend.

This API is not yet connected to the automatic runtime/migration scheduler.
Full historical-baseline semantics, dev reset, legacy/latest sync migrations,
views-schema aliases, permissions and complete original lifecycle tests remain.

## Verification

Eight unchanged-source records from `database/index.test.ts` map to 21 original
assertions. Their primary PostgreSQL mappings and seven applicable PGlite
companions preserve the catalog/metadata/error/timing checks. The original
active-lock failure test explicitly returns before its assertion on PGlite; no
companion pass is invented. See `parity/lifecycle-source-cases.json`.

Additional native checks cover all fenced write paths, old-owner shutdown,
rollback of failed recovery including DDL/indexes/triggers, startup cleanup,
finalized timestamps, canceled waiting and slow DDL. Eight real application
process exits cover first startup, partial undo, pre-commit metadata and
post-commit reopening on PostgreSQL and disk-backed PGlite. They do not establish
PostgreSQL-server or host power-loss behavior. The complete original oracle is
still blocked by the pending Foundry-generated contract fixture; source mappings
and native passes are reported separately from original oracle passes.
