# Database engine restart during a native mixed-source fork

The native Go indexer resumes from its last committed event when its database engine exits during a provider switch and a two-chain fork recovery. The test uses the public application launcher, the atomic Store and three local Anvil nodes. Chain 101 switches from an unavailable primary to a second node with a different block hash, factory-created child address and event value. Chain 202 keeps its original hash. The PostgreSQL selection uses the RPC cache and PGlite reads from the node directly. [Test matrix](../../../runtime/mixed_source_engine_recovery_test.go) · [Nodes, callbacks and row assertions](../../../runtime/mixed_source_reorg_test.go)

| Engine interruption | Ordering | Durable state before restarting the Go worker |
| --- | --- | --- |
| After the replacement contract CALL writes, before its transaction commits | Isolated, default multichain, Omnichain | The replacement account transaction and contract log have committed. The CALL and account transfer are absent. Both factory filters contain only the replacement child. Isolated retains the peer row and checkpoint. Both global modes have rewound the peer. |
| After the dependent peer log writes, before its transaction commits | Default multichain, Omnichain | All four replacement events and the block-end checkpoint have committed. The interrupted peer write is absent. The replacement header and both factory filters match the new branch. |

Each case kills its own PostgreSQL service or PGlite child process while the handler holds the transaction open. A subsequent query through the same transaction fails. The test restarts PostgreSQL on the same volume or opens the same PGlite directory. It inspects business rows, headers, factory discoveries and both progress records before launching the worker. Default multichain also retains its pending replay marker until catch-up completes.

After catch-up, the replacement log and transfer contain 11, replacing 7. Both global orderings rerun the surviving peer log against the replacement value and store `23/11`. Isolated retains the original peer observation of 23. Only the interrupted transaction adds a handler invocation beyond the expected fork replay. A second clean reopen preserves the checkpoints and adds no callbacks. External effects performed by a Go handler fall outside this database transaction.

## Reproduction

Use Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and the repository's PGlite test environment. The engine-kill tests create dedicated databases. The accompanying provider tests use the ordinary test database configured with `PONDER_TEST_POSTGRES`.

```sh
selection='^(TestMixedSourceEngineRecovery|TestMixedSourceProviderReorg|TestMultichainEngineRecovery)$'
go test -p 1 -count=1 -v -timeout=30m -run "$selection" ./runtime
go test -p 1 -race -count=1 -v -timeout=30m -run "$selection" ./runtime
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The focused selection passed 20 cases in ordinary mode and 20 cases with the race detector, with zero skips in either run. With the stricter exact-checkpoint assertions, all ten engine interruption cases passed again in each mode using the same commands with `-run '^TestMixedSourceEngineRecovery$'`. All 50 Go packages compiled and passed vet. Repository verification passed 503 Node.js tests and 43 Python tests. The module-wide ordinary and race suites run separately and their results are pending.

At upstream Ponder 0.18.2 commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, six selected crash-recovery and rollback cases passed against PostgreSQL 14.24. The filter excluded 23 other cases. Run this command from `packages/core`.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/database/index.test.ts src/database/actions.test.ts -t 'crash recovery|revert\('
```
