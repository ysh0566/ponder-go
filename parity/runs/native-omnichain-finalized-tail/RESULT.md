# Finalized Omnichain ranges with different ending timestamps

The Go dual-store runner now finishes a finite job when each chain has finalized its own event range. Setup reads still require the common finalized checkpoint. Opening checks the requested end height and compares the complete header identity when it coincides with the local final head. The business store validates ordered writes and recovered cursors against the corresponding chain's opening finality.

The historical coordinator emits its ordinary groups through the common checkpoint. Once all chains have scanned their finite ends, the runner drains the remaining events in checkpoint order, one transaction per physical tail block. It authenticates each block against the locally finalized lineage before executing callbacks. Log-only jobs also authenticate scanned heights with no matching event. Tail receipts go through the same business and recovery transactions as the preceding historical groups.

## Verification

The RPC fixture holds both chains at height 3, with ending timestamps 130 and 140. The mixed-source fixture checks the full sequence of overlapping contract logs and closing block callbacks against the reference event IDs, hashes and values. A failure after several writes in the later block leaves all its writes uncommitted. On a cold open, the slow chain keeps its end and the remaining nine callbacks commit with one safe receipt. The third opening performs no callback. Separate sparse fixtures cover disjoint source ranges, reusable source data and an empty tail. Another case indexes and reopens both chains at genesis, where chain ID determines the order of equal timestamps.

```bash
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 -run '^TestRuntimeOmnichain(FinalizedTailRecovery|FinalityWait|LogRanges|SourceOracle)$' ./historical
  go test $mode -p 1 -count=1 -run '^TestRuntimeOmnichain(PreflightBoundary|FinalityWaitAdmission|SetupFinality|SetupLineage)$' ./historical
  go test $mode -p 1 -count=1 -run '^TestOmnichainGenesis$/./both_at_genesis' ./historical
  go test $mode -p 1 -count=1 -run '^Test(FinalizedSourceOmnichainAdmission|SourceOmnichainStream)' ./runtime
  go test $mode -p 1 -count=1 -run '^(TestOmnichainAtomicSafeReceiptBinding|TestLegacyLiveRejectsOmnichainWireFields)$' ./store
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The pinned Ponder 0.18.2 historical generator passed its original `getHistoricalEvents() omnichain` case. Two additional two-chain probes against that generator verify delivery of pending events beyond the common checkpoint and release of a pending prefix after catch-up. The probes run on real PostgreSQL and local Anvil nodes. The Go writer's durable completion of the final pending block is a native integration case. [Source probes](../../../tools/source-runtime-multichain-probes.test.ts) · [Go recovery case](../../../historical/omnichain_finalized_tail_test.go)

The focused selection passed the same 75 distinct leaf cases in ordinary and race runs, including PostgreSQL and PGlite. Some compiler and database subprocesses were killed under the shared memory limit on preliminary runs. The affected selections passed when rerun serially. All 51 Go packages compiled, and `go vet -p 1 ./...` passed. The verification tooling passed 503 Node and 43 Python tests. The full-module ordinary and race suites are tracked separately.

Continuous processing of unfinalized heads, rollback across both stores and replay after a live fork remain open for managed Omnichain deployments.
