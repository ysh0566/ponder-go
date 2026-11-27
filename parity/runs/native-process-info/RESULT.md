# Go process information on /metrics

The Go CLI and the launched application share the release string `0.18.2-go.0`. Each launched application's registry publishes two gauges with value 1. The version sample includes the full Go release and its three numeric components. The settings sample reports the command and the business store's actual dialect. Writers also report the configured ordering. A standalone reader reports the database and command. An indexing reset clears the settings sample and preserves the version. [Go registry](../../../metrics/process.go) · [Application startup](../../../runtime/application.go) · [Scrape examples](../../../docs/USAGE.md#查看版本与运行配置)

## Verification

Run from the Go checkout with Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and the PGlite 0.2.13 bridge. The standalone CLI reader test starts a private PostgreSQL server using `PONDER_TEST_POSTGRES_BINDIR`.

```sh
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 -v ./metrics
  go test $flags -p 1 -count=1 -v -run '^(TestApplicationHistoricalTimings|TestApplicationDevMetrics|TestApplicationServeOnly)$' ./runtime
  go test $flags -p 1 -count=1 -v -run '^(TestManagedMultichainCLIStart|TestGlobalLogOptions)$' ./cmd/ponder
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The reference uses the pinned Ponder 0.18.2 sources at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Run these commands from the upstream `packages/core` directory. Set `PONDER_GO_ROOT` to the Go checkout.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/internal/metrics.test.ts
node_modules/.bin/tsx --tsconfig tsconfig.json "$PONDER_GO_ROOT/parity/runs/native-process-info/reference.cjs"
```

Three original metric controls passed. The supplemental comparison extracts the two gauge-setting calls from each pinned CLI command and executes them with the upstream options parser and metric registry, supplying the Go release string for comparison. It checks the labels, value and reset behavior for 12 writer configurations and the PostgreSQL reader. The 13 comparisons are supplemental and add no source-case mappings. [Reference comparison](reference.cjs) · [Upstream registry](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/internal/metrics.ts)

The 23 distinct native leaf cases in the metric, runtime and CLI selections passed in ordinary and race modes. The writer tests scraped all three atomic orderings on each backend. The dual-store CLI tests inspected a writer on each backend and an independent PostgreSQL reader. The development test examined the registry attached to the running Go application.

The ordinary dual-store CLI process initially reached the host memory limit while opening its PGlite engines. After archiving verified compiler-cache entries, the isolated PGlite run passed. The race CLI run split PostgreSQL and PGlite into separate test processes. The PostgreSQL cases include the standalone read-only login.

```sh
go test -p 1 -count=1 -v -run '^TestManagedMultichainCLIStart$/^pglite$' ./cmd/ponder
go test -race -p 1 -count=1 -v -run '^(TestManagedMultichainCLIStart|TestGlobalLogOptions)$/^(postgres|postgres-serve)$' ./cmd/ponder
go test -race -p 1 -count=1 -v -run '^TestManagedMultichainCLIStart$/^pglite$' ./cmd/ponder
```

The direct query-only PGlite application also exposed the `serve` sample with no CLI command in the environment. Its assertion passed individually in ordinary and race builds. All 51 Go packages compiled and passed vet. Repository tooling passed 503 Node and 43 Python checks. The full-module ordinary and race regressions use the separate exact-commit queue.
