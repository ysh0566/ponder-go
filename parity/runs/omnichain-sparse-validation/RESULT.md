# Omnichain sparse log-only validation

The local development branch starts at `origin/main` commit `69022ff9000f57dd7e741704eceb8ef7ae5d375d`. The source probe runs the clean Ponder 0.18.2 checkout at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

The probe seeds real sync stores with cached intervals covering blocks 1–13,000 on each chain, with logs only at 1 and 12,020. It exercises the production source SQL paging, ABI callbacks, transaction, checkpoint, readiness and migration paths. RPC replies are fixture headers. Both PostgreSQL 14.24 and PGlite 0.2.13 passed all 8 cases using Vitest 5.0.2.

- With query size 12,000, an error in the distant chain-two callback rolls back the whole merged group. Both chains keep their initial Down(1) position. Reopening replays all 32 calls.
- With query size 6, the same error preserves 16 earlier writes and both Up(1) positions. Reopening replays the 16 distant callbacks.
- With one or both chains empty, those chains keep Down(1) after the application becomes ready. A chain with callbacks reaches Up(13,000).

The Go runtime continues to revalidate each empty height against finalized lineage and now requests the header without transaction bodies for empty log-only partitions. The existing PostgreSQL sparse-middle case requested transaction bodies twice before the change, and zero times after it. The 13,000-height runs exercise upstream behavior. The native v16 physical-range limit still applies.

For a bounded-memory range proof, the physical headers can be staged in fixed-size pages keyed by chain and acquisition generation. The verifier needs a contiguous parent-hash walk from the finalized anchor to the durable cursor and the actual source positions for event-bearing blocks. A merged group must bind its proof reference and exact source checkpoint per chain to the same transaction as handler writes and journal updates. The activation proof additionally needs owned sync coverage and raw-log absence for chains or tails omitted by the coordinator. Those are the conditions that let an empty chain become Ready at its original Down position.

## Commands

```sh
PONDER_UPSTREAM_ROOT=/workspace/ponder-upstream \
  /workspace/ponder-upstream/packages/core/node_modules/.bin/vitest run \
  --config tools/source-runtime-omnichain-sparse.config.mjs --configLoader native --reporter=dot

# Repeat with PONDER_SPARSE_POSTGRES set to the isolated loopback fixture.

go test -p 2 ./... -run '^$'

go test -p 1 -count=1 -json ./historical ./store \
  -run '^(TestRuntimeOmnichain|TestLogOnly|TestOmnichainAtomicSafeReceiptBinding|TestLegacyLiveRejectsOmnichainWireFields)'

go test -p 1 -race -count=1 -json ./historical ./store \
  -run '^(TestRuntimeOmnichain|TestLogOnly|TestOmnichainAtomicSafeReceiptBinding|TestLegacyLiveRejectsOmnichainWireFields)'

go test -count=1 -json ./historical -run '^TestRuntimeOmnichain.*ApplicationExit$'
go test -race -count=1 -json ./historical -run '^TestRuntimeOmnichain.*ApplicationExit$'

go test -count=1 -json ./historical \
  -run '^(TestRuntimeOmnichainSourceHandlerFailure|TestRuntimeOmnichainDifferentStarts|TestRuntimeOmnichainFinalizedOrderAndReopen)$'
go test -race -count=1 -json ./historical \
  -run '^TestRuntimeOmnichainLogOnlySource$/^pglite$/^(default_failure|empty_chain_failure)$'
go test -race -count=1 -json ./historical \
  -run '^(TestRuntimeOmnichainStaticLogsCapabilities|TestRuntimeOmnichainStaticLogsSource)$'
go test -race -count=1 -json ./historical \
  -run '^(TestRuntimeOmnichainSourceOracle|TestRuntimeOmnichainPreflightBoundary|TestRuntimeOmnichainSourceHandlerFailure|TestRuntimeOmnichainDifferentStarts|TestRuntimeOmnichainFinalizedOrderAndReopen)$'
```

Native verification uses Go 1.25.11, a fresh test root for each command, and a task-owned PostgreSQL container with a tmpfs data directory. `PONDER_M44_TEST_ROOT` and `PONDER_M45_TEST_ROOT` both point to the command's root. `PONDER_UPSTREAM` points to the pinned checkout for the PGlite bridge. `PONDER_TEST_POSTGRES` points to the fixture, with `sslmode=disable`.

## Results

- The compile check passed for all 24 Go packages.
- The same 141 distinct native cases passed in normal and race mode across the selected runs, including 13 PostgreSQL application-exit cases per mode. Zero selected cases were skipped. The initial broad logs retain their path-validation failures and package-level 10-minute timeouts. The exit and smaller rerun logs provide the successful executions for those cases.
- Both upstream SQL and runtime probes passed 8 cases, including cold recovery on both paging configurations.
- The PostgreSQL container and temporary PGlite and native fixture directories were removed after verification.

Whole-repository execution and native support for long sparse ranges remain open. These supplemental runs do not add original source mappings.
