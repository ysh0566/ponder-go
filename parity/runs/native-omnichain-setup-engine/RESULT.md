# Database interruption around the Omnichain setup commit

A bounded Go job registers a contract setup handler on each of two chains. The first handler inserts a shared row. The second reads and updates that row. After setup, the job processes block callbacks and static ABI logs in the checkpoint order from the pinned Ponder 0.18.2 fixture. [Go scenarios](../../../historical/live_runtime_omnichain_setup_test.go) · [Fixture](../../../historical/testdata/runtime_omnichain_logs_source_golden.json) · [Upstream setup transaction](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/runtime/omnichain.ts)

| Interruption point | Durable state before a new runtime opens | Cold restart |
| --- | --- | --- |
| In the second setup handler, after a SQL query observes both writes in the open business transaction | The setup row, undo rows and initial cursors are absent. The bootstrap and batch counts are zero. | Both setup handlers run again, in chain order. |
| In the notification after the setup transaction commits, before indexing the first event | The shared row contains both writes. One bootstrap batch installed both initial checkpoints. No indexed event rows or undo records exist. | The runtime preserves setup and processes the complete ordered event sequence. |

Each interruption runs with PostgreSQL, the PGlite business engine, and the PGlite sync engine. The PostgreSQL run uses a private PostgreSQL 14.24 data directory and an immediate server shutdown, which aborts active transactions and requires crash recovery on restart. The PGlite tests terminate one verified child process with SIGKILL and reopen the original two data directories. For a sync engine loss during setup, a new callback RPC cache key forces an actual sync database read after the interruption. The Go process stays alive. A separate SQL reader checks the persisted metadata, application rows and journal before the next runtime opens. A subsequent completed open processes zero callbacks.

## Reproduction

Use the repository's Go test environment with a writable PGlite test root and its pinned PGlite driver. Set `PONDER_TEST_POSTGRES_BINDIR` to an installed PostgreSQL server bin directory containing `initdb`, `pg_ctl` and `postgres`. Run the local fixture as an unprivileged user. It initializes its own cluster and uses a private Unix socket. The Docker fixture is also available with `PONDER_TEST_POSTGRES_ENGINE_IMAGE=postgres:14.24-bookworm` and the bindir unset. For the existing PostgreSQL setup controls, set `PONDER_TEST_POSTGRES` to a separate disposable database. [Server fixture](../../../internal/testpostgres/server.go) · [Local cluster](../../../internal/testpostgres/local.go)

```sh
for mode in ordinary race; do
  flags=()
  if [[ $mode == race ]]; then flags=(-race); fi
  go test "${flags[@]}" -p 1 -count=1 -timeout=45m -run '^(TestOmnichainSetupEngineInterruption|TestRuntimeOmnichainSetup)$' ./historical
  go test "${flags[@]}" -p 1 -count=1 -timeout=10m -run '^TestDatabaseEngineRecovery$/postgres' ./store
  go test "${flags[@]}" -p 1 -count=1 -timeout=10m -run '^TestGeneratedDeploymentRoles$' ./cmd/ponder
done
```

The focused Go checks use PostgreSQL 14.24 and PGlite 0.2.13. The six native engine interruptions supplement the four existing two-chain setup controls. The PostgreSQL Store control checks that the postmaster restarted and an interrupted business transaction rolled back. The deployment role control checks the permissions of the generated writer and deployment manager against the disposable cluster. The local PostgreSQL binaries were built from the official 14.24 source archive with SHA-256 `a7fa7ed3d558172355f51406097a7bd4f6b473be80f311ef7cda96bf383d8897`. Initial Docker runs ran out of space before PostgreSQL initialization, and an initial PGlite startup exceeded available memory. [Official checksum](https://ftp.postgresql.org/pub/source/v14.24/postgresql-14.24.tar.bz2.sha256)

Each Go mode passed all six engine interruptions and all four setup controls, with zero skips. The four PGlite interruption cases also passed together in ordinary mode with dedicated directory pairs. Both PostgreSQL fixture controls passed in each mode. The module-wide regressions are tracked separately.

All 50 Go packages compiled with `go test -p 1 -run '^$' ./...` and passed `go vet -p 1 ./...`. The repository verification tools passed 503 Node.js tests and 43 Python tests.

In the pinned upstream `packages/core` checkout, the following selection passed six database recovery and revert tests. The Go engine interruptions are supplemental native checks and leave the upstream case inventory unchanged.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/database/index.test.ts src/database/actions.test.ts -t 'crash recovery|revert\('
```

An engine exit inside the readiness activation transaction and the dual-store live Omnichain handoff remain open.
