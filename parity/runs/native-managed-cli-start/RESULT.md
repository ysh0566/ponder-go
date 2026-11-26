# Default multichain independent storage through ponder start

The Go example registers a block callback on each of two local Anvil chains and writes one row per checkpoint to the business database. The independent runtime owns both storage domains. Its HTTP server borrows a reader for committed business rows, named chain progress and live-query notifications.

## Application lifecycle

The test builds the real CLI and uses it to build and launch the example as a separate Go project. A forwarding proxy holds the first historical block from one node until the test observes HTTP 200 from health and HTTP 503 from readiness and live SQL. After release, the two chains reach block 3. Both the grouped SQL results and named status are checked. A subscribed client receives new counts when chain one reaches block 4.

The PostgreSQL project uses the default five-second grace period. The PGlite project selects `--shutdown-timeout 30s` to allow its two local engines to finish even under race instrumentation. The parent CLI receives SIGTERM and must exit successfully. Chain two advances while the application is down. The same storage and build identity are reopened with a new owner. The test reads counts of 4 and 4, mines one more block and reads counts of 4 and 5 before sending a second SIGTERM. The example uses checkpoint primary keys and ordinary inserts, so duplicate persistent writes fail the run.

PostgreSQL readers and bookkeeping audits use pooled sessions. Mutating operations retain their original lifetime-lock sessions and ownership checks. A separate test holds a real database lock in front of a SQL query, a business-state audit and a sync-ledger read. It observes each wait through PostgreSQL, cancels it, commits a new batch and closes the same owner cleanly.

## Verification

PostgreSQL 14.24 and PGlite 0.2.13 backed these tests. Anvil 1.8.5 provided both chain histories. The race selection builds the child CLI and its example with the race detector as well.

```sh
selection='^Test(ManagedPostgresReadCancellation|ManagedLiveDomainsDatabase|ApplicationServeOnly|ApplicationShutdownJoinsHandler|SourceHTTPServer|HTTPServerBoundaries)$'
cli='^Test(ManagedMultichainCLIStart|LaunchGracefulShutdown|LaunchAddressFlags|StartRejectsNonpositiveShutdownTimeout)$'
readers='^Test(SQLValidationHTTP|SQLNotificationInvalidation|SQLCacheCancellation|ManagedLiveDomainsGuards)$'
go test -v -count=1 -run "$selection" ./store ./query ./runtime
go test -race -v -count=1 -run "$selection" ./store ./query ./runtime
go test -v -count=1 -run "$cli" ./cmd/ponder
go test -race -v -count=1 -run "$cli" ./cmd/ponder
go test -v -count=1 -run "$readers" ./query ./store
go test -race -v -count=1 -run "$readers" ./query ./store
```

Five pinned Ponder 0.18.2 server tests passed on the original source. Nine tests outside the selection were filtered out.

```sh
vitest run --maxWorkers=1 --hookTimeout=45000 src/server/index.test.ts -t '^(not ready|ready|health|listens on ipv4|kill closes idle keep-alive connections)$'
```

Both Go test modes passed for each backend, including both complete CLI lifecycles. All packages compiled, go vet passed, and repository tooling passed 503 Node checks, 40 Python discovery tests and three observer tests. The full-module ordinary and race regressions for this revision are pending. The upstream Go inventory remains at 883 ported cases, with 866 passing.
