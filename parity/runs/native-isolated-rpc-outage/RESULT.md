# Live RPC outages with independent Isolated stores

After two chains commit block 2, a local HTTP gateway returns 503 to every request for chain 1. The second observed retry releases blocks 3 and 4 on chain 2. The test holds the failing chain's retry until the handler on chain 2 commits block 4. During the outage the deployment is unready, its persisted serving flag is false and chain 1's durable cursor still points to the original block 2. [Integration test](../../../historical/isolated_live_outage_test.go)

On the parent revision `66c1be37`, the regression case on PostgreSQL timed out at the peer-progress guard. Both chains had committed blocks 1 and 2. The shared retry wait prevented the healthy chain from reading the new blocks.

The Isolated runner pauses serving and seals speculative reads at the first source fetch failure. An independent generator probes the disconnected chain's latest header at the configured retry interval. Healthy chains can commit while the request is delayed or returns an error. After a successful probe, the runner reconstructs the read queues from durable cursors. The restored node must still pass the ordinary branch and finality checks. The test replaces block 2 on chain 1 before restoring the gateway. It checks that only chain 1 replays this height, both chains reach block 5 and all ten business rows contain the corresponding canonical hashes. [Live queue](../../../historical/live_runtime_continuous.go) · [Usage](../../../docs/USAGE.md#双库-isolated-实时追块)

Initial opening and historical catch-up still acquire data for all chains. The integration test exercises endpoint-wide HTTP 503 after activation. Method-specific failures on a node that continues answering latest-header reads require separate verification.

## Reproduction

Use Go 1.25.11 with a dedicated PostgreSQL 14.24 test database and the PGlite 0.2.13 bridge. Set `PONDER_M45_TEST_ROOT` to a fresh absolute directory.

```sh
for flags in '' '-race'; do
  go test $flags -p 1 -count=1 -run '^TestRuntimeIsolatedLiveOutage$' ./historical
done
```

The upstream selection exercises the Isolated database commit and three real-time reorg cases. All four passed. The prolonged HTTP outage and the scheduling guard are additional Go integration assertions.

```sh
cd ../ponder-upstream/packages/core
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/database/actions.test.ts src/sync-realtime/index.test.ts -t 'commitBlock\(\) isolated|handleReorg\('
```

The outage case passed on PostgreSQL and PGlite in both ordinary and race modes. The existing PostgreSQL regression cases also exercise an Isolated reorg, interruption during a durable reorg, default multichain completion with a slower peer, and default multichain infinite retry with cancellation. Use the following selection to reproduce those controls.

```sh
go test -p 1 -count=1 -run '^TestRuntime(IsolatedLiveReorg|ContinuousCompletion|ContinuousInfiniteRetry)$/^postgres$' ./historical
```

The Go source-case ledger retains its 883 mappings. This additional native regression does not change that count.
