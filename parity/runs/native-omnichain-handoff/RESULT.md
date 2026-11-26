# Omnichain source history and live handoff

The Go source iterator passes the historical coordinator's pending events into live RPC polling. Consumers receive checkpoint-ordered events for historical and live blocks, a global rollback checkpoint on a shallow fork, and a global finality checkpoint as all chains advance. Exiting the iterator cancels and joins source workers.

The handoff test starts from the original two-chain pending fixture for each database engine. The captured historical result has three chain 2 callbacks above the common finalized checkpoint at the end of backfill. Reversed source registration and tied timestamps check ordering. Reconciled live inputs then trigger a fork on chain 1. The consumer sees the surviving chain 2 suffix again when it reaches the replacement chain 1 block. Another test polls two Anvil 1.8.5 nodes through the default Go RPC path, processes the configured end heights and checks the six emitted callbacks. A live Anvil snapshot revert also checks delivery of the old block, its canonical ancestor and the replacement at the same height. Stream termination checks cover a finite historical tail, early EOF, a provider error with a stalled peer, and cancellation.

```sh
selection='^(TestSourceOmnichainStream.*|TestSourceHistoryCompletionMerge|TestSourceHistoryAsyncTermination|TestSourceHistoryFinalizedModeBoundaries)$'
go test -p 1 -v -count=1 -timeout=10m -run "$selection" ./runtime
for test in Handoff Polling; do
  for backend in postgres pglite; do
    go test -p 1 -v -count=1 -timeout=10m -race -run "^TestSourceOmnichainStream${test}$/^${backend}$" ./runtime
  done
done
go test -p 1 -v -count=1 -timeout=10m -race -run '^(TestSourceOmnichainStream(PolledFork|Termination)|TestSourceHistoryCompletionMerge|TestSourceHistoryAsyncTermination|TestSourceHistoryFinalizedModeBoundaries)$' ./runtime
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

Go 1.25.11 passed the same 19 leaves in ordinary and race mode, including all nine stream leaves, with zero skips. Early combined attempts lost a PGlite subprocess under memory pressure. The final run moved compiler artifacts out of shared RAM and ran each integration/backend pair in its own race process. All 50 packages compiled and passed go vet. Repository verification passed 499 Node cases, four loopback cases and 43 Python cases.

The original Ponder 0.18.2 historical coordinator suite passed all 14 tests. Run in the pinned upstream packages/core directory.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/historical.test.ts
```

These are supplemental native tests. The separate business and sync database runtime still requires finalized Omnichain ranges. Full-module ordinary and race results for this revision are pending.
