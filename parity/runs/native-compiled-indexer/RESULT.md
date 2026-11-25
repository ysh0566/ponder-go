# Complete compiled Go source builds in the native indexer

A Go application can pass its compiled source indexing build to `runtime.NewIndexerFromSource`. The constructor uses the build ordering and the existing native workers. Sources without registered handlers do not extend a finite range. Setup-only contracts contribute their initialization block.

The acceptance fixture starts three independent Anvil 1.8.5 chains with distinct chain IDs. Two participate, with a third, unused declaration between them. Each chain mines three timestamped blocks and the registered block callbacks stop at height two. One participating chain disables caching and the other enables it. The second chain also registers an unbounded contract with only setup. The first declares an unbounded block source without a handler.

PostgreSQL 14.24 and PGlite 0.2.13 each exercise default, Omnichain and Isolated ordering. The checks cover registration and client selection, `eth_chainId` through each callback, the per-worker cache and the unmodified shared transport, completion, the two height-two canonical hashes, committed deliveries and a second open on the same persistent data. The Omnichain case checks cross-chain callback order. Native admission checks cover the selected one-chain worker, missing inputs, invalid ordering, store ordering mismatch and invalid callback types.

## Verification

The upstream source builder passed five selected cases with `vitest run --maxWorkers=1 src/build/config.test.ts -t 'multi-chain contracts|returns chain, rpc|overrides default values|block source|account source'`. Thirty-five tests were outside the name filter. The native constructor supplements leave the upstream comparison inventory unchanged.

Go 1.25.11 ordinary and race selections each passed 28 runtime leaf cases and three source-projection tests. Module-wide compilation, vet and 503 verification-tool checks passed. Full-module ordinary and race results for this revision are pending in the shared regression queue.

```sh
selection='^(TestCompiledMultichainIndexer|TestCompiledIndexerAdmission|TestDefaultIndexer|TestOrderedIndexer|TestIsolatedIndexer|TestSourceConfigurationRuntime|TestSourceFactoryRuntime)$'
go test -v -count=1 -timeout=20m -run "$selection" ./runtime
go test -race -v -count=1 -timeout=20m -run "$selection" ./runtime
go test -v -count=1 -run '^TestExecutableSource(ActualHandlers|ProjectionGraph|ExclusiveSession)$' ./config
go test -race -v -count=1 -run '^TestExecutableSource(ActualHandlers|ProjectionGraph|ExclusiveSession)$' ./config
go test -run '^$' ./...
go vet ./...
```
