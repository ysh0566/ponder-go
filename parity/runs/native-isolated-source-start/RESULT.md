# Isolated startup from Go source declarations

On a single store, `runtime.NewSourceIndexer` compiles the declared sources and binds native handlers before the chain workers start. Numeric or omitted source and factory bounds need no RPC during compilation. Each chain with an explicit `"latest"` bound supplies one latest header for all its declarations. The worker obtains finality from its own node during indexing. The full `config.BuildSourceIndexing` API still produces its finalized-block array for consumers that use it.

## What the tests exercise

- Two real Anvil nodes on PostgreSQL and PGlite. The second node returns HTTP 503 on the initial launch. The first chain commits block 1 while deployment readiness is false. After the peer returns, the test observes both chains at block 2. A replacement branch on the first chain preserves the peer's finalized rows. On the next launch the peer is offline again, the first chain commits its new second block, and readiness follows the peer's return. Across both openings the two handlers run four and two times, respectively.
- The direct Go-config entry point runs the same schedule. Six bounded deployments cover all three orderings and both databases. Each starts from a full build, completes and reopens using the new source constructor. The callback counts, per-chain canonical hashes and persisted rows are checked. Two additional Omnichain deployments reopen with a user-supplied Go RPC transport.
- Compiler controls exercise zero head reads on static chains, shared dynamic source bounds, a dynamic factory bound, invalid fixed and resolved ranges, cancellation before compilation, and cancellation during a required head read. The original full source builder retains its normal output and RPC behavior.

## Reproduction

Use Go 1.25.11, Anvil 1.8.5, a task-owned PostgreSQL 14.24 database and the PGlite 0.2.13 bridge. Configure the repository's test environment. The config package's compiler harness writes disposable binaries under `.parity-build/config-types`. The test checkout placed those binaries on a temporary volume after the workspace volume filled. The pristine oracle uses its sibling `ponder-upstream` checkout.

```sh
unset PONDER_UPSTREAM_ROOT
cases='^(TestIsolatedSourceIndexer|TestIsolatedIndexer|TestCompiledMultichainIndexer|TestCompiledCustomTransport)$'
go test -p 1 -count=1 -v -timeout=25m -run "$cases" ./runtime
go test -race -p 1 -count=1 -v -timeout=25m -run "$cases" ./runtime
go test -p 1 -count=1 -v -timeout=15m ./config
go test -race -p 1 -count=1 -v -timeout=15m ./config
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

Both ordinary and race runs passed the same 134 config leaf tests and 12 runtime leaf tests. All 51 module packages compiled and `go vet -p 1 ./...` passed. Repository tooling passed 503 Node checks and 43 Python checks. These tests leave the pinned mapping totals at 883 executed, 866 passed and 17 provider-specific failures.

The upstream control uses Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. From `packages/core`, run `node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/build/config.test.ts`. Its 39 enabled tests passed, including latest bounds, factory ranges and full build finality. Upstream skips its public-RPC warning case. The isolated outage scenario is a Go-specific extension.

The independent business and sync stores still require every participating chain's opening headers. The single-store constructor also needs a reachable endpoint when a declared bound on that chain explicitly requests `"latest"`. [Usage](../../../docs/USAGE.md#用编译后的-go-来源配置启动多链) · [Two-chain run](../../../runtime/isolated_indexer_test.go) · [Compiler controls](../../../config/source_native_heads_test.go)
