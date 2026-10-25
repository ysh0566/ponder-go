# M42 bounded source transaction observation

These supplemental cases run pinned `runtime/multichain.ts:runMultichain`
unchanged. They confer no original-test or outer-runner credit. Each test creates
a real source database/schema, two independent real Anvil chains, actual setup
and block handlers, and real RPC-backed cached clients. Fixture startup alone is
adapted to explicit loopback ports using the existing audited adapter.

The real `getHistoricalEventsMultichain` iterator is wrapped at its consumer
boundary. The wrapper either emits zero groups (setup-only) or the first actual
extracted/decoded group, then throws `HarnessStop`, a properly branded
`NonRetryableUserError`, on the next pull with transaction depth zero. It never
fabricates groups or substitutes expected results. Iterator `return`, admitted
RPC/cache work, and source shutdown are joined. A harness stop is captured
separately from a source failure. This is not historical completion or readiness;
cutover operations must not occur, and actual `app.is_ready` remains zero.

Observers delegate to real factories, `cache.flush`, query-builder transactions
and metadata wraps. Captured state includes before/after business rows,
checkpoints, app metadata and RPC cache, exact handler order and stable
args/context/database identities, yielded groups and original RPC responses.
The source application metric initializer is called, as in `bin/commands/start.ts`,
before running the source coordinator. This matters because rollback restores
previously existing completed-event gauge labels, not arbitrary synthetic
counters.

Six scenarios are required:

1. Setup order 1,2,1 mutates one shared row; setup RPC blocks 2,1,3 remain distinct
   from initial checkpoint selection.
2. Start block 2 after finalized block 1 selects finalized, proving the other
   branch of `min(start,finalized)` with actual source sync-progress encoding.
3. Second-chain setup failure preserves original business/checkpoint state;
   independent source RPC cache writes still settle and persist.
4. One real historical group commits and the next pull stops before cutover.
5. The second real historical event throws a nonretryable user error: completed
   gauge is restored while successful-event attempt/profile counters retain one.
6. A branded injected error at the second actual `update_checkpoints` invocation
   occurs after the real historical flush. PostgreSQL rolls back those business
   rows; source PGlite retains the flushed rows because its QB transaction merely
   calls the callback. In both, checkpoint metadata remains at setup and completed
   gauges restore zero while attempt/profile counts retain the actual group size.

Native journal attribution/sealing/pruning is an atomicity extension; source
historical setup/groups do not install native MAX journal segments. Likewise,
native independent PGlite directories and true SQL rollback intentionally differ
from source one-instance callback-only transactions. Do not claim physical
journal or failure-rollback parity from these captures.

`source-multichain-atomic-evidence.mjs` rejects reduced cases, mismatched chain
identities, altered source counters, callback reordering, wrong metadata,
unjoined RPC/cache work, inside-transaction stops and accidental readiness.
The source driver verifies the new M42 PostgreSQL receipt, canonical data path,
fresh inventory and actual `SHOW data_directory` before invoking the legacy
fixture DDL. No old cluster/schema maintenance is authorized.
