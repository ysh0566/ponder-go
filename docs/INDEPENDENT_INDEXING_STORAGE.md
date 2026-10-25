# Independently owned sync storage and reusable indexing bindings (M41)

M42 now adds bounded atomic setup/group primitives on this foundation; see [MULTICHAIN_ATOMIC_PRIMITIVES.md](MULTICHAIN_ATOMIC_PRIMITIVES.md). The exclusions below describe the M41 proof itself; the outer runner/readiness/cutover remain unfinished.

This stage supplies storage ownership and finite transaction bindings. It is **not** the outer historical or realtime runner, atomic multichain setup/checkpoint initialization, readiness, trigger cutover, or automatic recovery coordination. Existing single-chain runtime restrictions remain. New integration tests/probes are supplemental, not additional original-source test credit.

## Source anchors and the intentional native difference

All source paths below refer to pinned Ponder `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`:

- `packages/core/src/runtime/multichain.ts:93–105` creates the sync store from `database.syncQB`, then creates persistent cached clients. Lines 122–144 create the persistent indexing cache/store/service.
- `runtime/multichain.ts:253–264` binds the indexing store/cache to the setup transaction's `userQB` transaction. Lines 353–360 prefetch rows through `database.userQB` outside the following business transaction; lines 364–375 bind the same indexing store/cache to that next transaction.
- `packages/core/src/database/index.ts:249–268` constructs PGlite sync and user query builders over the **same** PGlite instance.
- `packages/core/src/database/queryBuilder.ts:462–503` uses genuine wrapped PostgreSQL transactions, but the PGlite branch invokes `callback(db)` rather than opening the underlying SQL transaction.

Native `Actions.transaction` holds its business `Store.mu` across the handler and commit. A standalone cache on that Store reacquires the mutex; a second Store over the same PGlite backend still contends with the backend transaction lock. Releasing the SQL transaction around handlers would not preserve native writes: delete/direct SQL can execute immediately, and a lease check is not row-version conflict validation.

The approved solution therefore uses separate native storage domains. PostgreSQL has independent connection pools and distinct namespace families. PGlite has two processes and two persistent directories (or two independent memory instances). Native business writes retain **real SQL rollback**, while sync-cache transactions can commit independently. The PGlite layout and failure atomicity intentionally differ from upstream; neither source-equivalent directory layout nor source-equivalent rollback behavior is claimed.

## Construction and storage identity

`store.IndependentStorageOptions` names the schema, ordering, explicit business/sync namespace roots, and either a PostgreSQL URL or business/sync PGlite directories. `OpenIndependentPostgres` and `OpenIndependentPGlite` own both backends; they do not accept opaque backend pairs as evidence of isolation.

Validation precedes DDL or PGlite startup:

- Both namespace roots must satisfy native ownership naming rules. The complete currently derived families (`root`, `_sync`, `_legacy`, `_rpc_1`) must not intersect.
- PostgreSQL compares the exact server-side `hashtextextended(...,0)` identities used by Store and sync-migration advisory locks across domains before initialization. Each domain is opened through a distinct pool, then pinned to its own dedicated, non-reconnecting SQL connection. Cooperative lifetime session advisory locks reserve every namespace in each domain’s family. All of that domain’s SQL runs on the same connection that holds its locks: losing the connection fails new work rather than reconnecting with stale cached rows. If only one connection dies, the other domain remains owned until it is closed. Owned SQL parses every statement and rejects direct advisory-unlock function calls, including qualified/quoted/escaped names; innocent literals and declared identifiers remain allowed. This is a lifecycle safeguard for trusted indexing SQL, not a security sandbox against hostile custom server functions or raw administration. Ordinary legacy PostgreSQL backends keep their previous behavior.
- Persistent PGlite paths are absolute/canonical, including symlinks through existing ancestors when the final directory does not exist yet. Equal, nested, aliased, non-directory, dangling-symlink and filesystem-root paths are rejected. Empty/`memory://` paths denote independent memory instances.
- Process-local reservations plus a fixed per-user `/tmp/ponder-go-independent-owners-<uid>` flock registry reject equal/nested active canonical paths across cooperating processes. Directory-inode flocks also detect aliases without placing a file in an otherwise empty PGlite data directory. Registry records are retained; unlocked records are not ownership. Locks remain held through normal backend/process close. These locks do not govern legacy/raw APIs or arbitrary other processes that ignore the ownership protocol, and do not protect against hostile filesystem replacement/removal. Both Node children inherit all lifetime-lock file descriptors through a private `openPGlite` helper. After abrupt Go-owner death, the same open-file-description locks remain held until both children exit, covering the orphan-child EOF/in-flight shutdown window. A controlled process test stops both Node children, kills the Go parent, verifies replacement rejection, resumes the children and verifies eventual release/reopen. This is process-lifetime exclusion, not a host-power-loss or filesystem-durability guarantee.

No lock file is inserted into the PGlite data directory itself: initdb requires a fresh empty directory. A successful PGlite owner has a private lifetime context, not the short initialization/request context. The temporary initialization-cancellation watcher is stopped/joined before returning. Legacy `OpenPGlite` retains its old context-lifetime semantics.

Failed initialization closes acquired resources and releases reservations; it never deletes, vacuums, resets or adopts existing data. Initialization across the two domains is not an atomic distributed transaction: newly created owned schemas/directories may remain after a later initialization error. Use fresh task-owned names when investigating such failures; this API does not authorize cleanup of unrelated namespaces.

