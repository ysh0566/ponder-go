# Setup and RPC cache policies for bounded default multichain indexing

The independent storage runtime accepts setup callbacks and per-chain cache policies with `RuntimeOptions.Live` unset. It uses the same setup receipt and cache-aware recovery rules as the corresponding continuous deployment. The finite plan still requires an end block for every callback and rejects ranges beyond the acquired latest block. Omnichain has a separate finalized-range contract.

The native regression uses a two-chain HTTP RPC fixture, PostgreSQL 14.24 and PGlite 0.2.13. Both chains expose height five and the callbacks end at height three. Three cases run on each backend.

- Both chains cache RPCs and run setup. Each event sees the row committed by its own chain’s setup callback.
- One chain disables caching and one enables it. The second setup callback fails after writing. The test checks the empty business table, retries with a fresh owner and verifies the full setup and event sequence. A direct read of the sync schema verifies one persisted balance response per indexed height for the enabled chain.
- Both chains disable caching and have no setup. The fixture returns a current balance of 42. Disabled caches contain a conflicting raw header and two stale balance responses, all of which must remain untouched.

After indexing and a cold reopen, both store contents and the two block-three checkpoints and hashes are checked. On these finalized ranges the callback lists remain exactly heights one, two and three. The preflight cases cover malformed setup metadata and cache policy, an end beyond acquisition, and the separate Omnichain requirements.

## Verification

```sh
selection='^(TestBoundedRuntimeSetupAndCache|TestRuntimePreflightRejectsUnsupported|TestRuntimeSetupPreflight|TestRuntimeOmnichainPreflightBoundary|TestRuntimeBoundedLifecycle)$'
go test -v -count=1 -timeout=45m -run "$selection" ./historical
go test -race -v -count=1 -timeout=45m -run "$selection" ./historical
go test -run '^$' ./...
go vet ./...
```

The pinned upstream passed nine selected setup and cache-policy tests with `vitest run --maxWorkers=1 src/indexing/index.test.ts src/indexing/client.test.ts -t 'processSetupEvents\(|executeSetup\(|without rpc request cache'`. The selection excluded 28 tests. The additional native cases leave the upstream case inventory unchanged.

Go 1.25.11 ordinary and race selections each passed 28 leaf cases. After adding the explicit persisted-result check, the two mixed-policy leaves passed again in both modes with `-run '^TestBoundedRuntimeSetupAndCache$/(postgres|pglite)/setup_mixed$'`. The ordinary and race totals count each leaf once per mode. All module packages built, vet completed successfully, and 503 verification-tool tests passed. Whole-module ordinary and race results for this revision are pending in the shared regression queue.
