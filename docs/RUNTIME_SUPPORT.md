# Runtime and module support

This matrix lists verified native Go features and the product capabilities still open against Ponder 0.18.2 at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. To run an indexer locally, follow the [usage guide](USAGE.md).

| Module / mode | Verified selected behavior | Remaining scope |
| --- | --- | --- |
| Single-chain and isolated Go starter | The atomic Store indexes new blocks, retries transient RPC errors and rolls back unfinalized forks. A bounded chain finalizes its configured end block. The RPC package can subscribe to new heads and reconnect. | The starter polls for the latest head. It needs a WebSocket subscription in the runtime loop. With open-ended sources it retains the undo journal and does not advance the durable finalized checkpoint automatically. Staged lifecycle workers have a separate finalization path. [Backend comparison and call sites](../parity/runs/native-go-ongoing-indexing/RESULT.md) |
| Default multichain RuntimeRunner | Real RPC acquisition, handlers and PG/PGlite business transactions; selected bounded/continuous history, source initial positions, cache policies, reorg/reopen and PG application exits. Factory logs, transactions, account transfers and contract CALL function traces are covered by the v10–v13 selections. | Other source combinations, provider failure cases and full lifecycle/CLI support. |
| Omnichain closing-block RuntimeRunner | v15 bounded ranges below the acquired global finalized watermark; cache enabled; exactly one interval1/offset0 closing block callback per chain covering all static contract ABI log ranges; startBlock >= 1. Actual RPC acquisition, ABI decoding, historical SQL paging and complete merged Groups transactions preserve same-block/overlap callback order and exact source failure/cold recovery on PG/PGlite. Historical.QuerySize 1..12000 (0 defaults12000). PG application exits are covered. | Live/pending/reorg, factory, mixed closing-block/log-only chains, account transaction/transfer/function trace sources, cache disabled, setup and start0 are rejected. Other ABI/provider/source combinations need broader omnichain coverage. PGlite abrupt exit and engine failures are not accepted. |
| Omnichain uniform log-only RuntimeRunner | The v17 storage format uses paged header proofs for both short and long ranges. It supports static contract ABI logs on every configured chain, finite callback ranges starting at block 1 or later, enabled caching and global finality. | Each chain requires a uniform log range below its finalized watermark. Live reorgs, mixed closing-block and log-only chains, and setup remain open. Application exits inside a callback and after activation are covered on PostgreSQL and PGlite, including activation with empty ranges. [Recovery tests](../parity/runs/native-engine-recovery/RESULT.md) |
| Isolated ordering | `runtime.NewIndexer` creates a worker for each configured chain with sources. Verified independent progress during an RPC outage, shared callback names, chain-scoped rollback and reopen on PostgreSQL and PGlite, and completion at finalized end blocks. | Further combinations of contract, account, factory and trace sources across chains. |
| Schema / business DB | Native schema, journal, scoped ORM/raw SQL and selected database lifecycle operations on PostgreSQL and PGlite. | Production role/permission layouts and the exported PostgreSQL types `cidr`, `inet`, `interval`, `macaddr`, `macaddr8` and `time`. Generated Go model round trips for binary, JSON, nullable array elements, point and line are covered on both backends. |
| ABI / events | Selected logs, account callbacks and dynamic contract CALL ABI decoding, including overloaded and dynamic tuple/array results. | Additional callback overlap and selector-collision combinations. Static ABIs generate typed function callbacks, including overloads and tuple arrays with colliding Go field names. |
| RPC / sync | Source-shaped actions, cache data, historical/realtime helpers and the selected actual RuntimeRunner pipelines. | Complete provider behavior, all archive/prefix failures and complete original runtime oracle coverage. |
| Recovery | Selected native durable receipts, exact retries, safe progress and application process exits. A PostgreSQL server or PGlite engine killed during a Store transaction preserves the prior commit on restart. Replaying twice calls the handler once and its writes can subsequently be reverted. | Host power loss, an engine killed inside the coordinated runtime with separate business and sync data, and abrupt PGlite application exits in further runtime modes. [Recovery tests](../parity/runs/native-engine-recovery/RESULT.md) |
| GraphQL HTTP and entities | Mountable read-only handler, primary and composite keys, scalar and enum fields, checkpoints and operation limits on PostgreSQL and PGlite. Table collections support typed filters, sorting, counts, offsets and bidirectional cursors. Relations support batched entity reads and filtered child pages. SQL views support aggregate fields and offset pages. | The 44 pinned GraphQL and middleware cases cover selected column types. Tables with a `point` or `line` column cause `graphql.New` to return an error. The generated Go models support both types. |
| CLI / development modes | Native dev, start and serve pass the listener address and namespaces to the child process. Dev rebuilds changed Go projects, keeps the running child during build errors and selects a per-revision shadow schema. Runtime startup, real ERC-20 minting and factory swaps are verified through the original SDK on both database backends. | PGlite directory administration, terminal UI and global log options. The PostgreSQL CLI can publish views, move them to a new deployment and prune inactive registered stores. [Database commands](USAGE.md#发布查询视图与清理部署) |

Of the 883 upstream cases ported to Go, 866 passed and 17 provider RPC cases failed in separate live runs. Five retained SDK cases also passed. The last recorded full Go ordinary and race suites each covered 30 packages. [Source inventory](../parity/test-ledger.json) · [Full module regression](../parity/runs/complete-module-regression/RESULT.md)

## Callback RPC methods

The Go starter passes an `*rpc.Client` to `runtime.Handler`. Generated callback registries expose an `*indexing.Client` with event-aware reads. The pinned Ponder callback API lists 21 named actions. Seven have high-level methods on `indexing.Client`. [Go callback context](../indexing/types.go) · [Upstream action list](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/indexing/client.ts#L122-L152)

| Area | Available on `indexing.Client` | Missing high-level methods |
| --- | --- | --- |
| State and contract reads | `GetBalance`, `GetCode`, `GetStorageAt`, `ReadContract`, `Multicall` | `Call`, `EstimateGas`, `GetFeeHistory`, `GetProof`, `SimulateContract` |
| Blocks and transactions | `GetBlock` | `GetTransactionCount`, `GetBlockTransactionCount`, `GetTransaction`, `GetTransactionReceipt`, `GetTransactionConfirmations` |
| ENS | `GetEnsName` | `GetEnsAddress`, `GetEnsAvatar`, `GetEnsResolver`, `GetEnsText` |

`Request` accepts raw JSON-RPC methods and parameters. Callers supply the block argument for historical reads and implement any higher-level action logic they need. The lower-level `rpc.Client` has its own receipt helper. The current `indexing.Client.Multicall` requires an explicit Multicall3 deployment address and lacks account and state override options. [Raw request handling](../indexing/client_requests.go) · [RPC receipt helpers](../rpc/actions.go) · [Multicall options](../indexing/client_multicall.go)

## Historical runtime selections

The v14 selected plan is **132 groups, 669 unique qualified native leaves**
(456 pure, 144 PostgreSQL, 69 PGlite), plus **276 race executions**, for **945
case executions**. It reruns the entire v13 selection of 612 unique leaves on the
new input. Its 57 additional qualified leaves comprise 36 omnichain/receipt
supplements and 21 affected legacy store controls. These are selection counts,
not whole-project completion. See
[validation](https://github.com/ysh0566/ponder-go/blob/2762a6bf5a2fbf339604306fbc1eee6f8f5554d3/parity/runs/m45-omnichain-v14-20261103/VALIDATION.md).

The v15 selected plan is **72 groups,559 unique qualified native leaves**
(473 pure,64 PostgreSQL,22 PGlite), plus **250 race executions**, for **809
case executions**. It selects528 v14 leaves again and adds31 native supplements
(17 capability,10 PG/PGlite source business,4 PG application-exit cases). The141
v14 leaves omitted on this snapshot are listed individually in
[v15 commands](https://github.com/ysh0566/ponder-go/blob/b92979aed1ed5f396be3365a905e4414d2b1acb0/parity/runs/m45-omnichain-events-v15-20261103/commands.json).
These are changed selection counts, not an increased formal mapping or whole-project
completion. See [v15 validation](https://github.com/ysh0566/ponder-go/blob/b92979aed1ed5f396be3365a905e4414d2b1acb0/parity/runs/m45-omnichain-events-v15-20261103/VALIDATION.md)
and [source contract](https://github.com/ysh0566/ponder-go/blob/b92979aed1ed5f396be3365a905e4414d2b1acb0/parity/runs/m45-omnichain-events-v15-20261103/SOURCE-CONTRACT.md).

Short and long log-only ranges write v17 and require v17 when reopened. A v16 namespace requires a fresh indexing run. [Paged lineage and sparse-range verification](../parity/runs/omnichain-long-sparse-v17/RESULT.md)
