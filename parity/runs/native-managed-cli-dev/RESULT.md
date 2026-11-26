# Hot reload for the independent multichain Go project

The native dev watcher passes the project revision to the Go application. The managed multichain example uses that revision as the build ID and opens its own business and sync stores. The writer records a development deployment in app metadata. The business and sync identities distinguish development deployments from production. Dev opens the revision’s business namespace directly, including when a production views name is present in the environment.

The integration test starts two local Anvil chains and the real dev command, then reads indexed rows over HTTP. A Go compile error leaves the first process indexing. A valid handler edit builds a second revision that replays the existing blocks with changed output and indexes another live block. Returning exactly to the first project revision reopens its persisted state and catches up under the original handler. The test exercises a full-length 45-character business namespace on PostgreSQL and PGlite.

## Verification

The integration tests use Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13. The race selection instruments the Go watcher and the project binaries that it builds. The PostgreSQL case checks the full revision in app metadata, the development flag, release of the previous writer’s lease and isolation from the configured production views namespace. The PGlite case verifies the business and sync directories for both revisions and reopens the original pair.

```sh
cli='^Test(ManagedMultichainCLIDev|DevReload|StartRejectsNonpositiveShutdownTimeout|LaunchGracefulShutdown)$'
storage='^Test(ManagedFiniteOptionsAndSchemaSnapshot|ManagedFinitePostgresIdentityFences|RuntimeBoundedLifecycle)$'
go test -v -count=1 -timeout=45m -run "$cli" ./cmd/ponder
go test -race -v -count=1 -timeout=45m -run "$cli" ./cmd/ponder
go test -v -count=1 -timeout=45m -run "$storage" ./store ./historical
go test -race -v -count=1 -timeout=45m -run "$storage" ./store ./historical
go test -v -count=1 -timeout=45m -run '^TestManagedMultichainCLIStart$/^postgres-serve$' ./cmd/ponder
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
```

Five upstream build and server tests passed. The selection filtered out 11 tests in the two files.

```sh
vitest run --maxWorkers=1 --hookTimeout=45000 src/build/index.test.ts src/server/index.test.ts -t '^(namespaceCompile\(\) rejects a sync schema as the (schema|views schema)|health|not ready|ready)$'
```

Hot reload on both backends and the storage identity and bounded-reopen controls passed in ordinary and race runs. The production PostgreSQL writer and standalone reader also passed their CLI regression. All packages compiled and passed vet. The verification tools passed 503 Node tests, 40 Python discovery tests and three observer tests.

The full-module ordinary and race regressions for this revision are pending. The inventory remains at 883 ported Go cases, with 866 passing.
