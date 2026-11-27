# Go callback execution time

The writer observes each completed callback in `ponder_indexing_function_duration{chain,event}`. The histogram uses the 17 upstream finite buckets from 0.001 to 100,000 milliseconds. The timer covers the callback invocation and can record setup, history, live or replay work. It records executions independently of the enclosing transaction's eventual commit. An explicit indexing reset clears the histogram.

The pinned upstream declares both labels and its callback dispatcher supplies the event name. The Go writer includes the configured chain name on each sample so two chains sharing a callback can be queried independently. The native managed service preserves the upstream order for a callback that catches a retryable error. Setup checks the stored error before observing. An event that returns successfully is observed before the service checks that error. [Histogram declaration](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L231-L238) · [Callback execution](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/indexing/index.ts#L199-L301)

## Native checks

| Check | Expected observation |
| --- | --- |
| Registry | Two sub-millisecond samples total 0.3 ms. The bucket at 0.05 ms contains one sample. Reset removes the series. |
| Two-chain service | Two setup executions on chain one. Three transfers split two on chain one and one on chain two. An error returned by the callback adds zero samples. |
| Atomic writer HTTP endpoint | The first catch-up exposes callback timings. After live mining on two Anvil chains, the exporter reports at least four block callbacks per chain under Multichain, Isolated and Omnichain ordering. |
| Source page with a failing callback | The first attempt fails after a business write, the retry succeeds, and the duplicate page skips execution. Exactly one timing is recorded with either RPC cache setting. |
| Independent business and sync stores | Each chain executes four block callbacks on the first run and four more on cold replay, while each business balance finishes at four. A rejected head adds zero callback or realtime latency samples. |

The database controls use PostgreSQL 14.24 and PGlite 0.2.13. The original upstream file at the pinned revision and the existing Go port use these four case identities.

| Upstream case in `packages/core/src/indexing/index.test.ts` | Line | Existing Go case |
| --- | ---: | --- |
| `executeSetup() metrics` | 599 | `968bb8a7d0476d79c8fb` |
| `executeSetup() error` | 663 | `d75ae6d953a7bb8ea73c` |
| `processEvents() metrics` | 908 | `26932124c42fde4c8e04` |
| `processEvents() error` | 985 | `afb6150d1f928a58bf24` |

```bash
cases='^(TestHandlerDuration|TestSourceMetrics|TestDevMetricSamples|TestServiceSupplementalTwoChainIdentityAndSetup|TestServiceSupplementalStickyErrorTiming|TestRuntimeContinuousFutureEnd|TestRuntimeFailedHeadLatency|TestApplicationHistoricalTimings|TestSourcePageRuntime)$'
ports='^TestSourceIndexingService$/(postgres|pglite)/(968bb8a7d0476d79c8fb|d75ae6d953a7bb8ea73c|26932124c42fde4c8e04|afb6150d1f928a58bf24)$'
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 -v -timeout=1h -run "$cases" ./metrics ./indexing ./historical ./runtime
  go test $mode -p 1 -count=1 -v -timeout=1h -run "$ports" ./indexing
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The following command runs the selected tests in the pinned upstream core package.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/internal/metrics.test.ts src/indexing/index.test.ts -t 'indexing metrics|completed event metrics|^(executeSetup|processEvents)\(\) (metrics|error)$'
```

Seven selected original cases passed on each upstream backend, PostgreSQL and PGlite, including all three metric flushing/reset controls. The exporter checks above are supplemental. The Go writer now registers 15 of the 39 named upstream families. [Coverage breakdown](../../../docs/RUNTIME_SUPPORT.md#prometheus-coverage)

The focused Go tests and all four selected ports passed in ordinary and race modes on both databases. All 50 Go packages compiled and vet passed. Repository tooling passed 503 Node and 43 Python checks. The module-wide ordinary and race runs use a separate exact-commit regression queue.
