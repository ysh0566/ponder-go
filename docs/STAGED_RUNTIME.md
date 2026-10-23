# Native staged single-chain runtime

`runtime.NewStaged(config, lifecycle, handlers, StagedOptions{BuildID: ...})`
uses an unmigrated `store.NewLifecycle` instance. `Sync` acquires/recovers its
lease, executes setup, backfills to the timestamp-based finalized boundary,
installs indexes/capture/live-query triggers and views, marks app metadata ready,
then processes live blocks. `Run` polls the same serialized `Sync` path. The
caller closes Lifecycle before Backend. The existing atomic constructor remains
available; a staged store cannot use atomic `Store.Apply`.

The source references are runtime/multichain.ts setup/historical/handoff/block/
reorg/finalize branches and runtime/events.ts splitEvents. Only single-chain
scheduling is implemented here; the source multichain algorithms remain pending.
The new source mapping is only the complete `splitEvents()` inline snapshot.
Native end-to-end/fault/process tests are additional evidence, not replacements
for original Foundry deployment/mint/filter/trace/runtime fixtures.

## Durable transitions

- Setup callbacks get the configured native setup block number. Setup business
  writes, initial checkpoint metadata and historical journal sealing commit
  together. A failed callback publishes neither data nor checkpoint metadata.
- Native historical business writes are still trigger-journaled. Each historical
  block commits data, full checkpoint, latest/finalized/safe and removal of its
  safe journal prefix together. This uses per-block transactions instead of the
  source's historical range batches, with the same confirmed boundary.
- Realtime handlers for one block execute in checkpoint order in one transaction.
  Writes initially have the 75-digit MAX sentinel. Flush/commitBlock for every
  table assigns the full end-of-block checkpoint; latest and NOTIFY then commit
  in that same transaction. Empty blocks still advance progress. Retry after an
  uncertain commit acknowledgment skips already committed business writes.
- Reorg uses the existing global-operation-suffix undo rules and canonical sync/
  protocol-cache rewind in one transaction. Public latest stays unchanged until
  a replacement block commits, matching the source. A private durable scheduler
  cursor lets failed replacement attempts resume without prematurely changing
  public metadata. The existing public Revert primitive is unchanged.
- Lifecycle recovery retains public latest while restoring safe business data.
  ResumeRuntime resets the private cursor and canonical provenance/cache at safe;
  subsequent indexing replays only unconfirmed blocks. Handler setup is not
  replayed for a recovered initialized application.
- Reorg/recovery transactions suppress database live-query publication. Actual
  committed replacement blocks publish their actual changed-table payload.
  Repeating Sync without changes does not publish a duplicate block notification.
  Automatic timestamp-based finalization advances the existing storage primitive.

No new eventless business-write route exists. Schema capture guards, lease
fencing and the same raw SQL trigger journal apply to all handlers.

## RPC calls inside a handler

A handler's cached RPC access uses its existing event transaction in the separate
sync cache schema. Starting a nested cache transaction while holding the store or
PGlite transaction lock would deadlock. `Client.WithCache` shares the original
transport's request IDs, retries and rate limiter while changing cache scope.
Per-block hash-addressed RPC cache misses are tested inside handlers; rollback
also rolls back those cache writes. Unknown request block metadata remains NULL
and is conservatively invalidated on reorg; full source request-hash/parameter
resolution remains pending. Scoped cache access rejects use after the handler.

## Verification and limits

Real existing Anvil plus task PostgreSQL and PGlite verify setup/history/live
handoff, cached/uncached operation, a head that advances during backfill, fork
replacement, handler and metadata failures, committed-acknowledgment failure,
private/public cursor separation, empty schemas, automatic finality, and actual
LISTEN/NOTIFY order. Six real process exits cover before sentinel assignment,
after latest but before commit, and after commit on both backends. Default
heartbeat timeout causes a deliberate ~25-second recovery wait per worker exit.
These are application-process tests, not server/host power-loss tests.

NOTIFY is not durable delivery. Reconnect tests read current data and listen for
later commits; they do not assert replay of messages missed while disconnected.
Full SSE reconnect/query resumption and external write notification integration
remain separate. query.Server's own Ready flag still needs explicit application
wiring; marking database app metadata ready does not automatically set it.

Historical/realtime transaction partitioning is native per-block adaptation.
Uncached-period logs/receipts are not automatically backfilled into protocol
cache tables. The full source filter/include/pagination planner, concurrent
history fetch pipeline, profile/prefetch/metrics integration, account/native
transfer/trace sources, multichain scheduling and complete CLI remain unfinished.
Strict TS source compatibility and real old-DB takeover remain separate tracks.
