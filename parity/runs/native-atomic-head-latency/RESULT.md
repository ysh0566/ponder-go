# Realtime head latency on the atomic Go indexer

The single-chain engine, independent chain workers, and the shared Multichain and Omnichain schedulers observe the two existing head-latency histograms after their initial catch-up. A sample starts on a successful latest-block RPC and ends only after the matching block’s writes and completion marker succeed. Catch-up remembers the last hash even while timing is disabled, so a peer replay on the first live poll cannot generate a fresh head sample. Each poller keeps at most one tip per chain, in memory for the current indexing run. An unchanged tip retains its original receipt time while waiting for the global watermark. A different tip supersedes it. If the by-height fetch returns a different hash, committing that block cancels the original observation. An already indexed block, a gap fill, or a peer block replayed during a reorg has no new head acknowledgement. A fresh writer starts with an empty registry. [Polling and commit paths](../../../runtime/engine.go) · [Cross-chain scheduling](../../../runtime/ordered_indexer.go) · [Usage](../../../docs/USAGE.md#查看实时区块延迟)

The histograms use the existing Ponder names, labels and millisecond buckets. The registry still matches 14 of the 39 named upstream families. [Pinned definitions](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts#L351-L374) · [Dual-store verification](../native-realtime-head-latency/RESULT.md)

## Checks

- Anvil and HTTP scrapes cover the three ordering modes on PostgreSQL and PGlite. Initial catch-up produces no latency samples. Both chains receive arrival and processing samples once their live heads can commit. [Application tests](../../../runtime/historical_metrics_test.go)
- The poller test covers an unchanged head, a superseded hash at the same height, a gap block, a completed ancestor and a bounded range. A single-chain handler writes a row then returns an error. Both databases roll back the row and neither histogram records the head. [Poller and handler tests](../../../runtime/polled_heads_test.go)
- The provider failover test replaces chain A’s branch while preserving chain B’s canonical blocks. The writer records one sample in each histogram for A and none for B. Cold reopening starts with no head samples. [Fork tests](../../../runtime/mixed_source_reorg_test.go)

```sh
go test -count=1 -run '^(TestApplicationHistoricalTimings|TestAtomicPolledHeads|TestAtomicHandlerFailureHasNoHeadSample|TestMixedSourceProviderReorg)$' ./runtime
go test -race -count=1 -run '^(TestApplicationHistoricalTimings|TestAtomicPolledHeads|TestAtomicHandlerFailureHasNoHeadSample|TestMixedSourceProviderReorg)$' ./runtime
```

All four Go test selections passed in ordinary and race modes. All 50 Go packages compiled and `go vet ./...` passed. Ten pinned upstream metric, realtime sync and reorg controls passed. Repository tooling passed 503 Node tests and 43 Python tests. The native head samples above are supplemental Go checks. The full module-wide ordinary and race executions run separately.
