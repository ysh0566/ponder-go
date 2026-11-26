# Engine interruption before finalized Omnichain publishes readiness

A Go storage integration test bootstraps two chains, writes one business row for each through the actual batch writer, and seals the runtime scope. Both chains have processed their finite end block. The test calls the same `ActivateRuntime` used by the historical runner for block sources. It uses an actual secondary index as an independent check on transactional DDL. [Storage tests](../../../store/managed_live_omnichain_activation_engine_test.go) · [Activation transaction](../../../store/managed_live_runtime_activation.go)

The test observes the pending transaction immediately before forwarding its commit. The transaction already contains the index, the application readiness flag, the serving control and the sealed operation receipt. A private PostgreSQL 14.24 server is stopped in immediate mode, or the test terminates the PGlite 0.2.13 child for exactly one directory. The database driver supplies the commit outcome. [Engine controls](../../../internal/testpostgres/server.go) · [PGlite child ownership](../../../internal/testpglite/engine.go)

| Engine stopped | Business transaction | Writer and recovery |
| --- | --- | --- |
| Shared PostgreSQL server | The cold database has the previous control and neither the index nor the activation receipt. | Readiness stays false while offline. Both rows and their checkpoints survive. |
| PGlite business engine | Opening the same directory shows the previous control, with the DDL and receipt rolled back. | Readiness stays false while offline. Both rows and their checkpoints survive. |
| PGlite sync engine | The business engine commits and retains the sealed activation receipt across reopening. | The writer cannot report ready while its sync engine is offline. Both rows and their checkpoints survive. |

Every case reopens both original stores with a fresh owner ID. The new generation starts unready, retains the completed batches and safe positions, activates with a new receipt and reports ready. This exercises finalized block sources. The separate activation path that persists pages of log-only scan evidence, and recovery from host power loss, still need acceptance.

## Reproduction

Set up the PostgreSQL and PGlite test prerequisites documented in the repository. Use a dedicated local PostgreSQL binary directory via `PONDER_TEST_POSTGRES_BINDIR` or a preloaded image via `PONDER_TEST_POSTGRES_ENGINE_IMAGE`. The test creates its own PostgreSQL cluster and PGlite directories.

```sh
go test -count=1 -v -run '^TestOmnichainActivationEngineInterruption$' ./store
go test -race -count=1 -v -run '^TestOmnichainActivationEngineInterruption$' ./store
```

## Verification results

The three engine interruptions passed in ordinary and race mode. The normal activation controls on PostgreSQL and PGlite and the historical runner’s PostgreSQL closing-block interruption also passed in both modes.

At upstream revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, six database recovery and revert tests passed. The Vitest filter was `crash recovery|revert\(` in `src/database/index.test.ts` and `src/database/actions.test.ts`. These verify the upstream recovery semantics. The three engine interruptions above are supplemental Go cases. Repository tooling passed 503 Node tests and 43 Python tests.
