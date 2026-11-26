# Application exit with independent Omnichain storage

A child Go process opens a fresh independent business/sync deployment and exits with code 83 inside a handler after writing, or after readiness activation. The parent checks the PID and backend in the marker, the exit status and the actual callback prefix. Each test reopens the same deployment with a fresh owner. PostgreSQL retains its running server and PGlite reopens the two data directories.

The block-only cases compare recovery against the pinned global callback order, durable rows and cursors. A second completed open receives zero callbacks. The static contract log cases cover default and small SQL pages. They compare the pre-recovery committed prefix with the source fixture, the cold replay suffix, safe, latest and finalized positions, final business rows and every callback on a second completed open.

## Verification

```sh
for backend in postgres pglite; do
  selection="^TestRuntimeOmnichain(StaticLogs)?ApplicationExit$/^${backend}$"
  go test -v -count=1 -timeout=45m -run "$selection" ./historical
  go test -race -v -count=1 -timeout=45m -run "$selection" ./historical
done
go test -v -count=1 -timeout=45m -run '^TestRuntimeContinuousFutureEnd$' ./historical
go test -race -v -count=1 -timeout=45m -run '^TestRuntimeContinuousFutureEnd$' ./historical
go test -run '^$' ./...
go vet ./...
```

Each Go mode passed six application-exit scenarios per backend. The static-log selections use SQL query sizes 6 and 12,000. The PGlite block-handler case passed again after integration with the custom source transport changes. Module compilation, vet and 503 verification-tool tests passed.

The default multichain future-end control passed on both backends in both modes. Each chain starts at height two and advances to the configured end at height four. The reopened deployment recovers from its safe checkpoint, replays and reaches the same final business balances. This confirms the documented live-polling configuration for finite bounds that do not yet exist.

Six pinned upstream cases passed with `vitest run --maxWorkers=1 --hookTimeout=45000 src/database/index.test.ts src/database/actions.test.ts -t 'crash recovery|revert\('`. Twenty-three tests fell outside the selection. The initial upstream startup could not bind its local port while the source-configuration oracle was running. The rerun used the same tests after the listener was released. These checks add no upstream case mappings.
