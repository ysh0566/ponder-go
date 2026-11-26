# Database interruption during finalized log-only activation

A two-chain storage integration test calls the paged activation entry point used by the historical Go runner. Each chain commits a business row and a log checkpoint at height 4. Its cached log filter covers heights 4 through 260. The fixture supplies linked headers through the finality anchor at height 261. For each chain, an activation stores a 256-header page, one final scanned header and a separate page for the finality anchor. [Historical caller](../../../historical/live_runtime_omnichain_history.go) · [Activation transaction](../../../store/managed_live_runtime_activation.go) · [Page assertions](../../../store/managed_live_omnichain_activation_pages_test.go)

The test interrupts the selected engine after the first successful page insert or immediately before forwarding the transaction's commit. At the first point it reads one full page in the real business transaction while the receipt, index and readiness flag are still absent or unset. At the second point it reads all six pages, both scan heads, both business rows, the index and the sealed receipt. The PostgreSQL server stops in immediate mode. For PGlite, exactly the child serving the selected directory is terminated. [Engine tests](../../../store/managed_live_omnichain_activation_engine_test.go)

| Interrupted engine | Cold database and recovery |
| --- | --- |
| Shared PostgreSQL or PGlite business engine | No page or activation receipt from the interrupted attempt survives. The source checkpoints and both previously committed rows survive. A new owner rebuilds the pages and publishes readiness. |
| PGlite sync engine | The business transaction can finish. Its six pages and receipt survive reopening. The writer reports unready while sync storage is unavailable. The new owner opens unready, verifies the stored scan heads and can activate again. |

Each recovered activation checks every stored header against the fixture, the durable source and safe checkpoints, and the separate scan endpoint. The historical runner has a companion test that processes static logs through RPC, interrupts the database in a handler, and cold-replays the unfinished callback. Host power loss and unfinalized dual-store Omnichain remain open.

## Reproduction

Use the repository's PostgreSQL and PGlite test prerequisites. Set `PONDER_TEST_POSTGRES_BINDIR` to a local PostgreSQL server installation for the private interruption tests.

```sh
go test -count=1 -v -run '^(TestOmnichainLogOnlyActivationEngineInterruption|TestOmnichainActivationEngineInterruption|TestManagedLiveRuntimeActivation)$' ./store
go test -race -count=1 -v -run '^(TestOmnichainLogOnlyActivationEngineInterruption|TestOmnichainActivationEngineInterruption|TestManagedLiveRuntimeActivation)$' ./store
go test -count=1 -v -run '^TestOmnichainEngineInterruption$/postgres/log-only$' ./historical
go test -race -count=1 -v -run '^TestOmnichainEngineInterruption$/postgres/log-only$' ./historical
```

## Verification

The six scan-page interruptions passed in ordinary and race modes. In the first race run, two block-mode controls lost a PGlite process during initial database opening. All six new interruptions passed in that run. A separate uncached rerun of the full store selection passed. Both modes also passed the block activation, normal readiness and historical static-log controls. All 50 packages compiled with `go test -run '^$' ./...` and passed `go vet ./...`. The repository checks passed 503 Node tests and 43 Python tests. At pinned upstream revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, six original database controls passed with the Vitest filter `crash recovery|revert\(` in `src/database/index.test.ts` and `src/database/actions.test.ts`. The Go engine interruptions are supplemental recovery tests. The module-wide ordinary and race regressions run separately.
