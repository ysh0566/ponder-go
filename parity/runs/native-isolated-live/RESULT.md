# Continuous Isolated indexing with separate business and sync stores

`historical.RuntimeRunner` accepts a Go source build with `experimental_isolated` ordering and a non-nil `RuntimeOptions.Live`. The business and sync stores open continuous Isolated deployments under the `managed-live-isolated-continuous-v1` format. Both PostgreSQL and PGlite support the same public entry points and restart procedure. [Usage](../../../docs/USAGE.md#双库-isolated-实时追块)

## What the tests exercise

Two controlled HTTP nodes produce independent chains. Chain 1 keeps a long speculative window and caches RPC results. Chain 2 has a one-block window and reads directly from its node. Both run a Go setup callback at genesis. The block callback reads a balance and inserts a row keyed by chain and height.

- Chain 1 writes through block 3. Chain 2 then writes through block 4 and finalizes block 3. Replacing block 3 on chain 1 must retain both the safe and speculative rows on chain 2. The test inspects both private cursors, the peer's safe head, callback counts and the physical business rows.
- One case sets a future end block. The other has an open-ended source and stops after the sync store commits the reorg intent, before the business rollback. Both close the runner and replace another block on chain 1 while offline.
- After reopening both original stores, chain 1 reads the replacement balance starting at block 2. Chain 2 resumes from block 4. The three safe peer callbacks and the two genesis setup callbacks execute only once across both openings. The bounded case completes at block 6. The open-ended case catches up to block 6 and the test stops it via its progress callback.
- The store test validates surviving journal spans after interleaved peer segments have been retired. It checks a no-write segment and rejects missing or unbound rows, changed digests, a wrong chain ID and invalid ordinal or predecessor relationships.

The interruption is injected at a durable operation boundary. A database engine exit inside a mixed factory/contract/account callback requires separate coverage. A retryable fetch error rebuilds the shared queue. Continued peer progress during a prolonged outage on one chain still needs verification. Cold restart can replay speculative peer callbacks above that peer's safe checkpoint. [Runner cases](../../../historical/isolated_live_test.go) · [Journal cases](../../../store/managed_live_isolated_test.go)

## Reproduction

Use the repository's local PostgreSQL and PGlite test configuration with Go 1.25.11. The following selection includes global rollback and finalized Isolated and Omnichain admission regressions.

```sh
selection='^(TestRuntimeIsolatedLiveReorg|TestRuntimeIsolatedPreflight|TestRuntimeIsolatedSetup|TestRuntimeIsolatedFinalized|TestRuntimeContinuousReorg|TestRuntimeContinuousFutureEnd|TestRuntimeOmnichainPreflightBoundary|TestIsolatedJournalAfterPeerRemoval|TestManagedLive(Segments|Finalize|Reorg)Unit.*|TestOmnichainAtomicSafeReceiptBinding)$'
go test -p 1 -v -count=1 -timeout=45m -run "$selection" ./store ./historical
go test -p 1 -race -v -count=1 -timeout=45m -run "$selection" ./store ./historical
go test -p 1 -race -v -count=1 -timeout=45m -run '^TestRuntimeIsolatedLiveReorg$' ./historical
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The focused ordinary run passed all 194 leaf cases with zero skips. The broad race run passed 191 leaves. A PGlite process was killed under memory pressure in the future-end case, and two later cases refused to reuse the failed fixture. A race rerun of both affected groups in a fresh fixture passed all six leaves, including the three affected PGlite cases. The four Isolated live cases with setup also passed under the race detector. The reviewed storage predicates passed all 166 selected storage cases in both modes. All 50 Go packages compiled, and `go vet -p 1 ./...` passed. Repository tooling passed 503 Node.js and 43 Python tests.

```sh
go test -p 1 -race -v -count=1 -timeout=45m -run '^(TestRuntimeContinuousReorg|TestRuntimeContinuousFutureEnd)$' ./historical
```

These focused results are separate from the module-wide ordinary and race suites, which run serially to limit concurrent PGlite memory use. Their results are pending.

The upstream reference at Ponder 0.18.2 commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` passed four selected commit, revert and finalize cases against PostgreSQL 14.24. The name filter excluded seven other cases. Run from `packages/core`.

```sh
node_modules/.bin/vitest run --maxWorkers=1 src/database/actions.test.ts -t 'commitBlock\(\) isolated|revert\(\)|^finalize\(\)$'
```
