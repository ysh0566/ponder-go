# Isolated cold reopen after partial per-chain setup

A continuous dual-store Isolated writer checks for a committed initial cursor on each chain whose RPC is unavailable. A chain with a reachable endpoint can acquire its opening finality and retry a setup transaction that previously rolled back. Both store ledgers and the business snapshot are authenticated under the existing owner locks. The writer validates an offline peer against its recorded finality when it reconnects. A fresh database or an unavailable chain without a committed initial cursor still requires the endpoint. [Store check](../../../store/managed_live_isolated.go) · [Runtime guide](../../../docs/USAGE.md#双库-isolated-实时追块)

## End-to-end case

The case runs against PostgreSQL and persistent PGlite directories. Both chains have a setup handler and index blocks 1 through 3. The first chain inserts a row and fails setup, rolling back its row and cursor. The second chain commits its own setup.

- With only the first chain offline, the public open function returns both `ErrMissingIsolatedBaseline` and the underlying HTTP 503.
- With only the second chain offline, the writer reopens. Its generation advances exactly once from the original run and its only existing cursor is unchanged. The first chain completes setup, commits all three blocks, and observes that the deployment is still unready and its peer has had no block callbacks.
- After the peer is brought online, the writer validates the retained cursor, processes its three blocks, and reaches completion and readiness. A query of the closed writer's business store finds one setup row and three indexed heights per chain. The failed setup had two attempts and the committed setup ran once. [Integration test](../../../historical/isolated_live_outage_test.go)

## Verification

Use the repository's task-owned PostgreSQL database, current PGlite bridge, fixture directories and pinned toolchain.

```sh
for mode in '' '-race'; do
  go test $mode -p 1 -count=1 -run '^Test(RuntimeIsolatedPartialSetupReopen|RuntimeIsolatedOfflineReopen|IsolatedReturningFinality|RuntimeIsolatedSetup)$' ./historical
  go test $mode -p 1 -count=1 -run '^TestRuntime(OmnichainPreflightBoundary|PreflightRejectsUnsupported)$' ./historical
  go test $mode -p 1 -count=1 -run '^TestManagedLiveDomainsBootstrap$' ./store
done
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

A PostgreSQL control substituted the original finality restoration function from the parent commit. The same end-to-end case failed when the offline second chain already had its checkpoint but the reachable first chain needed to finish setup.

Four pinned upstream Isolated commit and realtime reorg controls passed on private PostgreSQL. The Vitest name filter excluded 37 unrelated cases. These native recovery cases add supplemental coverage.

All 31 selected Go cases passed in both ordinary and race modes. The selection includes two partial-setup cases, five returning-finality and cold-reopen cases, two original per-chain setup cases, four store bootstrap cases and 18 preflight cases. Initial overlapping PGlite runs hit the executor memory limit during engine startup. The final backend-specific runs completed without skips. Verification tools passed 503 Node tests and 43 Python tests.

All 51 packages compiled and go vet completed without findings. Full-module ordinary and race runs are tracked separately.
