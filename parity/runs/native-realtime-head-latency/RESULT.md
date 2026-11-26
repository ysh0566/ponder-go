# Latency of accepted realtime heads

The managed Go realtime source starts a timer when it receives a polled head. After the dual-store writer commits the business transaction, it acknowledges that head. A standalone consumer of the built-in Omnichain polling stream can also acknowledge the block explicitly. Reconciliation supplies the acknowledgement only for the observed head. Gap blocks, duplicates, reorg notifications, historical events and rejected heads produce no samples. The atomic `runtime.NewIndexer` uses a separate polling loop. Instrumenting its head latency remains open.

| Histogram | Sample in milliseconds |
| --- | --- |
| `ponder_realtime_block_arrival_latency{chain}` | Wall-clock receive time minus the block timestamp. A chain clock ahead of the writer can yield a negative sample. |
| `ponder_realtime_latency{chain}` | Elapsed time from receipt to successful acknowledgement. This includes processing and waiting for the writer. |

The pinned upstream declares both histograms with the same chain label and 13 finite buckets, from 1 to 1,000,000 ms. It observes them inside the accepted-head callback and retains them across indexing metric resets. With these two names the Go writer registers 14 of the 39 explicitly named upstream metric families. [Upstream registry](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L351-L374) · [Accepted head](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/runtime/realtime.ts#L823-L856)

## Verification

- The registry test checks millisecond units, negative arrival latency, bucket boundaries, rejection, duplicate acknowledgements and indexing reset. [Registry tests](../../../metrics/registry_test.go)
- Two chains advance to heights 2 and 4. The dual-store writer commits four blocks per chain and observes two heads per chain. Reopening replays all four blocks and adds one accepted head per chain. A separate handler writes a row and then returns an error before commit. Neither histogram receives a sample. The tests use PostgreSQL and PGlite. [Continuous runner](../../../historical/live_runtime_continuous_test.go)
- Two Anvil chains with distinct historical cuts feed the polling stream. The consumer accepts one chain’s head and rejects the other. The historical events and gap blocks still arrive in checkpoint order. [Omnichain source stream](../../../runtime/source_omnichain_stream_test.go)
- A CLI writer exposes both histograms through HTTP after indexing a newly mined block. The separate PostgreSQL reader has its own process registry. [CLI exporter](../../../cmd/ponder/managed_start_test.go)

```sh
go test -count=1 -run '^(TestRealtimeBlockLatency|TestRuntimeContinuousFutureEnd|TestRuntimeFailedHeadLatency|TestSourceOmnichainStreamPolling|TestManagedMultichainCLIStart)$' ./metrics ./historical ./runtime ./cmd/ponder
go test -race -count=1 -run '^(TestRealtimeBlockLatency|TestRuntimeContinuousFutureEnd|TestRuntimeFailedHeadLatency|TestSourceOmnichainStreamPolling|TestManagedMultichainCLIStart)$' ./metrics ./historical ./runtime ./cmd/ponder
```

At upstream revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, seven original metric and realtime sync controls passed. The selection included duplicate, missing and failed block handling. The latency assertions above are supplemental Go tests.

The listed Go selections passed in ordinary and race mode on both database backends, including the separate PostgreSQL reader. All 50 Go packages compiled and `go vet ./...` passed. Repository tooling passed 503 Node tests and 43 Python tests. Module-wide ordinary and race regressions run separately.
