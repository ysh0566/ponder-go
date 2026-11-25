# Independent multichain store recovery

A deterministic two-chain HTTP RPC fixture indexes an original branch and then supplies a replacement branch. The Go application and database engines are separate OS processes. PostgreSQL 14.24 uses one task-owned server with separate business and sync pools and schemas. PGlite 0.2.13 runs two engines in separate persistent directories.

The engine interruption test kills the PostgreSQL container with SIGKILL, or kills the PGlite business or sync child identified by the exact directory and its Go parent PID. The Go process stays alive. Each target is interrupted after the sync-side intent, after the business rollback commit, and after the sync-side acknowledgement. The application-exit control terminates a child Go process with exit status 83 at the same boundaries on both backends.

A fresh owner reopens the same stores. It starts with readiness false and outstanding replay targets. Two successive opens verify each chain’s callback heights after its safe cursor, its canonical block-four hash and checkpoint, two final balances of four, and exactly one durable intent and acknowledgement for the interrupted operation. The application-exit control checks the recovered operation and final balances. Unfinalized callback delivery is repeatable.

## Verification

```sh
selection='^(TestCoordinatedEngineInterruption|TestRuntimeContinuousCrash)$'
go test -v -count=1 -timeout=45m -run "$selection" ./historical
for target in postgres pglite/business pglite/sync; do
  go test -race -v -count=1 -timeout=45m -run "^TestCoordinatedEngineInterruption$/$target/" ./historical
done
go test -race -v -count=1 -timeout=45m -run '^TestRuntimeContinuousCrash$' ./historical
go test -v -count=1 -timeout=45m -run '^TestCoordinatedEngineInterruption$/(postgres|pglite)/(business|sync)/business$' ./historical
go test -run '^$' ./...
go vet ./...
```

The pinned upstream database tests passed six selected crash recovery and revert cases with `vitest run --maxWorkers=1 src/database/index.test.ts src/database/actions.test.ts -t 'crash recovery|revert\('`. The selection excluded 23 tests. Host power loss and engine interruption in other runtime modes need separate verification. These native recovery cases do not change the upstream case inventory.

All 15 native scenarios passed in ordinary and race runs. Nine cover engine interruption and six cover application exit. The three ordinary business-commit engine cases also passed in a separate run. All Go packages compiled, `go vet ./...` passed, and the repository verification tools passed 503 tests.
