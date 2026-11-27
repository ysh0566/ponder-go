# Predictive RPC prefetch counts

The Go writer exports `ponder_indexing_rpc_prefetch_total` for contract reads predicted by the indexing client. It counts deduplicated database candidates when an attached cache begins a lookup. An enabled cache exposes zero for an empty batch. A disabled or absent cache does not perform a database lookup. Each admitted speculative request increments the RPC series before completion. Ordinary callback reads and consuming prefetched results do not increment either series.

## Verification

The fixed original cached and uncached fixtures exercise real PostgreSQL and PGlite sync caches and HTTP transports. With a cached result the database series increments once. With storage disabled the RPC series increments once. Native lifecycle cases cover a failed database lookup with a pending lookup on another chain, duplicate predictions, an empty batch, a failed background request and persistence through an indexing metrics reset.

```sh
go test -count=1 -run '^Test(ClientPrefetchCloseOwnsFailedSibling|ClientPrefetchReaderSingleSettlement|SourceIndexingPrefetch|SourceIndexingPrefetchProbes|ClientProfileServiceContext)$' ./indexing
go test -count=1 ./metrics
```

Both the ordinary and race runs passed all 66 selected Go cases. Each run covered 24 cases on PostgreSQL, 24 on PGlite, two client lifecycle cases and 16 registry cases. The two pinned upstream tests passed. Two supplemental assertions on the upstream implementation checked the samples for `mainnet`. A cached result recorded one `database` prefetch. A read with caching disabled recorded one `rpc` prefetch.

All 51 Go packages compiled and `go vet ./...` passed. The repository tooling passed 503 Node checks and 43 Python checks. The Go registry exposes 34 of the 39 metric families named in Ponder 0.18.2.

[Upstream admission logic](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/indexing/client.ts#L730-L782) · [Backend fixtures](../../../indexing/source_prefetch_test.go) · [Lookup and RPC lifecycle](../../../indexing/client_prefetch_lifecycle_test.go)
