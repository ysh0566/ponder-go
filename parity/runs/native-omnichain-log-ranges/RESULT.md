# Per-source ranges in independent log-only Omnichain indexing

Each configured static contract log callback can have its own inclusive block range on a chain. The runner acquires the chain from the earliest start through the latest end under the global finalized watermark. Each filter fragment must cover its intersection with the interval being certified. An inactive filter needs no coverage in that interval. Fragment identity and the shape of recorded coverage are still checked.

While verifying a gap or a remaining tail, the sync-store reader splits requests at filter starts and ends. Its SQL union can then return logs only for filters active throughout each request. Stored logs may be decodable and share the same selector yet fall outside their own contract's height range. Such logs do not cause callbacks or prevent activation. The business/sync format and its deployment identity are unchanged.

## Scenarios

The Go tests compile two contracts on two chains. On the first chain, A is enabled only at block 1 and B only at block 3. On the second, A covers blocks 1–3 and B overlaps at blocks 2–3. Four logs occur at each of heights 1 and 3 on both chains.

- The fresh-cache test acquires logs from the configured nodes. The first chain delivers eight callbacks and the second delivers twelve.
- The prefilled-cache test uses different contract addresses on the first chain. At height 1 its logs match B's address, and at height 3 they match A's address. All eight raw logs are decodable. The first chain delivers zero callbacks, the second delivers twelve and both finish scanning at height 3. The first chain retains its logical position at the start of block 1. On a third open the completed indexer delivers zero callbacks.
- Both configurations inject a callback error after writing to a shared business row. The cold reopen checks the exact global callback suffix, persistent rows, and both chains' safe, latest and finalized positions. Store tests also remove required coverage, supply malformed coverage for an inactive filter, reject incorrect chain-wide bounds, and check pagination through genesis, an uncovered height and an overlap.

The supplemental Ponder 0.18.2 reference test seeds the same two ranges and out-of-range address pattern. It checks the original runtime's callback order, rows, and both chains' three logical positions.

## Verification

The focused Go runs used Go 1.25.11, PostgreSQL 14.24 and PGlite 0.2.13 with separate test databases and temporary directories.

```sh
for backend in postgres pglite; do
  go test -v -count=1 -timeout=45m -run "^TestRuntimeOmnichainLogRanges$/^${backend}$" ./historical
  go test -race -v -count=1 -timeout=45m -run "^TestRuntimeOmnichainLogRanges$/^${backend}$" ./historical
  go test -v -count=1 -timeout=45m -run "^Test(OmnichainGenesis|RuntimeOmnichainLogOnlySource)$/^${backend}$/^(genesis_logs|abi_skip_trailing_empty)$" ./historical
done
go test -v -count=1 -run '^TestLogOnly' ./store
go test -race -count=1 -run '^TestLogOnly' ./store
go test -run '^$' ./...
go vet ./...
```

Against Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, the first command below ran in its `packages/core` directory. The second ran from the Go repository using the pinned source installation.

```sh
vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/historical.test.ts src/runtime/events.test.ts -t 'getHistoricalEvents\(\) omnichain|decodeEvents\(\) log'
vitest run --config tools/source-runtime-omnichain-sparse.config.mjs
```

The three selected upstream declarations and all ten supplemental probes passed. Twenty-two other upstream declarations in those files were outside the first selection. These native and reference supplement cases leave the upstream case inventory unchanged.

A separate SQL inspection opened copies of the two closed PGlite directories from the prefilled-cache case. The sync log table retained four chain-1 rows at each of blocks 1 and 3 with the opposite source addresses. The business table contained exactly the twelve chain-2 callback keys in their expected order.

```sql
SELECT block_number, address, count(*) FROM "<sync_namespace>_rpc_1".logs
WHERE chain_id = 1 GROUP BY block_number, address ORDER BY block_number;
SELECT value FROM "<business_namespace>".account WHERE id = 'shared';
```

All four backend/scenario combinations passed in ordinary and race modes. The four existing genesis and ABI-skipped tail controls passed in ordinary mode. The focused store suite passed in both modes, all packages compiled, and vet passed. The Go example from the usage guide compiled. The project tools passed 503 Node checks, 40 Python discovery tests and three additional observer tests. Full-module ordinary and race results for this revision are pending in the shared regression queue.
