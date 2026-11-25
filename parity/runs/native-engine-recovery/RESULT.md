# Native crash recovery

Go 1.25.11, PostgreSQL 14.24 (Docker) and PGlite 0.2.13 (Node 24) were used. Both backends use the current storage format.

| Test selection | Ordinary | Race |
| --- | --- | --- |
| Store engine kill and existing application-exit tests | 10 passed | 10 passed |
| Omnichain uniform log-only application exit | 10 passed | 10 passed |

The Store test first commits a balance of 7. A second transaction sets it to 13 and reads back its undo record before the engine receives SIGKILL. PostgreSQL uses a separate disposable container. The test checks exit status 137, restarts the same container and confirms a new postmaster. PGlite reopens the same physical directory after its engine process exits. Each backend recovers balance 7, the original event and journal checkpoint, and the original progress. Submitting the interrupted event twice invokes its callback once. A subsequent reorg restores balance 7 and the original checkpoint.

The log-only test runs a child Go indexer with independent business and sync storage. It forces an application exit during a callback or immediately after activation. It checks the exact callback prefix and durable rows, compares the resumed checkpoints and results against the pinned fixtures, and reopens again to verify no duplicate callbacks. The selection covers seven PostgreSQL cases and three PGlite cases, including empty-range activation on both backends.

Run the following commands with a test database in `PONDER_TEST_POSTGRES`, a local `postgres:14.24-bookworm` image, the PGlite module available to the bridge, and the historical fixture directories configured. Use a separate writable `PONDER_M45_TEST_ROOT` per historical run.

```sh
export PONDER_TEST_POSTGRES_ENGINE_IMAGE=postgres:14.24-bookworm
go test -count=1 -v -run '^(TestDatabaseEngineRecovery|TestCrashRecovery)$' ./store
go test -race -count=1 -v -run '^(TestDatabaseEngineRecovery|TestCrashRecovery)$' ./store
go test -count=1 -v -run '^TestRuntimeOmnichainLogOnlyApplicationExit$' ./historical
go test -race -count=1 -v -run '^TestRuntimeOmnichainLogOnlyApplicationExit$' ./historical
```

Host power loss and database engine interruption during coordinated operations across the two physical stores remain open. These native checks leave the upstream case counts unchanged.
