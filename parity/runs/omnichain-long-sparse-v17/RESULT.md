# Long sparse omnichain log history

The two-chain fixture indexes blocks 1 through 13,000 on each chain. Two callbacks match each eligible log, and only blocks 1 and 12,020 carry logs. The reference is Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Its eight cases passed independently on PostgreSQL 14.24 and PGlite 0.2.13.

| Source query size | Failure in the chain-two handler at block 12,020 | Cold restart |
| --- | --- | --- |
| 12,000 | Both chains retain Down(1). All 32 writes in the merged group roll back. | Replays 32 callbacks. |
| 6 | Both chains retain Up(1) and their 16 earlier writes. | Replays the remaining 16 callbacks. |

A chain with no logs becomes ready at Down(1). A chain with matching logs reaches Up(13,000). The tests check persisted rows, callback order, checkpoint identity and recovery using the runner, HTTP RPC, ABI and real SQL backends.

## Storage and recovery

The runtime walks backwards from the acquired finalized header to the durable cursor on each chain and checks every parent hash. It stores up to 256 fixed-width headers in memory per chain and spools longer walks to a private temporary file. Each source group commits its callbacks, business rows, journal, progress and header evidence in one transaction. Bounds, endpoints and an ordered SHA-256 digest bind each omitted range to its receipt. The owned sync store must cover every required filter fragment over that range and yield no matching raw log in the omitted heights.

The business database retains up to 256 headers per page. A cold audit rereads those pages, verifies parent linkage across page boundaries and checks the total physical page count. A full audit reads all retained pages, so its work grows with the number of recorded headers. Activation independently binds the configured history cut and the remaining finalized tail. Existing v16 namespaces keep their format. V17 applies to static log-only histories whose total physical height exceeds the existing v16 budget. Mixed stream types and raw logs that fail ABI decoding remain outside this admission path.

## Reproduction

Use Go 1.25.11, the pinned sibling upstream checkout, an isolated PostgreSQL database and a fresh local directory for each command. Set `PONDER_TEST_POSTGRES` to the loopback database URL, `PONDER_UPSTREAM` to the source checkout, and both `PONDER_M44_TEST_ROOT` and `PONDER_M45_TEST_ROOT` to the fresh directory.

```sh
go test -p 2 -run '^$' ./...
go vet -p 2 ./...
go build ./...
go test -timeout 90m -count=1 -json ./historical ./store \
  -run '^(TestRuntimeOmnichain|TestRuntimeFinalized|TestLogOnly|TestOmnichainAtomicSafeReceiptBinding|TestLegacyLiveRejectsOmnichainWireFields)'
go test -timeout 90m -race -count=1 -json ./historical ./store \
  -run '^(TestRuntimeOmnichain|TestRuntimeFinalized|TestLogOnly|TestOmnichainAtomicSafeReceiptBinding|TestLegacyLiveRejectsOmnichainWireFields)'
```

The recorded executions partition these expressions by test, backend, query size and failure scenario. The source probe uses `tools/source-runtime-omnichain-sparse.config.mjs` with `PONDER_UPSTREAM_ROOT` set to the pinned checkout. Run once on PGlite and again with `PONDER_SPARSE_POSTGRES` set to the isolated PostgreSQL URL. The supplemental probes do not change the formal mapping of 543 out of 905 upstream records.

## Review and regression results

Both ordinary and race runs passed the same 206 distinct leaf tests, including all 141 names from the v16 acceptance set. Each mode includes 16 long sparse integration cases and three process-exit cases. No selected case was skipped. [cases.json](cases.json) records the input fingerprint, exact additions, retained and omitted sets, and SHA-256 of every selected run. The comparison counts each package and complete subtest name once and excludes parent nodes, including implicit nodes between named subtests.

The code review checked the finalized walk, receipt and legacy format fences, source coverage, callback and journal transaction boundaries, retry and cold-open paths against the pinned source behavior. It found an early return in coverage validation that accepted a reversed trailing interval after an earlier interval had covered the request. The fix checks every interval and has a regression case on the submitted snapshot. The compiled CLI identifies its clean product revision as `b2a6be1ccfafc169cb1008c45b3045648fa723c2`.

The stage PostgreSQL container and its temporary data volume were removed after the last race run. All task-owned sparse test directories were released.

## Historical RPC test fixtures

Nine distinct upstream RPC success declarations passed on a locally served index of canonical historical data. The index served only the covered ranges and addresses. It retained the underlying log payloads and the original Vitest reports. Ethereum reads included 140,192 WETH logs and 3,773 Uniswap factory logs. Base returned 77 logs, TAC returned 185 and HyperEVM returned 4,217. For 641 HyperEVM heights missing from the primary archive, the index checked the full receipt list against the block's transactions using the official Hyperliquid RPC. The local archive and the original test source hashes are recorded in [archive-rpc.json](archive-rpc.json).

These supplemental runs used the pinned, unchanged test files with their RPC environment variables pointed to loopback. The nine service-specific error declarations depend on the corresponding provider's limits and response shape. The recorded original-environment result remains 849 passed, 44 failed, 10 skipped and 2 todo.
