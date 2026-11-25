# Source checkpoints and genesis in bounded default indexing

Set `RuntimeOptions.SourceCheckpoints` on an independent business/sync deployment to use the source initial position without live polling. The finite acquisition check still rejects an end block beyond the current head. Both scheduling modes with source checkpoints use the current source-position storage format and its authenticated recovery rules.

The fixture supplies deterministic headers over HTTP on two chains with separate business and sync storage. PostgreSQL 14.24 and PGlite 0.2.13 each cover four bounded scenarios.

- Begin and end at block zero with setup on both chains.
- Begin at zero and end at two with an unfinalized suffix. The initial cursor uses the same genesis start and finalized head as the captured genesis control. The test separately checks the extended range.
- Start at one without setup and reopen the completed range.
- Use different start and end blocks on the two chains and verify each chain’s initial public checkpoint.

The first run checks exact callback heights, setup counts and the initial public positions. A new owner reopens the same stores and verifies recovery and the final business balances. The pinned checkpoint values for the bootstrap controls are in `historical/testdata/source-initial-17c6d5ba.json`. The native acceptance cases leave the upstream declaration count unchanged.

## Verification

The bounded suite covers four ranges per backend, eight default preflight cases and eight Omnichain preflight cases. The continuous controls exercise the shared assertions at genesis and with an empty event range. Each invocation uses its own test databases and PGlite directories.

```sh
bounded='^(TestBoundedSourceCheckpoints|TestRuntimePreflightRejectsUnsupported|TestRuntimeOmnichainPreflightBoundary)$'
controls='^TestRuntimeSourceInitialMatrix$/^(postgres|pglite)$/^(genesis|empty_events)$'
for selection in "$bounded" "$controls"; do
  go test -v -count=1 -timeout=45m -run "$selection" ./historical
  go test -race -v -count=1 -timeout=45m -run "$selection" ./historical
done
go test -run '^$' ./...
go vet ./...
```

The upstream checkpoint control is `vitest run --maxWorkers=1 src/runtime/index.test.ts -t 'getLocalSyncProgress\('` on pinned revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

Ordinary and race runs each passed 24 bounded and preflight cases and four continuous controls. The upstream checkpoint control passed both selected tests. On the revision including coordinated engine-interruption tests, the PostgreSQL genesis case passed again, every Go package compiled, `go vet ./...` passed and the repository verification tools passed 503 tests.