The low-level storage owner exposes `BusinessActions()`, `BusinessStore()` and `SyncCache()`, but no sync-domain business Store/backend. Both PostgreSQL domain connections are fenced independently, including low-level sync/prune access. Raw business handles are an explicit lower-level ownership escape: their caller must join their work before low-level storage shutdown and coordinate session invalidation. The higher-level `indexing.Owned` keeps its storage private.

## Stable service, immutable transaction capabilities

`indexing.OpenOwnedPostgres` / `OpenOwnedPGlite` accept `OwnedOptions{Storage, Service, Requesters, Cache}`. They reject supplied Service database/client objects and construct strict clients from their actual owned standalone adapters, including both asynchronous read and write capabilities. An interface check alone is not the topology proof.

`Owned.Service()`, `Database()` and `Clients()` retain their identities. Handler arguments/context and per-chain clients are persistent service objects. `Owned.Batch(ctx, chain, historical, callback)` admits one business binding at a time; `historical` selects `Actions.HistoricalBatch`, not a history runner. The callback receives a capability-bearing context, the stable service and the immutable current Writer. Callers may dispatch setup/history handlers and use Writer checkpoint primitives within that same transaction.

The lower-level components are:

- `Actions.NewIndexingSession` / `store.NewIndexingSession`: persistent row-cache/profile ownership for sequential transaction scopes.
- `indexing.NewSessionDatabase`: stable facade, constructible while unbound. Its `Underlying()` returns nil; it does not expose a pointer that silently changes transactions.
- `Database.Bind(ctx, writer)`: validates owner and exclusivity, creates a fresh immutable store/cache binding and embeds a private capability in a derived context.
- `DatabaseBinding.Revoke()`: rejects later calls and joins already-admitted database operations. It revokes database authority **without cancelling the context**.
- `DatabaseBinding.Finish(actualTransactionError)`: must run after the enclosing SQL transaction settles, not merely after the handler returns. `Owned.Batch` performs this bookkeeping.

Unbound, wrong-owner, concurrent, stale-context and escaped old-handle use is rejected. A previous callback's context is not valid during a subsequent binding. Do not retain callback goroutines past return; callbacks own and must join their work. Caller cancellation/deadlines still propagate normally.

Successful commit retains a copied row-cache snapshot for the next immutable binding. Rollback, flush failure, uncertain commit and incomplete/panicking scope conservatively discard rows, negative predictions and write buffers, while retaining learned profiles. External writes/reverts require `IndexingSession.Invalidate()` before reuse; it refuses to race an active binding. There is no automatic subscription to arbitrary external SQL or another process's writes. The current high-level owner exposes no external-revert API; future recovery integration must explicitly coordinate invalidation rather than bypass this rule.

`Database.WithReadOnly(ctx, chain, callback)` is the explicit outside-business-batch row-prefetch/read binding. `Database.Prefetch` requires that binding or an active business binding; it does not silently open a writer. Read-only bindings set a real SQL `READ ONLY` transaction, reject buffered mutations and non-read-only raw SQL, revoke escaped handles and participate in the owner's admission/shutdown gate. `SetProfileEventContext` validates the same capability; context-free `SetProfileEvent` remains only for legacy fixed database adapters.

## Async cache work and shutdown

Normal commit or rollback revokes only database access. It does **not** cancel the derived callback context: already-admitted independent cache writes and lookup→RPC prefetch work remain eligible to complete, subject to the original caller's cancellation/deadline. A cache result committed before business rollback stays committed; an admitted pending write is not yet a durable result.

`Owned.Close(ctx)` follows an explicit mixed policy:

1. Seal new business/read-only bindings and let the admitted binding finish.
2. Close clients, cancelling and joining their lookup/RPC-prefetch futures and cache-write completions.
3. Seal/cancel/join owned synchronous requester/cache calls and standalone adapter read/write queues.
4. Only then close protocol/backend resources and release directory reservations.

Thus shutdown is graceful for the active business binding, then cancellation-and-join for outstanding independent work; it is not a promise to persist every pending cache write. `WaitCacheWrites` means **settled**, not **successfully persisted**. Caller/owner cancellation and late SQL failures may prevent persistence; a successful RPC return is not a cache-durability acknowledgment.

The `Close` context bounds only that caller's wait. One retained close operation owns unfinished work even after a timeout; a later `Close` joins the same operation. No backend is closed underneath pending jobs, and retries do not start replacement close goroutines. An uncooperative external requester/SQL operation can therefore keep ownership pending rather than be abandoned. The lower-level storage `Close` has the same retained-operation mechanism, but its caller must first drain all clients/adapters/bindings itself.

## Evidence boundaries

`store/independent_storage_test.go` covers namespace/path rejection, sentinel preservation, active path ownership and retained close, cross-process PGlite duplicate-owner rejection, PostgreSQL duplicate-owner rejection and actual termination of each dedicated connection with fail-closed behavior, plus actual PostgreSQL/persistent-PGlite independent cache writes inside a business SQL transaction, rollback separation, successful-init context cancellation and close/reopen durability. Focused two-connection-fencing normal/race logs are `parity/runs/independent-storage-m41/topology-focused-05.{normal,race}.log`; inherited-lock/orphan-window and SQL-guard controls are recorded separately under `topology-orphan-02.{normal,race}.log`. Earlier attempts are retained, including the initial PGlite failure caused by placing a lock file in an otherwise empty initdb directory.

Full stage acceptance and source integration probe results are recorded separately. These focused tests alone do not claim complete M41 acceptance, original-case equivalence, full cached-client compatibility, automatic multichain setup, MarkReady, historical/realtime cutover or production migration compatibility.
