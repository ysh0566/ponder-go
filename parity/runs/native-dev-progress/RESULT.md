# Go dev terminal and committed chain progress

The interactive CLI and generated Go indexer share an atomic status file inside the watcher's private build directory. The file reports the bound listener, application readiness and the committed database checkpoints used by `GET /status`. A custom application without a runtime reporter still displays its build and process status.

The terminal test uses a real pseudo-terminal, the generated Go project, a local Anvil chain and PostgreSQL. It mines a block, compares the live display with the HTTP status, introduces a Go syntax error, reads from the previous server during the failed build and starts a second revision. It checks partial child output, shutdown via SIGINT, progress-file cleanup, JSON output, explicit disable and redirected stdout. The layout tests cover constrained terminals, control characters, empty progress and unavailable checkpoints.

The application test reads successive atomic snapshots on PostgreSQL and PGlite and checks the actual ephemeral port, chain name and committed height. A failing custom readiness check changes the report while the committed height remains available. The pinned upstream renderer supplies the waiting, live and error states as a comparison. Its RPC, callback and estimated progress statistics remain outside this change. This milestone adds native coverage and leaves the upstream test inventory unchanged.

| Check | Command |
| --- | --- |
| Terminal and checkpoint tests | `go test -count=1 -run '^(TestGeneratedDevTerminal\|TestDevProgressLayout\|TestApplicationDevCheckpoints)$' ./cmd/ponder ./runtime` and the same selection with `-race` |
| Build and static analysis | `go test -run '^$' ./...` and `go vet ./...` |

All six selected top-level tests (terminal, layout, dev reload, custom readiness and the two HTTP server selections) passed in ordinary and race mode. The complete CLI package passed all 22 top-level tests in each mode with zero skips. Whole-module compilation and vet passed. The repository verification tools passed 503 checks. These are milestone results. The full ordinary and race suites on this snapshot are tracked separately.

## Native scope cross-check

The Go starter opens PostgreSQL explicitly, including in dev mode. Upstream pre-compilation chooses a PGlite directory for local runs when no PostgreSQL connection is configured. The Go database CLI opens only PostgreSQL. Upstream list, prune and view publication instead open the configured admin database, including the embedded driver. These are starter and offline administration gaps. Upstream explicitly exits with an error for a separate PGlite serve process. Cross-process PGlite serving therefore carries no upstream parity obligation. [Starter](../../../examples/erc20/main.go) · [Go database CLI](../../../cmd/ponder/database.go) · [Upstream database selection](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/build/pre.ts#L24-L83) · [Upstream database driver](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/database/index.ts#L222-L270) · [Serve restriction](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/bin/commands/serve.ts#L102-L109)
