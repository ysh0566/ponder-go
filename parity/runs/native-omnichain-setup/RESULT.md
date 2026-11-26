# Transactional Go setup in finalized Omnichain

The independent runtime accepts compiled contract setup handlers with a closing block callback on every chain or with static ABI logs on every chain. The two shapes use their existing business and sync storage formats. The configured default setup RPC height must be within local and global finality. A contract used only for setup can select a later block than its chain's last event.

The integration test registers a Go handler on each of two chains, reads each configured RPC block and updates a shared row. The second handler fails after updating the first handler's write. A fresh open retries both. It then stops after the committed initialization before delivering events. The next open consumes the captured global event sequence. A final open checks that initialization and completed events do not run again. The test inspects SQL rows, the undo journal and durable initialization checkpoints across opens.

A separate two-chain test supplies a setup height which looks safe to the preflight requester. The execution epoch observes a later timestamp on the authenticated lineage. It returns an error before any callbacks, business batches or initial checkpoints. Additional preflight cases cover a height above local finality and a locally finalized height above the common watermark.

The upstream reference is Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. The transaction and initial checkpoint writes are in `packages/core/src/runtime/omnichain.ts`. The setup handler context and dispatch are in `packages/core/src/indexing/index.ts`. The native selections use PostgreSQL 14 and PGlite 0.2.13. New coverage is supplemental and leaves the source-case inventory unchanged.

## Verification

```sh
go test -count=1 -timeout=45m -run '^TestRuntimeOmnichainSetup(Finality|Lineage)?$/^(postgres|local|global)$' ./historical
go test -count=1 -timeout=45m -run '^TestRuntimeOmnichainSetup(Lineage)?$/^pglite$' ./historical
go test -race -count=1 -timeout=45m -run '^TestRuntimeOmnichainSetup(Finality|Lineage)?$/^(postgres|local|global)$' ./historical
go test -race -count=1 -timeout=45m -run '^TestRuntimeOmnichainSetup(Lineage)?$/^pglite$' ./historical
go test -count=1 -run '^TestRuntime(OmnichainPreflightBoundary|OmnichainStaticLogsCapabilities|SetupPreflight)$' ./historical
go test -count=1 -run '^TestRuntimeSetupBaselineLineage$' ./historical
go test -race -count=1 -run '^TestRuntimeSetupBaselineLineage$' ./historical
go test -run '^$' ./...
go vet ./...
```

Both Go modes passed the four setup lifecycle combinations, the two preflight limits and both database cases with a contradictory RPC view. The default-ordering baseline lineage controls also passed on both engines. All packages compiled and vet passed. The verification tools passed 503 Node tests and 40 Python tests.

The pinned upstream command `vitest run --maxWorkers=1 --hookTimeout=45000 src/indexing/index.test.ts -t 'processSetupEvents|executeSetup'` passed all six selected cases. Seventeen other tests in the file were outside the selection. Full-module ordinary and race results for this revision are pending in the shared regression queue.
