# Go source builds for dual-store writers

`config.BuildRuntimeSourceIndexing` compiles a Go project's callbacks, filters, chain metadata and transports. Fixed and omitted source or factory bounds require no RPC. A bound of `"latest"` acquires one head on its chain during compilation. The dual-store runtime obtains its own finalized heads during opening. Callers that need finalized blocks in the compile result can use `config.BuildSourceIndexing`.

The default dual-store requester factory creates a fresh client for each participating chain in executable order, including after recovery. It uses the declared transport, chain name, chain ID, fixed log range policy and original per-chain concurrency limit. The CLI example formerly supplied clients for all configured chains, including ones dropped by callback compilation. An application can still override `RuntimeOptions.Requesters`.

## Verification

- A configuration test supplies an unused chain, a custom Go transport and a two-URL pool. It checks the selected order, fresh identities, reset request counts, custom calls and chain metadata across two generations. The existing compiler cases exercise fixed and dynamic source and factory bounds, validation and cancellation.
- A live Isolated project compiles with one participating chain returning HTTP 503. The offline endpoint receives zero compile requests. Both chains are available when storage opens. A later outage and fork exercise healthy-peer progress and canonical replay with the application's explicit requester factory.
- Bounded Isolated uses two Anvil nodes, a third idle RPC that fails the test on contact and the default requester factory. On PostgreSQL and PGlite it resumes a failed callback, validates the safe per-chain cursors, then cold-reopens the completed range.
- The real `ponder start` project indexes two Anvil chains and leaves a third unavailable RPC untouched. Its SQL results cover new blocks before and after writer restart on both backends. The PostgreSQL reader variant also uses a read-only login and follows writer progress across restart.

A participating chain that is offline at *storage open* still prevents both initial launch and cold reopen. Each participating chain's opening finality currently anchors the two-store recovery protocol. To support that case the stores must represent a chain awaiting its first authenticated header. For a returning chain the writer must check the durable safe cursor against canonical RPC before replaying its callbacks, while the healthy chain continues committing. [Original offline opening probe](../native-go-startup-audit/RESULT.md) · [Opening checks](../../../historical/live_runtime.go) · [Durable recovery](../../../store/managed_live_recovery.go)

## Reproduction

From the repository root, use Go 1.25.11, Anvil 1.8.5, a task-owned PostgreSQL 14.24 database and the PGlite 0.2.13 bridge. Set the managed fixture root, PostgreSQL URL and PostgreSQL binary directory for the test harness. The compiler cases need the repository's pinned upstream checkout and generated fixtures.

```sh
unset PONDER_UPSTREAM_ROOT
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 -timeout=35m ./config
  go test $flags -p 1 -count=1 -timeout=35m -run '^(TestRuntimeIsolatedFinalized|TestRuntimeIsolatedLiveOutage|TestRuntimePreflightRejectsUnsupported|TestRuntimeSetupPreflight)$' ./historical
  go test $flags -p 1 -count=1 -timeout=35m -run '^TestManagedMultichainCLIStart$' ./cmd/ponder
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The ordinary and race selections each passed 155 Go leaf cases (135 config, 17 historical and 3 CLI). All 51 module packages compiled and `go vet -p 1 ./...` passed. Repository tooling passed 503 Node and 43 Python checks. The module-wide ordinary and race suites use a separate release queue.

On Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, run `node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/build/config.test.ts` from `packages/core`. All 39 enabled tests passed. Upstream skips one public RPC warning test. Deferred head acquisition and the dual-store CLI restart scenarios are native Go coverage. The formal upstream mapping totals remain 883 Go cases executed, 866 passed and 17 provider-specific failures. [Use the dual-store writer](../../../docs/USAGE.md#用-cli-启动独立存储的多链索引器)
