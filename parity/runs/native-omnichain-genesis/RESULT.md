# Genesis in finalized Omnichain ranges

An independent business/sync deployment may start its closing-block or uniform log-only plan at block zero. Both modes use the existing source initial position at the start of the block. Every end must remain within local finality and below or at the global finalized watermark.

The two-chain native regression covers closing block callbacks at genesis, a closing-block plan extending from zero to three with overlapping static logs, a log-only plan over the same range, an empty log-only genesis and a log-only genesis containing a transaction and a matching log. The last case adds one coherent transaction and log per chain to the HTTP fixture. The expected callback projections for heights one through three come from the captured Ponder fixture. Expected genesis projections use the adjusted fixture headers and the checkpoint order by timestamp and chain ID.

Each deployment opens twice on the same business and sync storage. The assertions compare callback order, event checkpoints, decoded values, canonical cursor hashes, safe and finalized positions, business rows and the absence of retained undo entries. Both zero-height ranges and nonzero controls run on real database engines.

## Verification

For both PostgreSQL and PGlite the five genesis scenarios passed in ordinary and race builds. Both modes also passed the existing two PostgreSQL nonzero-start controls and 26 preflight cases. The first test projection conflated the identical numeric event types for logs and blocks. The final selection used the captured callback name to distinguish them.

```sh
for backend in postgres pglite; do
  go test -v -count=1 -timeout=45m -run "^TestOmnichainGenesis$/^${backend}$" ./historical
  go test -race -v -count=1 -timeout=45m -run "^TestOmnichainGenesis$/^${backend}$" ./historical
done
controls='^TestRuntimeOmnichain(StaticLogsSource|LogOnlySource)$/^postgres$/^small$'
preflight='^TestRuntimeOmnichain(PreflightBoundary|StaticLogsCapabilities)$'
for selection in "$controls" "$preflight"; do
  go test -v -count=1 -timeout=45m -run "$selection" ./historical
  go test -race -v -count=1 -timeout=45m -run "$selection" ./historical
done
go test -run '^$' ./...
go vet ./...
```

Seven pinned upstream cases passed and 22 fell outside the selection. The command was `vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/historical.test.ts src/runtime/index.test.ts -t 'getLocalEventGenerator\(|getHistoricalEvents\(\) omnichain|getLocalSyncProgress\('` from `packages/core`. All Go packages compiled, vet passed, and all 503 verification-tool tests passed. The selections add no upstream case mappings.
