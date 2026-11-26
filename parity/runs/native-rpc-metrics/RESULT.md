# RPC measurements from a Go indexer

Ponder 0.18.2 starts its request clock after selecting an endpoint. It observes duration on success and increments the error counter for each failed attempt, before deciding whether to retry. The success histogram uses milliseconds and both series use the configured chain name and RPC method. [Pinned request implementation](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/rpc/index.ts#L475-L570) · [Histogram and counter definitions](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L421-L436)

The Go transport test observes two failures and one success when a custom asynchronous request sees a connection error, a retryable null block, and then a block. The cache test checks a permanent method error, malformed JSON, uncached null results, and a cache hit on a repeated successful request. The registry test injects a 4.5 ms sample, verifies the first two buckets, sum and count, and resets both series. [Transport cases](../../../rpc/custom_transport_test.go) · [Registry cases](../../../metrics/registry_test.go)

The application test starts two Anvil chains and scrapes the writer's HTTP endpoint. It checks both chain names after backfill. A live callback makes a successful request and calls an unknown method on each chain. Both become visible through the same endpoint while the historical timing samples retain their earlier values. The independent-store CLI test also scrapes the PostgreSQL writer after it indexes blocks from both nodes. Its requester factory builds fresh transports with the configured chain names. [Application test](../../../runtime/historical_metrics_test.go) · [Independent-store CLI test](../../../cmd/ponder/managed_start_test.go)

## Reproduction

Run from the Go repository with the test Anvil binary, PostgreSQL and PGlite available. The upstream tests run from the pinned checkout's `packages/core` directory.

```sh
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 -run '^(TestRPCDurationBuckets|TestSourceMetrics|TestHistoricalDurations|TestSourceTransport|TestSourceConfigTransport|TestApplicationHistoricalTimings)$' ./metrics ./rpc ./config ./runtime
  go test $flags -p 1 -count=1 -run '^TestManagedMultichainCLIStart$/^postgres$' ./cmd/ponder
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/rpc/index.test.ts src/internal/metrics.test.ts
```

Go 1.25.11 passed the transport, configuration and registry selections in ordinary and race modes. The running HTTP writer passed all three orderings on PostgreSQL 14.24 and PGlite 0.2.13 in each mode. The independent PostgreSQL writer passed its CLI case in both modes. During the first concurrent race run, the workspace killed PGlite processes under memory pressure. Rerunning the individual cases with the compiled race binary verified the three PGlite results. All 11 selected upstream cases passed. The existing Go mapping count is 883. Whole-module ordinary and race execution is tracked separately.
