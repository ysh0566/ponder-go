# Finishing a finalized range with ABI-skipped logs

A Go indexer using independent business and sync storage can finish a uniform static-log-only range when matching raw logs are present but none of the registered callbacks can decode them. This works for a trailing range, a chain with only malformed logs, and covered gaps preceding later valid callbacks.

The store reads complete pages under the sync transaction and uses the same event builder and ABI decoder as historical indexing. Every returned log must be accounted for. One decodable callback prevents the range from being certified. A receipt for a range containing skipped logs binds the ordered filters and event ABIs. Existing interval coverage and finalized header lineage are still required. The logical source checkpoint can precede the scanned endpoint.

## Verification

The native scenarios send matched Value(uint256) logs with a one-byte data field. They cover a sparse middle block, valid events followed by a malformed final page, two chains with only malformed logs, and a callback failure and restart with a malformed-only second chain. The tests compare delivered callbacks, shared business rows, SQL checkpoints, durable scan heads, and completed cold opens. Two additional controls exercise raw-empty ranges. Store tests cover overlapping callbacks with the same selector and different indexed arguments. If either ABI decodes a log the range remains pending. Changing an ABI invalidates a proof that used it.

The supplemental probe runs the pinned Ponder 0.18.2 Omnichain indexing loop with SQL page size 6. Four valid logs per chain at height 1 run two callbacks each. Four malformed logs per chain at height 3 produce zero callbacks. The original runtime becomes ready and keeps all three logical positions for each chain at the end of block 1. The Go runner checks the same checkpoints and persists scan heads at height 3.

The Go commands used Go 1.25.11, PostgreSQL 14.24 and PGlite 0.2.13. Each backend/mode run used its own local test databases and directories.

```sh
for backend in postgres pglite; do
  for mode in ordinary race; do
    flags=()
    if [[ $mode == race ]]; then flags+=(-race); fi
    go test "${flags[@]}" -v -count=1 -timeout=45m -run "^TestRuntimeOmnichainLogOnlySource$/^${backend}$/^(abi_skip_.*|trailing_empty|all_empty)$" ./historical
    go test "${flags[@]}" -v -count=1 -timeout=45m -run "^TestRuntimeOmnichainLogOnlySource$/^${backend}$/^abi_skip_" ./historical
  done
done
go test -count=1 -run '^TestLogOnly' ./store
go test -race -count=1 -run '^TestLogOnly' ./store
go test -run '^$' ./...
go vet ./...
```

The following checks used Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. The first command ran in its `packages/core` directory. The second ran from the Go checkout with `PONDER_UPSTREAM_ROOT` set to the pinned source.

```sh
vitest run --maxWorkers=1 --hookTimeout=45000 src/runtime/events.test.ts src/runtime/historical.test.ts -t 'decodeEvents\(\) log|getHistoricalEvents\(\) omnichain'
vitest run --config tools/source-runtime-omnichain-sparse.config.mjs
```

Three selected upstream declarations and all nine supplemental source probes passed. Twenty-two other upstream declarations in the selected files were outside the first command. The supplemental tests leave the upstream-case counts unchanged.

An independent SQL inspection opened temporary copies of both closed PGlite directories after the final cold reopen of the malformed-only fixture. The sync log table contained eight rows on each chain, all with `data = '0x01'`. The business account table contained zero rows.

```sql
SELECT chain_id, count(*), bool_and(data = '0x01')
FROM "<sync_namespace>_rpc_1".logs GROUP BY chain_id ORDER BY chain_id;
SELECT count(*) FROM "<business_namespace>".account;
```

Both Go modes passed six historical scenarios on each backend, including four new ABI cases and two raw-empty controls. All four ABI cases passed again on each backend after adding explicit rejection of missing log data and mismatched block heights. The focused store suite passed in both modes. The module compiled and passed vet. The repository tools passed 503 Node tests, 40 Python discovery tests and three additional observer tests. Full-module ordinary and race results for this revision remain pending in the shared regression queue.
