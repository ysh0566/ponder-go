# Database engine exit during a finalized Omnichain callback

The historical runner uses separate business and sync stores. The cases cover a closing block callback on both chains and a uniform static-log-only configuration on both chains. They use SQL page size 6 and pinned Ponder 0.18.2 callback, row and checkpoint fixtures.

In a partially processed event group, the Go handler reads the updated shared row through SQL inside its active business transaction. The test then kills the disposable PostgreSQL server or the tracked PGlite child for one of the two directories. A business query observes loss of the PostgreSQL or PGlite business engine. For interruption of the PGlite sync engine, a handler RPC cache read observes the loss. The Go application stays alive throughout each test.

The failed owner reports unready. Before opening a new owner, the test reads the original business database and compares the previously committed row and undo state with the pinned failed-group snapshot. Cold reopen checks both chains' safe, latest and finalized logical positions, the exact callback suffix, final business data and canonical block hashes. A second completed open receives zero callbacks.

## Verification

```sh
for mode in ordinary race; do
  flags=()
  if [[ $mode == race ]]; then flags+=(-race); fi
  for selection in '^postgres$' '^pglite$/^business$' '^pglite$/^sync$'; do
    go test "${flags[@]}" -count=1 -v -timeout=45m -run "^TestOmnichainEngineInterruption$/${selection}" ./historical
  done
done
go test -run '^$' ./...
go vet ./...
```

Each Go mode passed six scenarios on PostgreSQL 14.24 and PGlite 0.2.13. PostgreSQL starts with a private Docker volume and is restarted after exit code 137. Each PGlite case uses newly allocated business and sync directories. The module compiled and passed vet. The repository's verification tools passed 503 Node tests and 40 Python tests.

At Ponder 0.18.2 commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, `vitest run --maxWorkers=1 --hookTimeout=45000 src/database/index.test.ts src/database/actions.test.ts -t 'crash recovery|revert\('` passed six selected database cases. Twenty-three other tests in those files were outside the selection. The native fault-injection scenarios leave the upstream case inventory unchanged. Full-module ordinary and race results for this revision are pending in the shared regression queue.
