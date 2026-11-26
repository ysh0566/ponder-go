# Static accounts and function callbacks in finalized Omnichain ranges

A source build can register account transaction and native transfer callbacks and static contract CALL callbacks when every chain has a closing block callback. The closing callback must cover all of its chain's event ranges. The historical runtime accepts matching transaction and trace checkpoints, including a contract CALL and a value transfer at the same checkpoint. It validates physical block identity, ordering and the canonical closing checkpoint before committing the group.

The Go regression uses two local Anvil chains with interleaved block timestamps. Each chain receives an incoming transfer of 10 wei and an outgoing 13 wei call to a payable contract function. The handlers check the transaction hash, successful receipt, transfer endpoints and amount, and decoded function input and output. Four globally ordered blocks produce fourteen callbacks writing to one shared row. On the last block a handler writes and then returns an error. Two further opens verify rollback, replay of the outstanding suffix, canonical cursors on both chains, the final shared row and zero callbacks on the completed range.

## Verification

```sh
selection='^(TestOmnichainStaticAccountEvents|TestOmnichainAccountPartition|TestRuntimeOmnichainStaticLogsCapabilities|TestRuntimeOmnichainPreflightBoundary)$'
go test -v -count=1 -timeout=45m -run "$selection" ./historical
go test -race -v -count=1 -timeout=45m -run "$selection" ./historical
go test -v -count=1 -timeout=45m -run '^TestRuntimeOmnichainStaticLogsSource$/^postgres$/^small$' ./historical
go test -run '^$' ./...
go vet ./...
```

Each Go mode passed 36 cases, including both database engines, the six partition checks and 28 preflight cases. The selected PostgreSQL static log regression also passed on the integrated tree. All Go packages compiled, vet passed and all 503 verification-tool tests passed.

The pinned upstream ran ten event-building, decoding and historical synchronization cases successfully with `vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/events.test.ts src/sync-historical/in-memory.test.ts -t 'decodeEvents\(\) (transfer|transaction|trace)|buildEvents\(\) (transaction|trace)|syncBlockData\(\) with (trace|transaction|transfer) filter'`. Twenty-four tests were outside this selection. The new end-to-end scenarios add no original case mappings.
