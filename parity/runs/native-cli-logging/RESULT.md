# Go CLI log levels and JSON diagnostics

The Go CLI parses global log options before and after the command, respects an explicit level over trace/debug shortcuts and inherited environment, and separates diagnostics on stderr from machine output on stdout. Numeric JSON levels and epoch millisecond time correspond to the pinned logger. The human-readable formatter uses Go text fields. Generated Go projects create a logger using the effective settings passed to their child process.

Tests start real Go programs through dev, start and serve, observe a dev revision, capture compiler errors, parse JSON diagnostics and check output under silent and error filtering. An independently generated PostgreSQL application starts in serve mode, emits its address and namespace as a structured info record, and serves live queries through an insert and rollback. The logger implements the existing runtime debug/warn interfaces and provides a trace record for the generated ERC-20 callback. Independently authored applications can consume the environment or use their own logging.

```sh
go test -count=2 ./logging ./cmd/ponder -run 'Test(RuntimeLogRecords|GlobalLogOptions|GoProcessLogSettings|DevProcessLogSettings|GeneratedGoServe|GeneratedGoStartViews|DevReload|InitGoProject|LaunchNamespaces|SourceNamespaces)$'
go test -race -count=2 ./logging ./cmd/ponder -run 'Test(RuntimeLogRecords|GlobalLogOptions|GoProcessLogSettings|DevProcessLogSettings|GeneratedGoServe|GeneratedGoStartViews|DevReload|InitGoProject|LaunchNamespaces|SourceNamespaces)$'
```

[Options](../../../cmd/ponder/logging.go) · [Logger](../../../logging/logger.go) · [Generated indexer](../../../examples/erc20/main.go)

An independent run imported buildOptions and createLogger directly from the pinned Ponder 0.18.2 sources using its installed tsx. The option precedence matched for inherited warn, debug, trace and an explicit error level. Five original JSON records contained numeric levels 10, 20, 30, 40 and 50, a millisecond timestamp, message and service field.

The focused ordinary and race selections both passed twice. Additionally, `go test -count=1 ./logging ./cmd/ponder` and its `-race` counterpart passed the complete two packages with separate task-owned business and pruning databases. All Go packages compiled with `go test -run '^$' ./...`, `go vet ./...` and 503 repository tooling checks passed. The full-module source fingerprint includes the new logging package.
