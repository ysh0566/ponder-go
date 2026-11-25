# Native Go development terminal measurements

A generated ERC-20 Go application runs in a real pseudo-terminal against a local Anvil 1.8.5 node and PostgreSQL 14.24. After a contract emits a Transfer log, the terminal shows the committed block, a positive RPC rate, 100% backfill, one successful callback and a positive mean duration. The same fixture exercises hot reload, Ctrl+C, output redirection, JSON logging and disabled UI.

Two Anvil chains share a native multi-chain indexer. A deliberately paused RPC shows one of two blocks complete before readiness on PostgreSQL and PGlite 0.2.13. After catch-up, the chain totals are two and three, and the shared callback count is five. Starting a new worker over the same indexed data keeps both backfills at 100% and counts zero callbacks. The runtime also checks that rewinds reset the appropriate estimates. A deterministic clock series checks that five completed blocks per five-second interval gives a forty-second ETA for forty remaining blocks after two intervals. The rate fixture sends six real requests over a two-second sampling interval, excludes a request issued before the watcher started, and ages those six requests out of the 100-sample window. Transport tests count a real HTTP 503 and its successful retry and verify that a cache hit adds no request.

| Coverage | Command |
| --- | --- |
| Native CLI, including the generated application under a pseudo-terminal | `go test -count=1 ./cmd/ponder` and the same with `-race` |
| Native counters, database progress and existing workers | `go test -count=1 -run '^(TestDevMetricSamples\|TestApplicationDevMetrics\|TestApplicationDevCheckpoints\|TestOrderedIndexer\|TestDefaultIndexer\|TestIsolatedIndexer\|TestRetryAndCache\|TestInvalidParamsIsNotRetried\|TestContinuousFinality)$' ./runtime ./rpc` and the same with `-race` |
| Final terminal and dual-backend selection | `go test -count=1 -run '^(TestDevProgressLayout\|TestGeneratedDevTerminal\|TestDevMetricSamples\|TestApplicationDevMetrics\|TestApplicationDevCheckpoints\|TestRetryAndCache)$' ./cmd/ponder ./runtime ./rpc` and the same with `-race` |
| Pinned upstream | Core Vitest `src/internal/metrics.test.ts` (three original cases), plus direct assertions on `getSyncProgress`, `getIndexingProgress`, `getAppProgress` and the real terminal renderer with known metric samples |
| Module checks | `go test -run '^$' ./...`, `go vet ./...` and repository verification tool self-tests |

The CLI selection contains 25 leaf cases, the runtime and RPC selection contains 15, and repository verification ran 503 tool checks. Native ETA uses completed block intervals, the work unit used by this indexer. The upstream application ETA uses indexed chain time. Each uses recent observed work, excludes cached work from measured speed and waits for multiple samples before reporting an estimate. Native regression tests do not change upstream mapping counts.
