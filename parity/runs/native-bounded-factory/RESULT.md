# Bounded factory discovery with independent storage

A Go indexer can open a default multichain deployment with `FactorySources` and `SourceCheckpoints` enabled and `Live` unset. Each participating chain must have reached its configured end. The bounded runner uses the existing factory storage format, per-chain cache policy and branch-aware child discovery.

Two-chain HTTP RPC fixtures exercise factory logs, account transactions, native transfers and contract CALL traces. Chain one caches RPC requests and chain two disables caching. The first owner completes the four-block range. Before the next owner opens the same stores, chain one replaces the branch from block two and chain two keeps its branch. Each owner checks both canonical end hashes, checkpoints and exact business rows. Account and CALL expected events come from the pinned oracle captures. The log case verifies child addresses and heights, including delivery on the creation block.

A separate fixture finalizes a factory birth at height one and runs the child's log callback at heights three through five. A new owner reconstructs child membership after the factory creation interval has closed. Both owners inspect each chain's event rows.

The production admission checks continue to require source checkpoints and the default scheduler for these factories. The verification covers PostgreSQL 14.24 and PGlite 0.2.13. The native scenarios leave the upstream declaration counts unchanged.

## Verification

```sh
for backend in postgres pglite; do
  go test -v -count=1 -timeout=45m -run "^TestBoundedFactorySources$/^${backend}$" ./historical
  go test -race -v -count=1 -timeout=45m -run "^TestBoundedFactorySources$/^${backend}$" ./historical
done
go test -v -count=1 -timeout=45m -run '^TestBoundedFactoryFinalizedBirth$' ./historical
go test -race -v -count=1 -timeout=45m -run '^TestBoundedFactoryFinalizedBirth$' ./historical
selection='^(TestRuntimeFactory(Transaction|Transfer|Trace)Capability|TestRuntimeOmnichainPreflightBoundary|TestRuntimePreflightRejectsUnsupported)$'
go test -v -count=1 -run "$selection" ./historical
go test -race -v -count=1 -run "$selection" ./historical
go test -run '^$' ./...
go vet ./...
```

The upstream selection used `vitest run --maxWorkers=1 src/sync-historical/in-memory.test.ts src/runtime/index.test.ts -t 'factory|child addresses'` in `packages/core`. Both files passed, with 13 selected tests and 25 outside the selection.

Go 1.25.11 ordinary and race runs each passed ten database scenarios and 29 preflight controls. After integration with native multichain engine recovery, the PostgreSQL bounded factory log case passed again, every module package compiled, `go vet ./...` passed and all 503 verification-tool tests passed. Archived tool fixture inputs were verified against their repository hashes before use. Two tooling files used a dedicated temporary volume.
