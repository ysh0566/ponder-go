# Signed block intervals for Go indexers

With an inclusive source range of 2 through 9, the signed intervals −3 and 3 both deliver callbacks at heights 2, 5 and 8. The compiled source descriptors retain the declared interval and offset. The ordinary Go scheduler stores the distance between matches as a `uint64`. A Go application can also use `config.BlockPeriod(-3)` directly in a `config.BlockSource` literal. Zero is invalid. [Go configuration](../../../config/config.go) · [Usage](../../../docs/USAGE.md#配置双库-omnichain-的区块回调)

The independent-store Omnichain runner uses the magnitude to identify an every-block source and to prove that skipped heights contain no scheduled callbacks. Its stored source filters and cache fragment IDs retain the declared sign. The period must be a safe integer and the range must be globally finalized. [Runner admission](../../../historical/live_runtime_omnichain_callbacks.go) · [Gap verification](../../../store/managed_live_logonly_scan.go)

## Verification

- Both ordinary Go entry points index a local Anvil chain through height 9. The negative and positive handlers write six distinct primary keys. Reopening the same PostgreSQL schema or PGlite directory preserves all six and a repeated insert would fail. [Compiled and direct runs](../../../runtime/compiled_indexer_test.go)
- The independent-store test runs two Anvil chains and alternates which chain has its cache enabled. Intervals −5 and −2 coincide at height 6. Another source uses −1 over a short range. A callback fails after a business write at the shared height. The reopened job verifies the committed prefix, replays the interrupted group in order, and checks the final empty heights and recovered cursors on both chains. A further reopening checks completed progress. [Transaction and recovery](../../../historical/omnichain_block_intervals_test.go)
- The gap proof independently rejects omission of a scheduled height. Admission rejects zero, fractional and unsafe periods. The signed Go helper covers the minimum and maximum `int64` values. [Schedule checks](../../../store/managed_live_block_schedule_test.go) · [Admission cases](../../../historical/live_runtime_omnichain_logs_capabilities_test.go)

On the preceding main revision the Go source conversion rejected −3 and the dual-store Omnichain preflight rejected the signed block schedule. The test source inventory retains its existing 883 mappings.

The focused ordinary and race cases passed on PostgreSQL and PGlite. Initial PGlite or compiler processes were killed under host memory pressure. Serial reruns with fresh PGlite directories completed after reclaiming rebuildable compiler cache. The positive-interval PostgreSQL regression and the existing log, account and CALL receipt checks passed. The whole module compiled and passed vet. The verification tools passed 503 Node and 43 Python cases. Full ordinary and race module runs are tracked separately.

Use Go 1.25.11, Anvil 1.8.5, a dedicated PostgreSQL 14.24 database, the PGlite bridge and fresh absolute test roots for the database runs.

```sh
selection='^(TestBlockPeriod|TestCompiledSignedBlockIntervals|TestRuntimeOmnichainSignedBlockIntervals|TestRuntimeOmnichainStaticLogsCapabilities|TestSparseBlockScheduleEvidence|TestSignedBlockScheduleEvidence)$'
for flags in '' '-race'; do
  go test $flags -p 1 -v -count=1 -timeout=40m -run "$selection" ./config ./store ./runtime ./historical
done
```

The pinned Ponder 0.18.2 matcher returned heights 2, 5 and 8 for both signed divisors and every height from 2 through 9 for −1. Its two original block-source and filter tests also passed. The direct invocation exercises its actual filter function on a negative interval. [Upstream builder](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/build/config.ts#L875-L900) · [Upstream matcher](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/runtime/filter.ts#L594-L604)

```sh
cd ../ponder-upstream/packages/core
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/build/config.test.ts src/runtime/filter.test.ts -t 'buildIndexingFunctions\(\) block source|isBlockFilterMatched'
node_modules/.bin/tsx -e 'import {isBlockFilterMatched} from "./src/runtime/filter.ts"; for (const interval of [-3,3,-1]) { const f={interval,offset:2%interval,fromBlock:2,toBlock:9}; console.log(interval,Array.from({length:11},(_,n)=>n).filter(n=>isBlockFilterMatched({filter:f,block:{number:n}}))); }'
```
