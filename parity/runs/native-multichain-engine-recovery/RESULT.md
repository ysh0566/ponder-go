# Atomic multichain recovery after a database engine exit

Two independently mined Anvil chains index block callbacks through `runtime.NewIndexer`. Each callback inserts its chain, height and canonical hash. The native multichain and Omnichain schedulers run the same scenario on PostgreSQL and PGlite.

After both chains have indexed heights one and two, chain A replaces both blocks while chain B keeps its original branch. In the callback for A's replacement block one, the test kills the engine after the journaled business insert returns and before the callback transaction can commit. The readiness observer sees the indexing failure. PostgreSQL uses an isolated container and checks its SIGKILL exit status before restarting the same data volume. PGlite uses the child PID registered by this Go process for this exact directory.

The next owner checks that the interrupted insert is absent before running the indexer. At catch-up it compares all four business rows and stored block hashes with the two Anvil chains. A second cold reopen verifies the same rows and hashes and checks that no callback was delivered again. These cases use native block callbacks on unfinalized replacement branches.

## Verification

```sh
go test -v -count=1 -timeout=30m -run '^TestMultichainEngineRecovery$' ./runtime
go test -race -v -count=1 -timeout=30m -run '^TestMultichainEngineRecovery$' ./runtime
control='^TestCoordinatedEngineInterruption$/^pglite$/^sync$/^ack$'
go test -v -count=1 -timeout=45m -run "$control" ./historical
go test -race -v -count=1 -timeout=45m -run "$control" ./historical
```

On the pinned upstream, six database recovery and revert cases passed with `vitest run --maxWorkers=1 --hookTimeout=45000 src/database/index.test.ts src/database/actions.test.ts -t 'crash recovery|revert\('`. The first invocation passed five cases and timed out in the isolated database setup hook for the sixth. The longer setup deadline allowed the unchanged selection to finish. Twenty-three tests were outside the selection.

All four native runtime cases passed in both ordinary and race runs. The independent PGlite control passed in both modes with the shared child tracker. The first native selection encountered PostgreSQL startup failures during a workspace disk shortage. After freeing space, two complete fresh selections passed. Module compilation, `go vet ./...` and all 503 verification-tool tests passed. The two tooling files that copy the upstream checkout ran with a dedicated temporary volume.
