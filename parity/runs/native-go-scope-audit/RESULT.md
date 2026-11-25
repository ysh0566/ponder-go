# Go indexer scope audit

This audit compares Go product revision `c5c6bc968c287e930246ca4325cd3122f5f96246` with Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. It covers current-format Go applications.

| Check | Observation |
| --- | --- |
| Construct a two-chain block indexer through `runtime.NewIndexer` | Isolated ordering constructed workers. Default and omnichain ordering returned the error directing the caller to `historical.RuntimeRunner`. No RPC request was needed for this constructor check. |
| Compare the three upstream callback action lists with the exported Go method set of `*indexing.Client` | Seven of 21 actions have named Go methods. All 21 names and their individual results match the [callback method table](../../../docs/RUNTIME_SUPPORT.md#callback-rpc-methods). Raw RPC remains available through `Request`. |
| Validate a table with a text primary key and one additional column, then call `graphql.New` | A text column passed both steps. The schema accepted `point` and `line`, then GraphQL rejected each column type. Schema validation rejected `inet` and `interval`. |
| Build the CLI with `go build ./cmd/ponder` and invoke the published commands | The binary reported `0.18.2-go.0`. Both `db prune` and `db create-views` returned the unimplemented-command error. The `dev` parser rejected `--log-level`, `--log-format` and `--disable-ui`. |
| Reconcile the source ledger | 883 native Go mappings, 866 passes and 17 separately executed RPC failures. Five retained SDK passes are counted separately. |

Go 1.25.11 passed `go test -count=1 -run '^TestRuntimeOmnichain(StaticLogsCapabilities|PreflightBoundary)$' ./historical` and the same command with `-race`. Each run passed 25 leaf cases. These tests check which configurations the runner accepts before it opens storage. Prior database runs cover the actual handling of supported sources on both backends. [Runtime matrix](../../../docs/RUNTIME_SUPPORT.md) · [Isolated multichain results](../native-chain-scheduling/RESULT.md)

The verification-tool suite passed 503 Node tests after restoring the archived inputs, including four original RPC, SQL and event fixture captures used by the test harness. The module-wide Go regression cited in the README was recorded separately.
