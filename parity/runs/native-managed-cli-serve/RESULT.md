# Independent PostgreSQL query service for a Go multichain project

The managed multichain example supports a separate `ponder serve` process. It checks the business model fingerprint and runtime format in the existing namespace, reads the writer’s committed readiness flag and joins progress with the recorded chain names. PostgreSQL delivers a notification for each published relation when the writer commits activation. This refreshes existing queries after historical writes made before live-query triggers were installed.

The end-to-end test builds the Go CLI and project, mines blocks on two local Anvil 1.8.5 chains and starts separate writer and reader processes. The reader uses a dedicated login with read-only transactions and SELECT on the four metadata tables and one business table. The test confirms the login lacks business INSERT and sync schema access. The writer’s RPC configuration file is temporarily removed during reader startup.

Before backfill, both servers return 503 for readiness and new live queries. After backfill, the test receives a PostgreSQL notification for the business table before another block is mined. The query process reads named checkpoints and row counts, serves a live insert, stays running while the writer stops, and delivers an aggregate over the same SSE connection after restart and catch-up. The writer then indexes another block after the reader exits. The test also checks failure on a missing namespace or mismatched model.

## Verification

The focused selections use PostgreSQL 14.24 and the project’s PGlite backend. The CLI test propagates the race flag to its child binaries. An early instrumented PostgreSQL run exceeded the default five-second writer shutdown grace. The test now uses the documented 30-second startup command option on both backends. The PostgreSQL shutdown case passed on rerun.

```sh
cli='^Test(ManagedMultichainCLIStart|GeneratedGoServe)$'
controls='^Test(ManagedPostgresReadCancellation|OpenExistingDeployment|ApplicationServeOnly|SQLNotificationInvalidation|PostgresQueryListenerReconnect|SourceHTTPServer|PublishViewsOnReady)$'
go test -v -count=1 -timeout=45m -run "$cli" ./cmd/ponder
go test -race -v -count=1 -timeout=45m -run "$cli" ./cmd/ponder
go test -v -count=1 -timeout=45m -run "$controls" ./store ./query ./runtime
go test -race -v -count=1 -timeout=45m -run "$controls" ./store ./query ./runtime
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

Four selected tests passed on the pinned upstream revision. The selection filtered out 21 tests in the two files.

```sh
vitest run --maxWorkers=1 --hookTimeout=45000 src/server/index.test.ts src/database/actions.test.ts -t '^(not ready|ready|health|live query notify trigger batches large payloads)$'
```

The PostgreSQL writer, PGlite writer, independent PostgreSQL reader and generated project CLI cases passed in ordinary and race mode. The seven store, query and runtime controls passed in both modes. All packages compiled and passed vet. Tooling passed 503 Node tests, 40 Python discovery tests and three observer tests. The full-module ordinary and race regressions for this revision are pending. The Go inventory remains at 883 ported upstream cases, with 866 passing.
