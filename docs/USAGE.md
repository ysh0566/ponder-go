# 原生 Go 索引器使用指南

这份教程使用仓库里的 ERC-20 示例、一个临时 PostgreSQL 数据库和一条本地 Anvil 链。你将部署一个只在构造时发出标准 `Transfer` 事件的演示合约，启动索引器，再通过 HTTP 查到这笔事件产生的两条余额记录。[索引器](../examples/erc20/main.go) · [表定义](../examples/erc20/schema.json)

## 选择 Go 索引器的运行入口

| 业务需要 | 运行入口 | 可用的多链模式 |
| --- | --- | --- |
| 用 Go 配置链和来源，在回调里写业务表 | `runtime.NewIndexer` | 默认多链、Isolated、Omnichain 均可回填并持续追块。 |
| 已用 Go 编译来源配置，并注册了 `runtime.Handler` | `runtime.NewIndexerFromSource` | 沿用编译配置选定的排序，一次启动所有注册了来源的链。 |
| 分开管理业务数据和同步缓存，使用 `indexing.Handler` | `historical.OpenRuntimePostgres` 或 `historical.OpenRuntimePGlite` | 默认多链与 Isolated 均可持续运行。Omnichain 的各链区间须在共同最终确认检查点以内。 |

如果各链分别维护自己的余额，使用 Isolated，在业务表主键中包含 `chain_id`。如果多条链共同维护一个按事件先后计算的值，使用 Omnichain，运行器会按跨链 checkpoint 排序回调。PostgreSQL 双库存储使用同一数据库内的两个命名空间，PGlite 使用两个数据目录。[独立多链示例](#从同一个-go-项目索引多条链) · [跨链排序示例](#按全局顺序索引多条链) · [双库存储](#独立存储的有限区间索引)

在双库 Isolated 中，设置 `RuntimeOptions.Live` 即可跟随链头。双库 Omnichain 应保留 `Live = nil`，并指定有限的结束高度。作业可以提前启动，等待区间最终确认。[提前启动](#等待双库-omnichain-的区间最终确认) 需要 Omnichain 持续追块时可选用普通 Go 入口。[Isolated 实时配置](#双库-isolated-实时追块) · [运行器检查](../historical/live_runtime.go)

### 读取 Omnichain 历史和实时事件

需要自己消费事件流时，可调用 `runtime.StreamSourceOmnichain(ctx, history, nil)`。传入与 `HistoricalSourceOmnichain` 相同的 `runtime.SourceHistoricalCoordinatorOptions`，其中包含各链的来源、RPC、已确认区块、同步进度及所使用的缓存。Go 事件流会先做历史提取，再使用每条链的 RPC 和轮询间隔追块。也可传入 `runtime.SourceRealtimeFeed` 接入已有的 Go 实时同步器。[双节点可运行用例](../runtime/source_omnichain_stream_test.go)

例如，下面的函数可以观察交接期间各条链的事件和发生回滚的检查点。

```go
func observe(ctx context.Context, history runtime.SourceHistoricalCoordinatorOptions) error {
    for update, err := range runtime.StreamSourceOmnichain(ctx, history, nil) {
        if err != nil {
            return err
        }
        switch update.Type {
        case "events", "block":
            for _, event := range update.Events {
                log.Printf("event %v at %v", event["type"], event["checkpoint"])
            }
        case "reorg", "finalize":
            log.Printf("%s through %s", update.Type, update.Checkpoint)
        }
    }
    return nil
}
```

`Events` 已按全局 checkpoint 排序。历史批次的 `Groups` 另外列出各链的进度，其中的事件与 `Events` 重复。实时批次可以包含其他链先前暂缓交付的事件。处理函数应只读访问事件映射，同一事件可能在分叉后再次交付。一条链分叉时，消费者收到 `reorg` 后应撤销全局检查点之后的业务写入。后续批次会重新交付仍在规范链上的事件。`finalize` 给出各链共同确认的位置。使用自定义事件源时，如有 `update.Source.BlockCallback`，在处理完该次交付后按实际结果应答。

此接口在进程内协调事件顺序。消费方负责业务事务、检查点持久化和重启恢复。固定区间处理到结束高度后返回，持续索引可通过取消 `ctx` 或跳出循环来停止。节点错误会返回给调用方。[事件流实现](../runtime/source_omnichain_stream.go)

## 准备环境

需要 Git、Go 1.25 或以上版本、启用 CGO 的 C 编译环境、Docker、Foundry 的 `anvil` 和 `forge`，以及 `curl`、`jq`。以下命令适用于 Bash。示例已用 Go 1.25.11、PostgreSQL 14.24 和 Foundry 1.8.5 验证。首次构建会下载 Go 依赖，Forge 首次编译会按合约声明获取 Solidity 0.8.37；本机已有该编译器时也可将 `FOUNDRY_SOLC` 设为其绝对路径。

在终端 A 克隆项目、构建 CLI 并启动本地链。示例应用配置的链 ID 为 1，启动 Anvil 时也要指定为 1。

```sh
git clone https://github.com/ysh0566/ponder-go.git
cd ponder-go
go build -o bin/ponder ./cmd/ponder
./bin/ponder --version
anvil --host 127.0.0.1 --port 18545 --chain-id 1 --silent
```

版本命令应输出 `0.18.2-go.0`。保持 Anvil 运行，在终端 B 进入同一仓库根目录，启动本次教程专用的临时数据库。示例口令仅用于这个绑定到本机的容器，停止容器会清除这次的数据库。

```sh
docker run --rm -d --name ponder-erc20-guide \
  -p 127.0.0.1:55432:5432 \
  -e POSTGRES_USER=ponder -e POSTGRES_PASSWORD=local_demo \
  -e POSTGRES_DB=ponder postgres:14.24-bookworm
until docker exec ponder-erc20-guide pg_isready -h 127.0.0.1 -U ponder -d ponder; do sleep 1; done

export PONDER_LOCAL_DATABASE_URL='postgres://ponder:local_demo@127.0.0.1:55432/ponder?sslmode=disable'
export PONDER_LOCAL_RPC_URL='http://127.0.0.1:18545'
```

## 生成一条可索引的日志

继续在终端 B 执行。Forge 使用这条本地链已解锁的第一个账户部署合约。部署交易会从零地址向部署账户发出数值为 25 的 `Transfer(address,address,uint256)` 日志。

```sh
demo_dir=$(mktemp -d)
mkdir -p "$demo_dir/src"
cat > "$demo_dir/src/TransferDemo.sol" <<'SOL'
// SPDX-License-Identifier: MIT
pragma solidity 0.8.37;

contract TransferDemo {
    event Transfer(address indexed from, address indexed to, uint256 value);

    constructor() {
        emit Transfer(address(0), msg.sender, 25);
    }
}
SOL

sender=$(curl -fsS -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"eth_accounts","params":[]}' \
  "$PONDER_LOCAL_RPC_URL" | jq -er '.result[0]')
forge create --root "$demo_dir" "$demo_dir/src/TransferDemo.sol:TransferDemo" \
  --rpc-url "$PONDER_LOCAL_RPC_URL" --unlocked --from "$sender" \
  --broadcast --json > "$demo_dir/deploy.json"
PONDER_LOCAL_CONTRACT=$(jq -er '.deployedTo' "$demo_dir/deploy.json")
export PONDER_LOCAL_CONTRACT
```

## 启动索引器

在同一终端 B 中运行以下命令，使索引器继承刚才设置的三个环境变量。`--root` 指向包含 Go 入口和 `schema.json` 的示例目录，`--schema` 为这次索引指定独立的 PostgreSQL 命名空间。

```sh
./bin/ponder start --root ./examples/erc20 --schema ponder_go_erc20_demo
```

CLI 会调用 PATH 中的 `go` 构建示例，先打开 HTTP 服务，在历史同步完成后继续跟踪新区块。回填期间可查看 `/health` 和 `/status`，`/ready` 返回 503。临时 RPC 连接或网关错误会触发退避重试，恢复到已提交进度后 `/ready` 返回 200。保持终端 B 运行。日志会给出实际监听地址，默认从 `127.0.0.1:42069` 开始，端口占用时会尝试下一个端口。

## 查询索引结果

在终端 C 执行。若启动日志显示其他端口，先相应修改 `base`。

```sh
base=http://127.0.0.1:42069
curl -fsS -o /dev/null -w '%{http_code}\n' "$base/ready"
curl -fsS "$base/status" | jq .
curl -fsSG "$base/sql/db" \
  --data-urlencode 'sql={"json":{"sql":"SELECT id, balance FROM account ORDER BY id","params":[]}}' \
  | jq '.rows'
```

`/ready` 应返回 HTTP 200。在上述全新链上，`/status` 中的 `local.block.number` 为 1。SQL 返回两行；使用 Anvil 默认账户时结果如下。示例按收到的事件累计净变动，因此零地址扣减 25，部署账户增加 25。`balance` 以十进制字符串返回。

```json
[
  {"id": "0x0000000000000000000000000000000000000000", "balance": "-25"},
  {"id": "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266", "balance": "25"}
]
```

| HTTP 接口 | 用途 |
| --- | --- |
| `GET /health`、`GET /ready` | 检查服务是否响应、是否就绪。 |
| `GET /status` | 读取各链上次提交的索引进度。 |
| `GET /sql/db` | 在应用定义的表和视图上执行只读查询。将 SQL 和参数放在上例的 JSON 对象中，再对 `sql` 参数进行 URL 编码。 |
| `GET /sql/live` | 用同样的 `sql` 参数建立 SSE 订阅。先用 `/sql/db` 读取当前结果；订阅在相关表变动且结果改变时发送数据。 |
| `GET /metrics` | 以 Prometheus 文本格式读取服务指标。 |

### 查看历史回填耗时

在运行索引器的进程上读取 Prometheus 指标。将端口换成应用实际监听的端口。

```sh
curl -fsS http://127.0.0.1:42069/metrics | grep '^ponder_historical_'
```

| 指标 | `step` 和含义 |
| --- | --- |
| `ponder_historical_extract_duration` | `format` 整理同步库查询返回的行，`build` 组装原始事件，`decode` 解码供 Go 回调使用的事件。 |
| `ponder_historical_transform_duration` | `prefetch` 调度预取，`begin` 开启业务事务，`index` 执行索引回调，`load` 刷新写入缓存，`finalize` 推进最终确认位置，`commit` 提交业务事务。 |

读数是当前进程内已完成步骤的累计**毫秒数**，按步骤聚合所有链。可用两次抓取之差观察这段时间内各步骤的耗时。并发链上的时间会分别累加。一笔事务后续回滚时，此前已完成步骤的耗时仍会保留。未执行的步骤没有样本，例如直接在内存中构建事件时不会产生同步库的 `format` 样本。

`runtime.NewIndexer` 和 `runtime.NewIndexerFromSource` 在首次追平过程中计入已达到最终确认范围的区块。双库 `historical.RuntimeRunner` 按实际回填阶段计时。实时追块和实时分叉重放均沿用原有累计值。独立 `serve` 进程拥有自己的指标注册表，要看回填耗时应抓取写入进程。`runtime.LaunchApplication` 自动连接查询服务的注册表。直接调用 `Run` 时，可传入 `metrics.WithHistorical(ctx, registry)`，再用 `registry.Collect(ctx)` 读取。[运行时验证](../parity/runs/native-historical-duration-metrics/RESULT.md)

## 重开索引或只提供查询

保留运行中的 `start`，在另一个终端配置同一个 `PONDER_LOCAL_DATABASE_URL`。使用相同的业务命名空间，选择另一个 HTTP 端口。查询进程只需要数据库连接，RPC 和合约地址由写入进程使用。

```sh
./bin/ponder db list --database-url "$PONDER_LOCAL_DATABASE_URL"
./bin/ponder serve --root ./examples/erc20 --schema ponder_go_erc20_demo --port 42070
```

在 `http://127.0.0.1:42070` 使用上文的 SQL 和 SSE 接口。`serve` 会核对 `schema.json`、排序模式和当前数据库格式。首次部署请先用 `start` 初始化数据库。使用较早版本启动的同格式索引器时，重启写入进程一次即可安装通知触发器。后续提交和重组回滚都会唤醒独立查询进程。写入进程停止后仍可查询最后一次提交的结果。

查询进程可以使用独立的 PostgreSQL 登录角色。下面的示例假设 `indexer_reader` 已有目标数据库的连接权限。由数据库所有者在写入部署初始化后执行，再把读角色的连接地址设为查询进程的 `PONDER_LOCAL_DATABASE_URL`。新增业务表或视图后要给读角色授予相应的 `SELECT` 权限。

```sql
GRANT USAGE ON SCHEMA ponder_go_erc20_demo, ponder_go_erc20_demo_sync TO indexer_reader;
GRANT SELECT ON ponder_go_erc20_demo.account,
                ponder_go_erc20_demo._ponder_go_meta,
                ponder_go_erc20_demo._ponder_go_progress,
                ponder_go_erc20_demo_sync._ponder_go_meta TO indexer_reader;
```

自行编写入口时，写入端调用 `Store.EnableQueryNotifications`，查询端使用 `store.OpenExisting` 和 `query.NewAtNamespace`。PostgreSQL 的监听连接暂时不可用时，每 250 毫秒重试。恢复后，原 SSE 流重新读取当前结果。断线期间的多次提交可能合并为一次推送。[生成项目与读角色实测](../parity/runs/native-standalone-serve/RESULT.md) · [断线恢复实测](../parity/runs/native-query-reconnect/RESULT.md)

完成后，分别停止查询服务、终端 B 的索引器和终端 A 的 Anvil。若要丢弃本次教程数据，运行 `docker stop ponder-erc20-guide`，并删除终端 B 中 `$demo_dir` 指向的临时目录。

## 开发时自动重载

在原生 Go 项目目录中启动开发模式。它会监视 Go 源码、模块文件、JSON 配置和 SQL 文件，开发服务器会打印实际监听地址和数据所在的 schema。

```sh
ponder dev --hostname 127.0.0.1 --port 42069 --schema ponder_go_demo
```

保存修改且编译成功后，CLI 会停止上一个进程并启动新的索引器。编译失败时，之前成功启动的进程继续运行，下一次编辑会重新构建。开发 schema 的名字由前缀和项目内容的摘要生成，修改处理函数或表定义会选择新的命名空间并重新索引。按 Ctrl+C 退出。

当标准输出和标准错误连接到同一个交互终端时，`ponder dev` 在日志下方显示编译和重载状态。通过 `runtime.LaunchApplication` 启动的应用还会显示实际 HTTP 地址、是否追上链头，以及每条已有 checkpoint 的链最近提交的区块。即使指定 `--port 0`，也可以使用显示的地址查询。重组期间区块号可能回退。自定义 HTTP 入口只显示进程和构建状态。

使用 `runtime.NewIndexer` 时，每条链和每个 Go 回调都有一行统计信息。下例中的 `Transfer` 回调已成功执行 28 次。

```text
local  block 119  RPC 7.5 req/s  backfill 60%  ETA 10s
Token:Transfer  calls 28  avg 0.650 ms
```

- **RPC 速率**按最近最多 100 次终端采样之间的实际请求数计算，包含重试和回调中的查链请求。命中 RPC 缓存时不会发送请求。
- **回填进度**是当前扫描范围内已完成的区块数除以总区块数。范围从最早来源的起始块到最近获取的链头，如有结束块则取较低者。区块号来自已提交的 checkpoint，百分比只计已完成所有回调的区块。重启后计入已完成区间，重组后重新计算。运行器至少采到两段各五秒以上的实际处理进度后，才显示剩余时间估算。
- **回调次数和平均耗时**按回调名汇总。计数来自本次进程成功返回的调用。重组后重新执行的回调会再次计数，热重载后重新开始统计。

需要普通日志时使用 `ponder dev --disable-ui`。管道或文件重定向、`--log-format json`、`--log-level silent` 和 `TERM=dumb` 都会自动关闭进度显示。[终端与双后端测试](../parity/runs/native-dev-progress/RESULT.md)

`--hostname` 和 `--port` 也可用于 `start` 和 `serve`。端口 0 让操作系统分配空闲端口。使用 `runtime.LaunchApplication` 的 Go 应用会读取这两个选项。自行创建 HTTP 服务的应用可从 `PONDER_HOSTNAME`、`PONDER_PORT` 读取对应值。

## 控制 CLI 和 Go 应用日志

```bash
ponder --log-format json start --log-level info
ponder dev -v
ponder serve --log-level warn --log-format=json
```

`--log-level` 支持 `trace`、`debug`、`info`、`warn`、`error`、`fatal` 和 `silent`。默认是 `info`。显式级别优先，其次是 `-vv` 或 `--trace`、`-v` 或 `--debug`，再其次是环境变量 `PONDER_LOG_LEVEL`。`--log-format` 支持 `pretty` 和 `json`，默认 `pretty`。全局选项可以放在子命令前后。

CLI 和生成的 Go 应用将诊断写到标准错误，`--log-format json` 每行输出一条记录，包含毫秒 Unix 时间戳 `time`、数值级别 `level` 和消息 `msg`。例如 `debug` 为 20，`info` 为 30，`error` 为 50。`db list`、checkpoint 编解码等命令继续把结果写到标准输出，脚本可以分别重定向两个流。

`dev`、`start` 和 `serve` 把有效设置通过 `PONDER_LOG_LEVEL` 和 `PONDER_LOG_FORMAT` 传给子进程。自定义 Go 入口可用 `logging.New(os.Stderr, logging.Environment())` 创建同配置的日志器，用 `runtime.Options{Logger: logger}` 接收索引期间的诊断。应用自己直接写入标准输出或标准错误的文本仍由应用控制。[生成项目与测试](../parity/runs/native-cli-logging/RESULT.md)

## 用于自己的项目

在自己的工作目录中安装 CLI、创建索引器并构建。首次使用需要能访问 Go 模块代理或 GitHub。

```sh
go install github.com/ysh0566/ponder-go/cmd/ponder@main
"$(go env GOPATH)/bin/ponder" init my-indexer
cd my-indexer
go mod tidy
go build .
```

使用 PostgreSQL 时，通过数据库 URL 连接服务。使用 PGlite 时，在生成项目中执行 `npm install` 安装固定的数据库驱动。两种方式都用 Go 编写回调。[本地 PGlite](#使用本地-pglite)

如果设置了 `GOBIN`，CLI 位于该目录。要使用自己的仓库地址，可在项目中执行 `go mod edit -module 你的模块路径`。生成的项目包含自己的 `go.mod`、Go 事件处理函数和表定义。沿用上文的数据库、RPC 和合约地址，在项目目录执行 `ponder start --schema ponder_go_my_indexer`，其中 `ponder` 取安装后的实际路径。

- **从 Go 示例开始。** 在生成的项目中修改 `schema.json` 定义表和主键，在 `network.json` 设置链 ID、合约地址和起止区块，在 `handlers.go` 注册事件回调。默认 ERC-20 示例从区块 0 开始，使用已有合约时可将 `startBlock` 设为部署高度。[配置](SOURCE_CONFIGURATION.md) · [示例入口](../examples/erc20/main.go)
- **编写处理函数。** 示例在 `Token:Transfer` 中解码参数，通过事件上下文的 `c.DB` 更新两行。将写入放在回调事务中，以便索引器一起提交业务数据和 checkpoint，并在重组时撤销。[存储操作](DATABASE_ACTIONS.md)
- **生成模型。** 使用 `codegen --schema … --typed` 生成类型化模型。对自己的应用，将 `--out` 指向目标 Go 包目录，并用 `--package` 指定包名。`codegen --abi` 可生成事件和函数类型，`--app-config` 可生成回调名称与链类型。[类型化配置](TYPED_SOURCE_CONFIGURATION.md)
- **提供查询。** 示例用 `query.New` 创建 SQL 服务，通过 `runtime.LaunchApplication` 启动索引和 HTTP 监听，在查询模式下传入 `nil`。需要 GraphQL 时可用 `graphql.New` 创建处理器，通过 `http.ServeMux` 挂到 `/graphql` 并把 mux 赋给 `query.Server.API`。示例默认提供上表中的接口。[示例入口](../examples/erc20/main.go) · [GraphQL 实现](../graphql)

脚手架的 `runtime.Handler` 通过 `c.Client.Call` 发 JSON-RPC 请求。使用生成的回调注册表时，客户端类型是 `*indexing.Client`，可调用 `GetBalance`、`ReadContract` 等方法，也可用 `Request` 传入 RPC 方法和参数。使用原始 RPC 读取历史状态时，请显式传入事件的区块号。使用 `Multicall` 批量读合约时，默认采用链配置登记的 Multicall3 部署及起始高度。自定义链可通过 `MulticallOptions.MulticallAddress` 指定已部署的合约地址。[回调客户端方法](RUNTIME_SUPPORT.md#callback-rpc-methods)

批量模拟可使用 `Account` 指定外层调用的发送地址，使用 `StateOverride` 临时改变账户余额、nonce、代码或存储。下面的 `calls` 是要读取的 `[]indexing.ContractCall`，`slot` 和 `value` 是 32 字节十六进制字符串。

```go
results, err := args.Context.Client.Multicall(ctx, calls, indexing.MulticallOptions{
    Account: sender,
    StateOverride: indexing.StateOverride{
        target: {StateDiff: map[string]string{slot: value}},
    },
})
```

`StateDiff` 只覆盖指定存储槽，`State` 替换整个账户存储。覆盖只在本次批量模拟中生效。[双后端与缓存测试](../parity/runs/native-multicall-options/RESULT.md)

在生成的回调中，`args.Context.Client` 可直接查询交易和回执。按交易哈希查找时会查询节点当前的收录状态。按高度查找时会读取该高度的规范区块，重组后相同高度可能返回另一笔交易。确认数从收录区块到当前链头计算，待打包交易返回零。下例中的 `hash` 是交易哈希。

```go
client := args.Context.Client
receipt, err := client.GetTransactionReceipt(ctx, hash)
if err != nil {
    return err
}
confirmations, err := client.GetTransactionConfirmations(ctx, indexing.ConfirmationOptions{Receipt: receipt})
if err != nil {
    return err
}
log.Printf("transaction %s, block %v, confirmations %s", hash, receipt["blockNumber"], confirmations)
```

`GetTransaction` 和 `GetTransactionReceipt` 保留 JSON-RPC 字段表示，区块号和 gas 等数量是十六进制字符串。`GetTransactionCount` 读取账户 nonce，`GetBlockTransactionCount` 读取区块交易笔数，两者返回 `uint64`，默认读取当前链头。要在事件所在区块读取账户 nonce，可显式传入 `indexing.TransactionBlock{BlockNumber: n}`，其中 `n` 为 `*big.Int`。用 `TransactionLookup{Block: ..., Index: ...}` 可以按区块和交易序号定位交易。缺失的交易或回执默认会重试，可通过 `RetryEmptyResponse` 关闭重试。[回调交易 API 与重组实测](../parity/runs/native-transaction-actions/RESULT.md)

在 Go 回调里，`Call` 读取 EVM 返回的十六进制数据，没有返回数据时得到 `nil`。`EstimateGas` 返回 `*big.Int`。这两个方法接受与 JSON-RPC 相同的交易字段，数量使用十六进制字符串。默认在事件区块执行，可用 `CallOptions.BlockOptions` 指定其他高度。节点需要能够读取对应区块的状态。

```go
client := args.Context.Client
call := indexing.ContractCall{
    ABI: tokenABI, Address: tokenAddress, FunctionName: "transfer",
    Args: []any{recipient, amount},
}
sim, err := client.SimulateContract(ctx, call, indexing.SimulateContractOptions{
    Transaction: indexing.Object{"from": sender},
})
if err != nil {
    return err
}
gas, err := client.EstimateGas(ctx, sim.Request)
if err != nil {
    return err
}
log.Printf("transfer returned %v and would use about %s gas", sim.Result, gas)
```

`sim.Result` 是按 ABI 解码的结果，`sim.Request` 包含已经编码的交易。模拟过程中临时执行的转账和存储写入会随调用结束而丢弃。模拟时可以指定 `StateOverride`、RPC 格式的 `BlockOverrides` 或附加到 ABI 调用数据的 `DataSuffix`。复用交易做估算时，如果需要相同的区块和账户状态，请再次传入对应的 `CallOptions`。EIP-7702 授权列表使用 RPC 字段格式。只提供授权而省略目标地址时，客户端从首条签名推导收权账户，并按授权列表调整 gas 估算。

如需调用尚未部署的合约，可提供 `CallOptions{Deployless: &indexing.DeploylessCall{Code: creationCode}}`。也可指定 `Factory`、`FactoryData` 并在交易中给出预期目标地址。两种方式都由节点临时创建合约并执行调用。[模拟、gas 和重组验证](../parity/runs/native-contract-simulation/RESULT.md)

对于已经按 ENS 规则规范化的名称，可以用 `args.Context.Client.GetEnsResolver(ctx, "alice.eth")` 查询负责这个名称的解析器合约地址。默认读取事件所在区块，可通过 `indexing.ENSResolverOptions` 显式传入其他高度或 Universal Resolver 地址。成功返回零地址表示该次查询没有找到解析器。[ENS 历史查询示例与测试](../parity/runs/native-ens-resolver/RESULT.md)

在 Go 回调中，使用 `GetEnsAddress` 读取钱包地址，使用 `GetEnsText` 读取指定文本记录。两者默认读当前事件的区块；记录缺失时返回 `nil`。如果链上解析器报告缺失也需要中断索引，可以传入 `indexing.ENSNameOptions{Strict: true}`。

```go
wallet, err := client.GetEnsAddress(ctx, "alice.eth")
if err != nil {
    return err
}
url, err := client.GetEnsText(ctx, "alice.eth", "url")
if err != nil {
    return err
}
if wallet != nil && url != nil {
    log.Printf("wallet %s has website %s", *wallet, *url)
}
```

默认返回带校验和的以太坊地址。显式传入 `CoinType: big.NewInt(0)` 等 ENSIP-9 币种编号时，返回该币种的原始地址字节，以十六进制字符串表示。可用 `BlockOptions.BlockNumber` 指定高度，`GatewayURLs` 会传给 Universal Resolver。遇到 EIP-3668 时，客户端还会从合约指定的网关取数并交回合约验证。[双后端与上游验证](../parity/runs/native-ens-records/RESULT.md)

用 `avatar, err := client.GetEnsAvatar(ctx, "alice.eth")` 查询头像。成功时 `*avatar` 是图片 URL 或链上数据 URI，缺少记录或图片不可用时返回 `nil`。客户端在指定的 ENS 区块读取头像记录，与固定上游一致，NFT 合约的 `tokenURI` 或 `uri` 在同一链按 `latest` 读取。链外图片及元数据由查询时的网关提供。

`indexing.ENSAvatarOptions` 可设置 `IPFSGateway` 和 `ArweaveGateway`。默认 HTTP 客户端连接公网，超时 15 秒，远程 NFT 元数据上限为 4 MiB。使用自建内网网关时，可通过 `HTTPClient` 传入限制到该网关的传输配置。图片 URL 需通过 HTTP HEAD 检查。[头像示例与测试](../parity/runs/native-ens-avatar/RESULT.md)

在生成的 Go 回调中，`GetFeeHistory` 默认以事件所在区块作为费用区间的末端。`GetProof` 在同一区块读取账户及指定存储槽的 EIP-1186 证明。费用、余额和存储值使用 `*big.Int`，证明节点保留 RPC 返回的十六进制编码。

```go
history, err := client.GetFeeHistory(ctx, 4, []float64{25, 75})
if err != nil {
    return err
}
proof, err := client.GetProof(ctx, address, nil)
if err != nil {
    return err
}
log.Printf("oldest block %s, account nonce %d, balance %s", history.OldestBlock, proof.Nonce, proof.Balance)
```

传入 `[]string{slot}` 可同时读取存储证明，其中 `slot` 是 32 字节的十六进制存储键。两种方法均可通过 `indexing.BlockOptions{BlockNumber: n}` 指定其他高度。非创世区块在重组后会重新向节点读取。如显式设置 `Cache: "immutable"`，客户端会读取链头并允许复用已缓存的响应。[历史区块与重组验证](../parity/runs/native-state-reads/RESULT.md)

在仓库根目录查看为 `account` 表生成的 Go 模型。

```sh
model_dir=$(mktemp -d)
./bin/ponder codegen --schema examples/erc20/schema.json --typed --out "$model_dir/model.go"
head "$model_dir/model.go"
```

生成的模型用 `[]byte` 表示二进制，`json.RawMessage` 表示 JSON，`[2]float64` 和 `[3]float64` 表示点和线。SQL 数组使用 `[]orm.Nullable[T]`，可通过 `orm.Some(value)` 和 `orm.Null[T]()` 分别写入值和空元素。生成的 ABI 回调保留 tuple 的原始组件名，两个组件转换成相同 Go 名字时会得到不同的字段名。

### 选择 Go 项目模板

`ponder init --template <名称> <新目录>` 会生成 Go 处理函数、表定义及所选示例用到的 ABI。省略 `--template` 或选择 `go`、`erc20` 时生成本地 ERC-20 余额示例。下列模板的 `network.json` 预填了 Ponder 0.18.2 对应示例的链、监听地址和起始区块。

| 模板 | 参考网络与区块 | 可查询的数据 |
| --- | --- | --- |
| `empty` | Ethereum 主网，占位起始区块 1,234,567 | 提供 `example(id, name)` 空表和空 ABI，供项目自行定义事件及 Go 回调。 |
| `blocks` | Ethereum 主网，从 19,750,000 开始，每 5 个区块一次 | 读取 Chainlink 的 `latestAnswer()`，以区块时间戳为主键将报价记录到 `chainlink_price`。 |
| `multichain` | Ethereum、Base、Optimism、Polygon，各自首次启动时的链头（含） | 累计每条链上每个地址收到的 WETH9 `Deposit` 金额，主键为链 ID 和地址。 |
| `friendtech` | Base，从 2,430,440 开始 | 索引 Friendtech Shares V1 的买卖事件、持仓和双方的累计统计。 |
| `uniswap-v3-flash` | Ethereum 主网，从 12,369,621 开始 | 从 Uniswap V3 工厂发现池子，按代币汇总闪电贷事件。 |
| `filter` | Ethereum 主网，从 14,438,081 开始 | 只请求 PrimitiveManager 的 `Swap` 事件，在 `"swapEvent"` 中保存事件 ID、付款方和接收方。 |
| `reference-erc20` | Ethereum 主网，13,142,655 至 13,150,000（含） | 按地址维护余额，保存全部转账和授权事件。GraphQL 可从账户查询收付款记录，也可从转账查到双方账户。 |
| `api-functions` | Ethereum 主网，13,142,655 至 13,150,000（含） | 索引 ERC-20 转账和授权，提供计数、转账排名和 GraphQL 接口。 |
| `read-contract` | Ethereum 主网，从 15,963,553 开始 | 监听 FileStore 的文件创建和删除事件。`file` 保存文件内容、大小、创建时间及可选的元数据。 |
| `call-traces` | Ethereum 主网，从 19,800,000 开始 | 跟踪 Multicall3 的 `aggregate3()` 调用。`multicall` 按实际调用者保存累计 gas、输入计数和子调用成功或失败次数。 |
| `erc721` | Arbitrum One，3,163,146 至 3,200,000 | `token` 保存每个 NFT 的当前所有人，`transfer_event` 保留转手记录。 |
| `erc1155` | Ethereum 主网，从 12,129,118 开始 | `token_balance` 按持有人和代币 ID 记录余额。批量转账的每一项都有独立的 `transfer_event`。 |
| `erc4626` | Ethereum 主网，15,774,471 至 18,712,028 | `account` 和 `allowance` 保存份额余额与授权金额，另有转账、授权、存入和取出事件表。 |
| `factory` | Sepolia，从 4,121,269 开始 | 从 Llama 工厂创建事件中分别发现 Core 和 Policy 地址。运行日志与 `llama_event` 记录对应子合约的 `ActionCreated` 和 `Initialized` 事件。 |
| `accounts` | Ethereum 主网，首次启动时的链头（含） | 按收款地址汇总 BeaverBuilder 发出交易的 wei 总额和最近一次交易的 calldata。转入 BeaverBuilder 的原生币转账会写入应用日志。 |
| `proxy` | Ethereum 主网，从 17,942,156 开始 | 索引 Astaria Router 代理地址上的 `Liquidation` 和 `OwnershipTransferred`，分别记录清算人和新所有人。 |

用 `ponder init --template empty my-indexer` 新建自己的索引器。将 `network.json` 中的零地址和起始高度改为目标合约及部署高度，把事件 ABI 写入 `abi.json`，再在 `handlers.go` 的 `handlers` 中以 `ExampleContract:事件名` 注册回调。`schema.json` 定义回调写入的业务表。

用 `ponder init --template blocks my-prices` 生成区块采样项目。节点须能在来源区间按区块高度执行历史 `eth_call`。回调从起始高度开始每隔 5 块调用一次 `latestAnswer()`，将带符号的结果除以 10⁸。启动后可执行 `SELECT timestamp, price FROM chainlink_price ORDER BY timestamp` 查看报价。使用自己的预言机时，修改 `network.json` 中的 `address`，或设置 `PONDER_LOCAL_CONTRACT`。[本地双后端验证](../parity/runs/native-block-templates/RESULT.md)

上表中除 `api-functions` 外，各模板在 `/` 和 `/graphql` 提供 GraphQL，在 `/sql` 提供 SQL。`api-functions` 在 `/graphql` 提供 GraphQL，并自定义 HTTP 查询。本地余额入门模板（`go`、`erc20`）默认提供 SQL。[GraphQL 路由实现](../cmd/ponder/scaffold/graphql.go.tmpl)

用 `ponder init --template reference-erc20 my-token` 创建主网 ERC-20 项目。按下文设置 Go 模块依赖，将 `PONDER_RPC_URL_1` 指向可读取该区间历史日志的 Ethereum 节点，在项目目录运行 `ponder start`。首次见到付款地址时，回调将其余额记为零；首次见到收款地址时，记入本次收款金额。后续转账按收付方向更新。结果反映该区间内的活动。每次授权都会新增一条 `approval_event`，而 `allowance` 保留每对所有者与被授权地址的最新金额。

```sh
curl -fsS http://127.0.0.1:42069/graphql -H 'Content-Type: application/graphql' \
  --data '{ accounts(limit: 5) { items { address balance transferFromEvents { totalCount } transferToEvents { totalCount } } } }'
```

也可以查询 `{ transferEvents(limit: 5) { items { amount fromAccount { address } toAccount { address } } } }`，或用 SQL 执行 `SELECT owner, spender, amount FROM allowance`。[ERC-20 双后端验证](../parity/runs/native-reference-erc20/RESULT.md)

用 `ponder init --template friendtech my-shares` 新建 Friendtech 索引器，设置 `PONDER_RPC_URL_8453`。有效交易写入 `trade_event`，并更新按 subject 和 trader 组成的主键记录的 `share.amount`。份额为零的事件会被跳过。买入时 `trader_amount` 为 ETH 金额加协议费和 subject 费用，卖出时减去这两项费用。上游示例在 subject 或 trader 首次出现时创建零值汇总行，从该地址的后续交易起更新统计。查询逐笔交易可用 `SELECT trade_type, share_amount, trader_amount FROM trade_event ORDER BY id`。

用 `ponder init --template uniswap-v3-flash my-flash` 新建闪电贷索引器，设置 `PONDER_RPC_URL_1`。`network.json` 的地址指向工厂，Go 回调监听工厂通过 `PoolCreated` 公布的池子。每个池子的 `token0()` 和 `token1()` 用 `latest` 读取并按不可变结果缓存。`token_borrowed.amount` 累加借款额。按上游示例，`token_paid.amount` 首次写入该代币的实付额，之后每次累加该代币的借款额。各代币保留合约原始单位。两个模板均提供 SQL，以及位于 `/` 和 `/graphql` 的 GraphQL。[本地合约与双后端验证](../parity/runs/native-protocol-templates/RESULT.md)

用 `ponder init --template multichain my-weth` 创建多链索引器。将 `PONDER_RPC_URL_1`、`PONDER_RPC_URL_8453`、`PONDER_RPC_URL_10`、`PONDER_RPC_URL_137` 分别设为 Ethereum、Base、Optimism、Polygon 的 RPC 地址。每条链都可以另设 `PONDER_WS_URL_<链 ID>` 接收新区块通知。四条链使用各自的 RPC 和 `network.json` 中各自的合约地址。若只需其中几条链，可在新部署启动前删去其他网络条目。

按下方生成项目的依赖设置步骤准备好项目后，在项目目录运行 `ponder start`。首次启动时分别记录各链的起始高度，之后重启会从同一部署的进度补块。`handlers.go` 把 `Deposit(dst, wad)` 的金额加到 `account`，即从起始区块起的累计存款；转出、取款不参与这一指标。Isolated 排序使每条链独立推进，并在重组时只回滚对应链的写入。可用 SQL 或 GraphQL 按链查询同一地址。

```sql
SELECT chain_id, address, balance FROM account ORDER BY chain_id, address;
```

```sh
curl -fsS http://127.0.0.1:42069/graphql -H 'Content-Type: application/graphql' \
  --data '{ accounts(where: {chainId: 8453}) { items { address balance } } }'
```

状态接口 `/status` 按网络名返回进度。PostgreSQL 支持在相同部署上另开 `ponder serve` 提供查询，该进程只需数据库连接与项目配置。[四链存款与重组验证](../parity/runs/native-multichain-template/RESULT.md)

用 `ponder init --template filter my-swaps` 创建事件过滤项目。`indexer.go` 在合约配置中选定 `Swap`，生成的日志请求携带对应的主题。用 `SELECT id, payer, recipient FROM "swapEvent"` 查询结果。HTTP 服务也在 `/` 和 `/graphql` 提供 GraphQL，可以查询 `{ swapEvents { items { id payer recipient } } }`。

用 `ponder init --template api-functions my-api` 创建带自定义 HTTP 路由的项目。路由定义在 `api.go`，通过只读事务查询当前部署。项目公开三个自定义 GET 接口和 `/graphql`，并保留服务状态接口。此模板通过 `server.DisableSQL = true` 关闭内置 SQL 和 SQL 订阅接口。新地址首次出现时余额从零开始，同一地址再次参与转账时才按收付方向增减。该示例只统计配置区间内的活动。启动项目后，将下面的地址和端口改为应用日志中的实际值。

```sh
base=http://127.0.0.1:42069
curl -fsS "$base/count"
curl -fsS "$base/count/0x0000000000000000000000000000000000000000"
curl -sS "$base/whale-transfers"
curl -fsS "$base/graphql" -H 'Content-Type: application/graphql' --data '{ transferEvents { totalCount } }'
```

两个计数接口分别返回全部转账数和指定地址参与的转账数。自转账计一次。`/whale-transfers` 按发送方当前索引余额降序取最多十笔转账，每条含 `sender`、`senderBalance` 和 `amount`，金额按 18 位小数换算为字符串。空结果沿用上游的 HTTP 500 和 `Not found`。[本地验证](../parity/runs/native-filter-api-templates/RESULT.md)

用 `ponder init --template read-contract my-files` 索引 FileStore。创建事件触发后，Go 回调以交易接收方和文件名为参数，在事件区块调用 FileStoreFrontend 的 `readFile(address,string)`。节点需要能执行该区间的历史 `eth_call`。文件删除后，对应的业务行会一并删除。换用自己的部署时，将 `network.json` 的 `address` 改为事件合约地址，并更新 `handlers.go` 的 `frontendAddress`。查询示例为 `SELECT name, size, contents FROM file ORDER BY name`。

用 `ponder init --template call-traces my-multicalls` 跟踪 Multicall3。节点需为历史区块提供 `callTracer`。运行器先调用 `debug_traceBlockByHash`。若节点返回“方法不存在”（`-32601`），则逐笔调用 `debug_traceTransaction`。通过中间合约调用时，结果归在该中间合约地址下。可用 `SELECT "from", gas_used, bytes, successful_calls, failed_calls FROM multicall` 查询。`bytes` 沿用上游计算方式，每个子调用的输入按 4 字节向上取整为一个计数单位，再累加。[两个模板的验证记录](../parity/runs/native-contract-templates/RESULT.md)

下面的命令使用可读取上述历史区间的 Arbitrum RPC 和 PostgreSQL 启动 ERC-721 项目。

```sh
ponder init --template erc721 my-nfts
cd my-nfts
go mod tidy
export PONDER_RPC_URL_42161="$ARBITRUM_RPC_URL"
export DATABASE_URL="$INDEXER_DATABASE_URL"
ponder start --schema ponder_go_nfts
```

`PONDER_RPC_URL_<链ID>` 指向节点。单链模板还可用 `PONDER_LOCAL_RPC_URL` 和 `PONDER_LOCAL_CONTRACT` 分别覆盖节点和监听地址。切换到自建链时，同时将 `network.json` 中的 `name`、`id`、`startBlock` 和可选的 `endBlock` 改为该链及部署区间。工厂模板的 `address` 填工厂地址，账户模板填被观察的账户，代理模板填代理地址。Go 回调在 `handlers.go` 中，启动和查询服务在 `main.go` 中。使用 PGlite 时执行 `npm install` 安装驱动，再按本地 PGlite 一节启动。[链上事件验证](../parity/runs/native-project-templates/RESULT.md)

账户项目使用支持 `callTracer` 的节点来识别转入的原生币。首次用部署命名空间启动时，`"startBlock": "latest"` 将节点当前高度保存到数据库；同一部署重启时会补齐停机期间的区块。需要从指定高度回填时，在新部署的 `network.json` 填入该高度的整数。可以通过以下 SQL 核对累计金额和最后一次调用数据。

```sql
SELECT "to", value, data FROM transaction_events ORDER BY value DESC;
```

代理模板使用代理和实现合约的事件声明解码代理地址上的日志。查询 `liquidation_event.liquidator` 或 `ownership_transfer_event.new_owner` 可取得事件参数。实现合约被直接调用时从自身地址发出的日志会被地址过滤器排除。[账户和代理验证](../parity/runs/native-account-proxy-templates/RESULT.md)

### 同一条链上记录匹配多个回调

假设合约配置名为 `C`，ABI 同时声明下面两种 `Marked` 事件。它们的第一个 topic 相同，但一个把 `left` 存在 topic 中，另一个把 `right` 存在 topic 中。使用完整事件名注册 Go 处理函数，索引器会用各自的 ABI 声明解码。

```go
handlers := map[string]runtime.Handler{
    "C:Marked(uint256 indexed left, uint256 right)": handleLeftIndexed,
    "C:Marked(uint256 left, uint256 indexed right)": handleRightIndexed,
}
```

例如，日志的索引值为 7、数据值为 11 时，第一个回调会收到 `left=7, right=11`，第二个会收到 `left=11, right=7`。其中一个回调失败后重启，已经提交的回调会被跳过。若两个回调向同一张表各写一行，可用 `c.Event.Name` 和 `c.Event.ID` 作为联合主键，因为同一条链上记录的事件 ID 相同。函数选择器碰撞时，每个已注册的合约 CALL 回调也按自己的 ABI 解码；需在合约配置中启用 `IncludeCallTraces`。

如果此前运行的部署包含这种共享 topic 的事件声明，请使用新命名空间从合约起始块重新索引，以补齐旧索引遗漏的回调。[测试与重放示例](../parity/runs/native-callback-overlap/RESULT.md)

### 合约的 EIP-3668 链下读取

`Call`、`ReadContract`、`SimulateContract` 和 ENS 查询遇到 `OffchainLookup` 时自动检查返回的 sender 是否为目标合约。客户端按合约提供的 URL 顺序尝试网关，然后使用回调选择器将响应和原始附加数据交给合约验证。初次调用使用所选区块，验证回调与固定上游一致，使用同一条链的 `latest`。Universal Resolver 的本地批量请求可以嵌套，同批中某条查询失败时，其余结果仍会交给合约。

默认网关使用公网 HTTP 客户端。接入自建网关时，可通过 `CCIPReadOptions.HTTPClient` 配置受控传输。若处理函数需要记录节点返回的原始回滚，按次关闭自动解析即可。

```go
result, err := client.ReadContract(ctx, call, indexing.ReadContractOptions{
    CCIPRead: indexing.CCIPReadOptions{Disabled: true},
})
```

原始 `client.Request(ctx, "eth_call", params)` 直接返回节点结果。自动查询单次最多执行 8 层合约回调、8 层批量查询、64 次 HTTP 尝试和 64 个批量成员，网关响应正文累计上限为 4 MiB。调用方取消上下文会中断请求。[链下读取实测](../parity/runs/native-ccip-read/RESULT.md)

## 列类型与 GraphQL 表示

### 在 Go 模型中使用数据库枚举

例如，在 `schema.json` 中声明 `trade_type`，让交易表的 `kind` 列使用它。

```json
{
  "enums": [{"name": "trade_type", "values": ["BUY", "SELL"]}],
  "tables": [{"name": "trade", "columns": [
    {"name": "id", "type": "text", "primary": true},
    {"name": "kind", "type": "trade_type", "notNull": true}
  ]}]
}
```

运行 `ponder codegen --typed --schema schema.json --package models --out models/models.go` 前，先创建 `models` 目录。在 Go 回调里，已知的标签可直接使用生成的函数。

```go
input := models.NewTradeInsert("tx-1", models.TradeTypeBuy())
_, err := models.Trade.Bind(c.DB.Indexing()).Insert(ctx, input)
return err
```

从配置或外部请求读取标签时，调用 `models.ParseTradeType(label)` 并检查错误。解析区分大小写，`String()` 和 JSON 保留数据库中的原始标签。`TradeType` 的零值是声明的第一个标签，这里为 `BUY`。可空列使用 `orm.Nullable[models.TradeType]`，数组元素各自保留 SQL NULL。普通结构体生成和 `--typed --callback-db` 也提供同一组枚举取值。[可编译示例与数据库读写测试](../orm/enum_models_test.go)

### 为数据库列绑定自定义 Go 类型

例如，索引器可以在 Go 回调中使用 `time.Duration`，并在数据库中以 `text` 保存 `"1.5s"`。在 `schema.json` 的列上设置 `goCodec`。`type` 指定已有的存储类型，`goCodec.type` 和 `goCodec.name` 分别指向生成包中的 Go 类型和编解码器变量。两者均使用导出的 Go 标识符。

```json
{"tables":[{"name":"job","columns":[
  {"name":"id","type":"text","primary":true},
  {"name":"elapsed","type":"text","notNull":true,"goCodec":{"type":"Duration","name":"DurationCodec"}}
]}]}
```

在项目的 `models/duration.go` 中定义转换。解码函数接收存储层按列类型读出的 Go 值，例如 `text` 对应 `string`，`bytes` 对应 `[]byte`。可用类型别名引用其他包定义的类型。

```go
package models

import (
    "time"

    "github.com/ysh0566/ponder-go/orm"
)

type Duration = time.Duration

var DurationCodec = orm.ColumnCodec[Duration]{
    Encode: func(v Duration) (any, error) { return v.String(), nil },
    Decode: func(raw any) (Duration, error) {
        text, err := orm.DecodeString(raw)
        if err != nil {
            return 0, err
        }
        return time.ParseDuration(text)
    },
}
```

运行 `ponder codegen --schema schema.json --typed --package models --out models/models.go`。在回调中调用 `models.NewJobInsert("job-1", 1500*time.Millisecond)` 即可创建写入参数，读回的 `JobRow.Elapsed` 为 `time.Duration`。普通结构体模式也会生成对应的 Go 字段。生成模型的 JSON 使用该 Go 类型的 JSON 表示。

同一编解码器也可绑定到主键，或绑定到 `text[]` 以逐个转换非空元素。类型化数组使用 `[]orm.Nullable[T]`，可空列使用 `orm.Nullable[T]`。构建参数时会保存编码结果；执行写入时会先检查转换错误，批量写入中有一项编码失败，整批都不会提交。[可编译示例](../orm/testdata/custom/codec.go) · [双库读写测试](../orm/custom_codec_test.go)

### 网络类型与几何类型

在 `schema.json` 的 `columns` 中可以声明 `{"name":"subnet","type":"cidr"}`。同样可用 `inet`、`interval`、`macaddr`、`macaddr8` 和 `time`，数组类型在名称后加 `[]`。生成的 Go 模型以字符串保存这六类标量。数据库校验输入并返回规范形式，例如写入 `2001:0db8:0:0::1` 后可读到 `2001:db8::1`。

`point` 和 `line` 在 Go 模型中分别接受两个坐标和三个直线系数。GraphQL 以 PostgreSQL 文本表示这些值，例如点 `"(1,2)"` 和直线 `"{1,0,-1}"`。通过 `orderBy` 对几何列排序和翻页时，按规范文本的字典序排列。下面的查询适用于含有 `origin point` 和 `axis line` 两列的 `drawing` 表。[双后端读写与查询用例](../graphql/native_column_types_test.go)

```graphql
{ drawings(where: {origin: "(1,2)"}, orderBy: "origin") { items { id origin axis } } }
```

## 用 WebSocket 通知追块

在 `config.Chain` 中设置 `WS`，单链和独立多链运行器会在收到 `newHeads` 通知时立即查询 RPC，然后从持久化进度补齐所有新区块。

```go
chain := config.Chain{
    Name: "local",
    ID:   31337,
    RPC:  []string{"http://127.0.0.1:8545"},
    WS:   "ws://127.0.0.1:8545",
    PollingIntervalMS: 1000,
}
```

`PollingIntervalMS` 控制定时检查的间隔。订阅断线或网关拒绝订阅时，定时检查仍会追块。使用普通 HTTP 轮询的项目可以省略 `WS`。同一项目只更换通知地址时会沿用已有索引区间。[通知、断线与重连实测](../parity/runs/native-head-notifications/RESULT.md)

## 从同一个 Go 项目索引多条链

需要各链独立写入时，将 `store.New` 和 `config.Config.Ordering` 都设置为 `schema.Isolated`。每张业务表要有 SQL 列 `chain_id`、Go 模型属性 `chainId`，并将该列包含在主键中。在 `cfg.Chains` 配置各链的名称、ID 和 RPC，在每个来源的 `Chain` 指定所属链。多条链可以使用同一个来源名称和处理函数。回调从 `c.Chain.ID` 获取当前链 ID。

```go
worker, err := runtime.NewIndexer(cfg, db, handlers)
if err != nil {
    return err
}
routes := query.New(db)
for _, chain := range cfg.Chains {
    routes.ChainNames[chain.ID] = chain.Name
}
app, err := runtime.LaunchApplication(ctx, worker, routes, "127.0.0.1", 42069)
if err != nil {
    return err
}
return app.Wait()
```

原子存储的单链和独立多链索引器会根据每条链的 `ReorgWindow`（单位为秒，默认 180）推进最终确认位置。即使持续索引且没有设置 `EndBlock`，较早区块的撤销日志也会清理。窗口内的写入保留撤销记录，发生重组时索引器会回到共同祖先并重放新区块。将窗口设为 0 会立即确认已索引区块。[两个后端的重组验证](../parity/runs/native-continuous-finality/RESULT.md)

一条链的 RPC 临时不可用时，其他链继续追块，`/ready` 等待所有参与索引的链完成追赶。某条链所有来源均指定 `EndBlock` 时，该链处理到最后一个结束区块，等它达到配置的最终性后停止轮询。所有链结束后 HTTP 查询继续可用。

## 使用默认多链排序

要让多条链的 Go 回调更新同一张业务表，并允许较快的链先提交，在 `cfg.Chains` 中配置各链，在每个来源的 `Chain` 字段指定链名。下面沿用前例的 `cfg`、`handlers` 和存储后端。

```go
db, err := store.New(ctx, backend, model, namespace, schema.Multichain)
if err != nil {
    return err
}
cfg.Ordering = schema.Multichain // 新建配置也可省略 Ordering
worker, err := runtime.NewIndexer(cfg, db, handlers)
if err != nil {
    return err
}
```

将 `worker` 传给 `runtime.LaunchApplication`。同一轮可用的区块按 checkpoint 处理，单条链可以先于其他链提交。若链 A 重组，运行器从 A 上第一个失效回调起撤销其后的写入，并重放其他链受影响的回调。即使失效回调没有写行也会触发重放。单纯替换空块会保留其他链已提交的回调结果。检测到跨链回滚且有 RPC 离线时，运行器保留恢复标记，等各链可读取后再重放。这个标记可跨进程重启保留。[重组与恢复测试](../runtime/default_indexer_test.go)

默认排序的原子存储当前使用 `json-v4` 回执格式，包括只配置一条链的应用。如果显式指定的部署命名空间使用旧格式，换一个新的命名空间重新索引。使用独立业务库和同步库的部署可通过 `historical.OpenRuntimePostgres` 或 `historical.OpenRuntimePGlite` 创建托管运行器，其支持的来源与启动选项见运行矩阵。[运行模式矩阵](RUNTIME_SUPPORT.md)

### 用编译后的 Go 来源配置启动多链

如果项目用来源配置声明链、合约或区块，可以直接用整份构建结果创建索引器。下面的 `sourceConfig` 是 `config.ParseSourceConfiguration` 返回的配置，其中 `Head` 是在多条链上声明的区块来源。`db` 是按相同排序打开的 `*store.Store`，`onHead` 是 Go 函数 `func(context.Context, *runtime.Context) error`。

```go
base, err := config.BuildSourceConfig(sourceConfig, config.SourceBuildOptions{})
if err != nil {
    return err
}
build, err := config.BuildSourceIndexing(ctx, sourceConfig, base, []config.SourceIndexingFunction{
    {Name: "Head:block", Fn: runtime.Handler(onHead)},
})
if err != nil {
    return err
}
worker, err := runtime.NewIndexerFromSource(build, db, runtime.Options{})
if err != nil {
    return err
}
```

将 `worker` 交给 `runtime.LaunchApplication`，或直接调用 `RunIndexing`。排序来自来源配置，每条参与链保留自己的 RPC 和缓存策略。只有注册了回调的来源决定索引范围。仅注册 `合约名:setup` 的合约在其起始区块执行一次。某条链还有持续事件来源时，该链会继续追块。[运行模式](RUNTIME_SUPPORT.md) · [双后端验收](../parity/runs/native-compiled-indexer/RESULT.md)

### 为单条链配置 Go RPC 传输

在 `config.SourceBuildOptions.RPCTransports` 中使用来源配置的链名作为键。该链可以省略 `rpc` 地址。如果同时配置了地址，编译后的客户端使用传入的函数。其他链各自使用配置的节点池。下例将 `archive` 链的请求交给项目自己的客户端，适用于需要请求签名或使用非 HTTP 通道的索引器。

```go
// backend 实现 rpc.Requester 的 Call 方法。
options := config.SourceBuildOptions{
    RPCTransports: map[string]rpc.TransportFunc{
        "archive": func(ctx context.Context, method string, params any) (json.RawMessage, error) {
            var result json.RawMessage
            err := backend.Call(ctx, method, params, &result)
            return result, err
        },
    },
}
base, err := config.BuildSourceConfig(sourceConfig, options)
```

函数返回 JSON-RPC 响应中 `result` 的完整 JSON 值。例如，区块高度应编码为 `"0x10"`，空结果应编码为 `null`。协议错误可返回 `*rpc.Error`。函数需要支持并发调用并遵守传入的 `ctx`。每次调用执行一次请求，来源客户端负责排队和重试，每次尝试的超时上限是 10 秒。在 `runtime.NewIndexerFromSource` 中，历史取数与回调读取会使用同一条链的传输；缓存命中可直接返回持久化的结果。订阅新区块仍可单独配置 `ws` 地址。[传输与双后端验证](../parity/runs/native-custom-transport/RESULT.md)

### 原生多链索引器在数据库中断后恢复

数据库在处理链重组时退出后，重新启动原 PostgreSQL 实例，或重新打开原 PGlite 数据目录。用相同的命名空间、模型、配置和处理函数重新创建 `store.Store` 和 `runtime.NewIndexer`。数据库会丢弃未提交的回调事务。索引器会重新处理受影响的区块，并按当前链上的区块哈希更新进度。普通多链和 Omnichain 均覆盖一条链被替换、另一条链保持不变时的恢复。[双后端恢复测试](../parity/runs/native-multichain-engine-recovery/RESULT.md)

普通 Go 索引器按事件提交回调。一笔交易同时触发账户交易、合约日志、合约 CALL 和转账时，如果数据库在 CALL 回调写入后、提交前退出，重启会保留已提交的交易和日志，从 CALL 继续。在默认多链和 Omnichain 下，另一条链的日志回调若读取了旧分支的数据，也会用新分支的数据重新执行。Isolated 会保留另一条链的结果。[双链混合来源的中断位置与恢复结果](../parity/runs/native-mixed-engine-recovery/RESULT.md)

### 用 CLI 启动独立存储的多链索引器

仓库中的 Go 示例监听两条链。每条链从区块 1 开始，每个区块向业务表 `indexed_block` 插入一行。同步缓存使用单独的存储。回调和 HTTP 服务在同一个 Go 进程中运行。[示例代码](../examples/managed-multichain/main.go) · [链配置](../examples/managed-multichain/ponder.json)

在两个终端各启动一个本地节点，并保持运行。

```sh
# 终端 A
anvil --chain-id 1 --port 8545 --no-mining
# 终端 B
anvil --chain-id 2 --port 8546 --no-mining
```

在仓库根目录用第三个终端给每条链各出两个区块，然后启动索引器。以下命令使用本地 PGlite。要使用 PostgreSQL，在启动前设置 `DATABASE_URL`，或设置优先级更高的 `DATABASE_PRIVATE_URL`。

```sh
for port in 8545 8546; do
  curl -fsS "http://127.0.0.1:$port" -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"anvil_mine","params":["0x2"]}'
done
npm install --prefix examples/managed-multichain
go build -o bin/ponder ./cmd/ponder
./bin/ponder start --root examples/managed-multichain \
  --schema ponder_go_blocks --hostname 127.0.0.1 --port 42069 --shutdown-timeout 30s
```

在第四个终端查询进度和已提交的区块数。若使用了其他监听端口，将 `base` 改成启动日志中的地址。

```sh
base=http://127.0.0.1:42069
curl -sS -o /dev/null -w '%{http_code}\n' "$base/ready"
curl -fsS "$base/status" | jq .
sql='{"json":{"sql":"SELECT chain, count(*)::text AS n FROM indexed_block GROUP BY chain ORDER BY chain","params":[]}}'
curl -fsSG "$base/sql/db" --data-urlencode "sql=$sql" | jq .rows
curl -NsSG "$base/sql/live" --data-urlencode "sql=$sql"
```

在全新的节点上，回填期间 `/health` 返回 200，`/ready` 和实时订阅返回 503。就绪后，`/status` 显示 `one` 和 `two` 的高度均为 2，SQL 显示每条链各两行。保持订阅打开，向任一节点再发一次 `anvil_mine`，该链的计数随提交更新。

用 Ctrl-C 停止索引器，保留两个节点。`start` 默认给子进程 5 秒完成退出，示例用 `--shutdown-timeout 30s` 为两个 PGlite 引擎完成事务和关闭留出时间。停机期间出块后，重新执行同一条 `ponder start` 即可补块。PGlite 将两个目录保存在示例的 `.ponder/ponder_go_blocks/` 下。PostgreSQL 使用 `ponder_go_blocks` 和 `ponder_go_blocks_cache` 两个命名空间。自己的 Go 应用可以调用 `runner.QueryServer(ctx)` 创建查询服务，再将 `runner` 和该服务传给 `runtime.LaunchApplication`。修改处理函数逻辑后，用新的 `BuildID` 和命名空间启动新部署。退出时等待应用停止，依次关闭查询服务和运行器。[端到端测试](../cmd/ponder/managed_start_test.go)

### 双库 Go 项目的热重载

启动上一节的两条 Anvil 链，在仓库根目录运行以下命令。用 PostgreSQL 时设置 `DATABASE_PRIVATE_URL` 或 `DATABASE_URL`。用 PGlite 时按上一节安装驱动，并将这两个环境变量留空。

```sh
./bin/ponder dev --root examples/managed-multichain \
  --schema ponder_go_blocks_dev --hostname 127.0.0.1 --port 42070 \
  --shutdown-timeout 30s
```

编辑 `examples/managed-multichain/main.go` 中的 `recordBlock` 并保存。例如，将写入的 `"chain": chain` 改为 `"chain": chain + "-v2"`。编译成功后，CLI 关闭旧进程，用当前修订的业务库和同步缓存重新索引。在上节的查询命令中设 `base=http://127.0.0.1:42070`，即可看到已索引区块也带上新的链名后缀。编译失败时，原进程继续追块并响应查询。恢复原项目内容会重新打开该修订的存储并补齐缺少的区块。

CLI 将完整修订哈希传给子进程的 `PONDER_DEV_REVISION`，将对应业务命名空间传给 `DATABASE_SCHEMA`。示例把修订号用作 `BuildID`，并设置 `RuntimeOptions.Development`。PostgreSQL 为每个修订使用各自的业务和缓存命名空间。PGlite 将每个修订的两个目录保存在 `.ponder/<业务命名空间>/`。开发模式的查询走当前进程的 HTTP 地址，生产部署的公共视图由 `start` 管理。[双后端热重载测试](../parity/runs/native-managed-cli-dev/RESULT.md)

### 双库 PostgreSQL 的独立查询进程

用 PostgreSQL 运行上一节的 `ponder start`，等写入进程建立业务命名空间后，可以在另一终端运行以下命令。将 `READER_DATABASE_URL` 设为同一数据库的只读账号连接串，`--schema` 填写入进程的业务命名空间。查询进程从业务库读取链名、检查点和就绪标记，运行时只需项目的 Go 代码和数据库连接。[Go 示例入口](../examples/managed-multichain/main.go)

```sh
DATABASE_PRIVATE_URL="$READER_DATABASE_URL" ./bin/ponder serve \
  --root examples/managed-multichain --schema ponder_go_blocks \
  --hostname 127.0.0.1 --port 42070
curl -s http://127.0.0.1:42070/status
```

以业务表所有者身份给现有的 PostgreSQL 登录角色授权。下面的角色名、业务命名空间及表名对应这个示例。账号还需具备该数据库的 `CONNECT` 权限。

```sql
GRANT USAGE ON SCHEMA ponder_go_blocks TO query_reader;
GRANT SELECT ON ponder_go_blocks.indexed_block,
                ponder_go_blocks._ponder_go_meta,
                ponder_go_blocks._ponder_go_app,
                ponder_go_blocks._ponder_go_chains,
                ponder_go_blocks._ponder_go_progress TO query_reader;
```

历史回填期间，`/health` 可用，`/ready` 和新建的 `/sql/live` 请求返回 503。写入进程发布就绪状态后，查询端能按链名返回区块高度，已有 SSE 连接也能接收写入进程重启后补入的数据。查询角色只需读取业务命名空间。独立查询部署使用 PostgreSQL。使用 PGlite 时可访问 `start` 自带的 HTTP 服务。

自己的 Go 应用可以调用 `query.OpenManaged(ctx, backend, model, namespace, ordering)`，再将返回值传给 `runtime.LaunchApplication(ctx, nil, routes, host, port)`。退出时先停止 HTTP 服务，再关闭查询服务和 PostgreSQL 连接。[运行和重启测试](../parity/runs/native-managed-cli-serve/RESULT.md)

### 独立存储的有限区间索引

Go 应用通过 `historical.OpenRuntimePostgres` 或 `historical.OpenRuntimePGlite` 管理独立的业务数据与同步数据时，可以在默认多链排序下将 `RuntimeOptions.Live` 留空，并给每个事件来源设置 `endBlock`。打开时各链都必须已经到达配置的结束高度。要包含创世区块，请为来源设置 `startBlock: 0` 并将 `RuntimeOptions.SourceCheckpoints` 设为 `true`。此时初始 checkpoint 标记区块的起点，回调会收到区块 0。只需索引创世区块时可设置 `endBlock: 0`。新建部署时选择 checkpoint 模式，重开原存储时沿用相同设置。不启用该选项时，有限区间从区块 1 或更高处开始。调用 `Run` 后，索引器处理到各链的结束高度并返回 `Completed`。[来源 checkpoint 验证](../parity/runs/native-bounded-source-checkpoints/RESULT.md)

默认多链将 `Live` 留空时，所有结束高度都需要在打开作业时已经存在。若未确认的区块发生分叉，运行器会暂停就绪状态，协调业务库和同步缓存回滚，并重新执行受影响的跨链回调。追平回滚前各链的进度后恢复就绪。返回 `Completed` 前还会按高度核对各链未确认的结束区块，因此已经先跑完的链也能发现同高度替换。如果分叉穿过本次打开时的最终确认边界，`Run` 会返回错误。[双库有限区间分叉测试](../parity/runs/native-bounded-reorg/RESULT.md)

`RuntimeOptions.Historical.MaxQueryParameters` 控制同步库单条批量 INSERT 可使用的绑定参数数量，适用于区块、日志、交易、回执、调用轨迹、同步区间和工厂子地址。默认值为 16000。运行器按每行需要的参数个数拆批。例如，每条日志需要 12 个参数，设置为 25 时，每条 INSERT 最多写入两条日志。预算容不下一行时，错误会指出数据表和所需的参数数量。可保留原 `BuildID` 和两个存储位置，使用新的 `OpenID` 和更大的预算重启，继续已提交的进度。该设置适用于两种后端和当前支持的排序模式。Omnichain 的 `Historical.QuerySize` 单独控制历史事件查询的分页。[配置与恢复测试](../parity/runs/native-runtime-query-budget/RESULT.md)

要让默认多链等待尚未出现的结束区块，给每个来源设置有限的 `endBlock`，并设置 `RuntimeOptions.Live = &historical.RuntimeLiveOptions{}`。提前启动后，运行器先处理现有区块，再轮询各链，所有链都达到各自的结束高度时返回 `Completed`。到达结束高度时仍在重组窗口内的区块，重启后可能从安全检查点重新执行。[双链未来结束高度与重放测试](../historical/live_runtime_continuous_test.go)

可在同一个构建中注册合约的 `合约名:setup` 回调，并逐链配置 `cacheRpcRequests`。setup 在首个事件前提交。第二条链的 setup 失败时，两条链的初始化写入会一起回滚。用相同的 `BuildID`、存储位置和新的 `OpenID` 重试后可重新执行初始化。对关闭缓存的链，回调通过 RPC 读取区块状态，已有的 RPC 缓存记录会保留。独立存储的 Omnichain 有自己的来源与最终确认要求。[双后端测试](../parity/runs/native-bounded-setup-cache/RESULT.md) · [运行模式矩阵](RUNTIME_SUPPORT.md)

在默认多链排序下使用工厂合约发现子地址时，将 `RuntimeOptions.FactorySources` 和 `SourceCheckpoints` 都设为 `true`。将 `Live` 留空即可在固定高度结束。工厂地址配置里的 `startBlock` 和 `endBlock` 决定扫描创建事件的范围，子合约或账户配置的区间决定向 Go 回调投递哪些事件。例如，工厂在区块 1 创建的子合约，即使其日志回调从区块 3 才开始，仍可在区块 3 收到事件。重开同一部署时，运行器按区块哈希恢复子地址，RPC 节点需能返回创建区间的区块和日志。未最终确认的区块被替换后，回放会使用新分支创建的子地址。[有限区间工厂测试](../parity/runs/native-bounded-factory/RESULT.md)

独立存储的 Omnichain 会自动启用来源 checkpoint。可以注册一个或多个区块回调，各自选择采样间隔，也可以只订阅合约日志、合约 CALL 或账户事件。每个来源都需要声明有限区间。将来源的 `startBlock` 和 `endBlock` 都设为 `0` 可以只处理创世区块。区块回调会收到区块 0，日志回调会收到区块 0 内匹配的日志。各链的结束位置必须都已进入全局最终确认范围。[Omnichain 创世区块测试](../parity/runs/native-omnichain-genesis/RESULT.md)

#### 订阅双库索引器的事件组提交

在 `historical.RuntimeOptions` 上设置 `OnCommitted`，可以在业务事务提交后收到每条链的事件组进度。默认多链和 Isolated 支持回填、实时追块和重放；Omnichain 支持已最终确认的有限区间。

```go
opts.OnCommitted = func(ctx context.Context, c historical.RuntimeCommit) error {
    log.Printf("operation=%s chain=%d block=%s checkpoint=%s events=%d historical=%t safe=%t",
        c.OperationID, c.ChainID, c.Head.Hash, c.Checkpoint, c.Events, c.Historical, c.Safe)
    return nil
}
```

`Checkpoint` 是本组的来源位置，`Head` 是提交后该链实际扫描到的区块头。一个区块没有匹配到事件时，提交仍能推进扫描位置，通知中的 `Events` 为 0。Omnichain 的一笔事务可以同时提交多条链的事件组，这些通知共享 `OperationID`。回调里可以通过运行器的 `Control(ctx)` 读取已提交的进度。

`Historical` 表示该组使用了历史回填处理逻辑。`Safe` 表示**通知发生时**，持久化的安全检查点已覆盖本组。默认多链会单独推进安全检查点，因此已经写入业务库的历史组也可能收到 `Safe == false`。如果之后发生分叉或重启，同一高度的重放会有新的提交编号，替换分支还可能带来新的区块哈希。

回调返回错误时，运行器会发完这笔事务中其余组的通知，随后从 `Run` 返回错误。进程若在提交后、回调前退出，应用可能收不到该次通知。只关注有限 Isolated 或 Omnichain 的安全回填进度时，还可以使用下面的历史回调。两者同时设置时，每组先调用历史回调，再调用运行器回调。[提交与重放测试](../parity/runs/native-runtime-commit-notifications/RESULT.md)

#### 接收双库回填的提交通知

使用有限区间的 Isolated 或 Omnichain 索引器时，把下面的函数设置在传给 `historical.OpenRuntimePostgres` 或 `historical.OpenRuntimePGlite` 的选项上。每个 `GroupResult` 对应一条链，包含已提交的来源 checkpoint、区块范围和事件数。Isolated 每次提交一个区块。Omnichain 的一笔事务可以同时提交多条链的事件组。

```go
opts.Historical.OnCommitted = func(ctx context.Context, group historical.GroupResult) error {
    log.Printf("chain=%d checkpoint=%s range=%v events=%d attempts=%d",
        group.ChainID, group.Checkpoint, group.BlockRange, group.Events, group.Attempts)
    return nil
}
```

收到通知时，业务数据与安全检查点已经提交，函数中可以调用运行器的 `Control(ctx)` 读取进度。`Attempts` 是该批次尝试执行的次数，包括补取事件输入后的重试。若数据库提交应答丢失、运行器通过数据库中的提交记录确认成功，`Reconciled` 为真。

通知函数返回错误时，运行器会发完同一笔事务内其他链的通知，随后从 `Run` 返回错误。重新打开同一部署会从安全检查点继续。若进程在提交后、调用通知函数前退出，重启会跳过已经提交的事件组。初始化和就绪分别使用 `OnSetupCommitted` 和 `OnActivated`。

Omnichain 的 `opts.Historical.Clock` 同时用于回填协调器的链头刷新计时与本地同步的区间大小估算。默认使用系统时间。传入自定义函数时，它需要支持多个链并发调用。链上区块时间和 checkpoint 仍由 RPC 区块头确定。[提交、报错与恢复测试](../parity/runs/native-historical-commit-observers/RESULT.md)

#### 双库 PostgreSQL 回填的事务重试

`historical.OpenRuntimePostgres` 在初始化和历史回填的业务事务遇到 PostgreSQL 死锁（`40P01`）或序列化冲突（`40001`）时重试。默认从 125 毫秒开始等待，每次翻倍，最多重试 9 次。若 PostgreSQL 在提交时报告冲突，运行器先重新核对所有权、操作记录和提交前的进度，再决定能否重跑同一批。重试可能再次调用同一个 Go 处理函数，业务结果应写入该回调的数据库事务。

应用可设置 `opts.Historical.RetryWait` 记录等待，或在自己的时间预算用尽时返回错误。下面的例子将单次等待限制为两秒，关闭运行器或取消作业会中断等待。

```go
opts.Historical.RetryWait = func(ctx context.Context, delay time.Duration) error {
    timer := time.NewTimer(min(delay, 2*time.Second))
    defer timer.Stop()
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-timer.C:
        return nil
    }
}
```

PGlite 对每笔事务执行一次。实时轮询、Go 处理函数的业务错误和失去数据库连接都有各自的退出或恢复流程。提交结果无法确认时，本次运行返回错误，应用应关闭运行器并使用同一部署、全新的 `OpenID` 重新打开。[事务冲突与重启验证](../parity/runs/native-historical-retry-wait/RESULT.md)

#### 等待双库 Omnichain 的区间最终确认

两个构造函数都支持 `RuntimeOptions.OmnichainFinalityPollInterval`。零值表示在打开时检查一次。正值表示按指定间隔重新读取各链的最新区块，再按各自的重组窗口计算最终确认位置。例如，链 A 已产生结束区块，链 B 的最终确认时间仍早于这个区块时，作业会继续等待。若某个 `setup` 回调读取的区块高于该链最后一个事件，也会等到该区块最终确认。

下面的 `opts` 是已配置好存储、来源和 Go 回调的 `historical.RuntimeOptions`。示例为等待和回填合计预留十分钟，作业结束后释放上下文。

```go
opts.OmnichainFinalityPollInterval = 3 * time.Second
jobCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
defer cancel()
runner, err := historical.OpenRuntimePostgres(jobCtx, opts)
if err != nil {
    return err
}
defer runner.Close(context.Background())
_, err = runner.Run(jobCtx)
return err
```

等待期间，运行器只读取链数据。达到条件后才打开业务库和同步库，随后由 `Run` 执行回调。取消等待或达到超时会结束这次打开。节点请求返回的错误和无效区块头也会返回给调用方。使用 PGlite 时将构造函数换成 `historical.OpenRuntimePGlite`。[双后端回填与取消测试](../parity/runs/native-omnichain-finality-wait/RESULT.md)

#### 配置双库 Omnichain 的区块回调

例如，在 Go 来源配置的 `blocks` 字段中加入以下两个来源，然后向 `config.BuildSourceIndexing` 注册 `Sample:block` 和 `Audit:block`。采样从各自的 `startBlock` 开始，两个端点都包含在内。

```go
blocks := map[string]any{
    "Sample": map[string]any{"chain": "mainnet", "startBlock": 1, "endBlock": 8, "interval": 5},
    "Audit":  map[string]any{"chain": "mainnet", "startBlock": 2, "endBlock": 7, "interval": 2},
}
```

`Sample` 在第 1、6 块触发，`Audit` 在第 2、4、6 块触发。第 6 块按注册顺序执行两个回调。如果第二个回调失败，重启后会一起重放这个未提交的批次。第 7、8 块均无匹配时，运行器仍会验证扫描已到达结束高度。相同配置可配合每条链各自的 RPC 缓存设置。[双链采样与恢复测试](../historical/omnichain_block_intervals_test.go)


在双库 Omnichain 中，可以只订阅静态或工厂子合约的 CALL。在来源配置中启用 `includeCallTraces` 并提供函数 ABI（使用 `config.SourceTypedContract` 时设为 `IncludeCallTraces: config.SourceSome(true)`），然后注册函数回调，例如 `config.SourceIndexingFunction{Name: "Child.record()", Fn: record}`。每条链可独立设置 `cacheRpcRequests`。关闭缓存的链需要能按已最终确认的区块哈希返回历史调用树、交易和回执。回调失败后，用相同的 `BuildID`、原数据库和新的 `OpenID` 重启即可从已提交位置继续。[双链与双后端验证](../parity/runs/native-omnichain-sparse-contract-calls/RESULT.md)

要按工厂发现的地址订阅合约日志、CALL 或账户事件，请将下面的 `children` 赋给合约或账户声明的 `Address`，然后编译配置。示例用工厂的 `Created(address indexed child)` 事件发现子地址。

```go
created := abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{
    {Name: "child", Type: "address", Indexed: true},
}}
children, err := config.FactoryByParameter(created, config.DynamicFactoryParameter("child"),
    config.FactoryOptions{Address: factoryAddress, StartBlock: 100, EndBlock: 199})
if err != nil {
    return err
}
```

打开运行器时同时设置 `RuntimeOptions.FactorySources = true` 和 `SourceCheckpoints = true`，并将 `Live` 留空。子地址从发现它的区块起生效，同一区块内的匹配事件也会投递。每条链独立记录发现结果。开启缓存的链会持久化子地址及其首次出现的高度。关闭缓存的链会在重启时重新读取工厂发现区间，所以节点需要保留该区间的历史日志。[发现、回滚与重启测试](../parity/runs/native-omnichain-factory/RESULT.md)

例如，工厂在区块 100 至 199 登记子合约，子合约可以监听到区块 250。在一个没有业务事件的区间恢复时，关闭缓存的链还会读取此前的创建日志，以确定哪些地址当时已登记。合约在登记高度之前发出的日志会被排除。[两条链上的纯日志工厂示例和恢复验证](../parity/runs/native-omnichain-log-factory/RESULT.md)

同一条链上的两个静态日志来源可以有不同的起止区块，端点都包含在内。下面的 Go 配置将两个合约放在 `mainnet` 上。假设两者都声明了 `Transfer` 事件，区块 150 到 199 内两者的回调都可能收到事件，区块 200 到 299 内只有 `Late:Transfer` 能收到事件。`transferABI` 是包含该事件的 `[]abi.Event`，两个地址分别指向对应合约。

```go
contracts := map[string]config.ContractDeclaration[[]abi.Event, string, any]{
    "Early": {ABI: transferABI, Chain: "mainnet", Address: earlyAddress, StartBlock: 100, EndBlock: 199},
    "Late":  {ABI: transferABI, Chain: "mainnet", Address: lateAddress, StartBlock: 150, EndBlock: 299},
}
```

也可以为两个来源设置不相交的区间。重启时，运行器分别核对各来源在其有效区间里的扫描记录。同步库中即使已经存有其他高度的同签名日志，也只会派发声明范围内的事件。[区间、缓存与重放测试](../parity/runs/native-omnichain-log-ranges/RESULT.md)

纯静态日志索引会用每个匹配回调的 ABI 检查日志。例如，合约发出的日志缺少 `uint256` 参数时，无法解码它的回调会跳过。即使一条链的剩余日志全部被跳过，只要运行器能核对所有来源已扫描到各自的结束高度，索引器仍可就绪。扫描结束高度和最后提交的业务检查点可能不同。用同一配置重启后，已提交的回调不会重复执行。[区间末尾与重启测试](../parity/runs/native-omnichain-abi-tail/RESULT.md)

#### 在不同链上使用不同的回调

双库 Omnichain 支持直接回填账户事件。给账户来源设置有限的 `startBlock` 和 `endBlock`，再注册需要的 Go 回调。例如，一个收款账户可注册下面两项。

```go
handlers := []config.SourceIndexingFunction{
    {Name: "Wallet:transaction:to", Fn: recordIncomingTransaction},
    {Name: "Wallet:transfer:to", Fn: recordIncomingTransfer},
}
```

`recordIncomingTransaction` 和 `recordIncomingTransfer` 是项目自己的 `indexing.Handler`。每条链的结束区块都必须处于全局已最终确认的范围内。启用缓存时，运行器会在同步库中核对区间覆盖、交易回执和转账 trace。最后一次回调可能早于扫描结束高度。重启时沿用原来的配置、存储和 `BuildID`，并使用新的 `OpenID`。[两条本地链的恢复验证](../parity/runs/native-omnichain-sparse-accounts/RESULT.md)

将某条链的 `cacheRpcRequests` 设为 `false` 时，运行器会直接向该链节点复核账户来源覆盖的每个区块，包括重启时需要重新核对的历史区间。节点须能按哈希返回完整区块和交易数（`eth_getBlockByHash`、`eth_getBlockTransactionCountByHash`），并提供整块或逐笔交易回执。订阅原生币转账还需要 `debug_traceBlockByHash` 的 `callTracer`。节点缺少数据时，本次扫描会返回错误，进度保留在已提交的回调组。[关闭缓存的双链验证](../parity/runs/native-omnichain-uncached-accounts/RESULT.md)

例如，若要在 `mainnet` 同时索引账户交易和合约日志，在 `base` 只索引工厂子合约的日志，可将逐块来源（`interval: 1`、`offset: 0`）和账户来源的 `chain` 都设为 `mainnet`，在两条链上分别注册合约日志来源。每个来源可以指定自己的起止高度。所有结束高度都须处于各链共同的最终确认范围内。

运行器按同一跨链顺序提交两类回调。逐块链执行到结束区块。纯日志链的最后一个事件可以早于结束区块，运行器会核对两者之间的扫描记录。使用相同的配置、数据库和 `BuildID` 重启后，已提交的回调不会重复执行。[可运行的 Go 配置和恢复测试](../historical/omnichain_factory_test.go) · [双后端验证](../parity/runs/native-omnichain-mixed/RESULT.md)

可以逐链设置 RPC 缓存。下面的配置让该链在内存中处理历史事件，回调直接从节点读取链上状态。其他链可保留默认设置。关闭缓存的链仍然保存业务数据和检查点。重启时使用相同的缓存设置和存储位置。[逐块回调缓存测试](../parity/runs/native-omnichain-cache-policy/RESULT.md)

```go
cache := false
chain := config.ChainDeclaration[uint64, string]{
    ID: 1, RPC: rpcURL, CacheRPCRequests: &cache,
}
```

在纯静态日志模式下，关闭缓存的链需要 RPC 端点能够持续提供所配置区间的历史日志和区块头。处理事件之间的空区间时，运行器会再次分页调用 `eth_getLogs`，并在发布就绪状态前按来源复查完整区间。关闭缓存可省去持久化原始日志和缓存区间的空间，代价是额外的 RPC 请求。复查失败时本次运行无法就绪，已提交的回调可以在重启后接续。[无缓存扫描与重启测试](../parity/runs/native-omnichain-uncached-logs/RESULT.md)

默认多链与 Omnichain 的已确认区间都可以在 `config.BuildSourceIndexing` 的处理函数列表中注册初始化回调。例如，配置中已有名为 `Token` 的合约，下面这项会在收到首个来源事件前运行。示例中的 `account` 表需要文本列 `id`（主键）和 `value`。

```go
functions = append(functions, config.SourceIndexingFunction{
    Name: "Token:setup",
    Fn: indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
        _, err := a.Context.DB.InsertOne(ctx, "account", store.Row{
            "id": fmt.Sprintf("initial-%v", a.Context.Chain["id"]), "value": "ready",
        }, store.Conflict{})
        return err
    }),
})
```

对每条参与链，`setup` 使用该合约在链上配置的 `startBlock` 作为默认 RPC 读取高度，省略时为 0。只用于初始化的合约也可以选择晚于事件区间的高度，但该高度必须已经在本链及跨链的最终确认范围内。所有链的初始化写入和初始 checkpoint 一起提交。若任一回调失败，使用相同配置、存储和 `BuildID`，换一个新的 `OpenID` 重试，会重新执行整组初始化。若初始化已提交，重启会继续索引事件，已提交的初始化只执行一次。[PostgreSQL 与 PGlite 回滚和重启测试](../parity/runs/native-omnichain-setup/RESULT.md)

要同时处理账户和合约调用，可在同一条链注册相应的账户与合约回调。如果还需要按区块更新业务表，可添加区块来源，并注册其 `名称:block` 回调。静态账户可注册 `Wallet:transaction:from`、`Wallet:transaction:to`、`Wallet:transfer:from` 和 `Wallet:transfer:to`。对静态合约启用 `includeCallTraces`，再按 ABI 注册函数回调，例如 `Worker.record()`。需要读取回执时，在相应来源配置中启用 `includeTransactionReceipts`。同一笔有金额的合约调用可同时触发交易、函数和转账回调，最后执行该块的区块回调。[两条 Anvil 链上的运行与重启用例](../historical/omnichain_account_events_test.go)

若索引进程在已确认区间的区块或静态日志回调中突然退出，用原来的业务库和同步库、同一份来源配置和相同的 `BuildID` 重新打开，并为这次打开分配新的 `OpenID`。运行器从已提交的跨链回调组继续。若进程在运行器进入就绪状态后退出，已经完成的回调不会再次执行。PostgreSQL 使用原来的数据库及命名空间，PGlite 使用原来的两个目录。[应用进程退出验证](../parity/runs/native-omnichain-app-recovery/RESULT.md)

若数据库引擎在静态日志回调里退出，先关闭失败的运行器。PostgreSQL 使用原有数据卷重启服务。PGlite 重新打开原有业务和同步目录。沿用同一来源配置与 `BuildID`，用新的 `OpenID` 启动。已提交的跨链事件组会保留，中断的事务会回滚，其回调在恢复时重新执行。带逐块回调和纯日志这两种已确认区间模式都覆盖了这个场景。[引擎中断与恢复测试](../parity/runs/native-omnichain-engine-recovery/RESULT.md)

### 双库 Isolated 按链回填

需要将业务数据与同步缓存分开保存，并让各链独立维护记录时，在 Go 来源配置里将 `ordering` 设为 `string(schema.Isolated)`（配置值为 `experimental_isolated`）。为每个来源指定有限的结束高度。每张链上业务表都要把整数列 `chain_id`（Go 属性 `chainId`）放入主键。例如，两个网络可各自保存同一高度的记录。

```go
seen := schema.Table{
    Name: "seen", PrimaryKey: []string{"chain_id", "height"},
    Columns: []schema.Column{
        {Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true},
        {Name: "height", Type: "bigint", NotNull: true},
    },
}
opts.Historical.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{seen}}
opts.Historical.Owned.Storage.Ordering = schema.Isolated
opts.Live = nil
```

用 `config.BuildSourceIndexing` 注册 `indexing.Handler`，将编译结果赋给 `opts.Historical.Build`。准备好业务和同步存储、`Requesters`、`BuildID` 与 `OpenID` 后，用 `historical.OpenRuntimePostgres` 或 `historical.OpenRuntimePGlite` 打开并调用 `Run`。启动时需要连通所有参与链，各来源的结束区块必须已在本链最终确认。每条链可从区块 0 开始，无需对齐各链的区块高度或时间戳。

回调通过 `a.Context.DB` 写入本链的 `chainId`。每个区块的业务变更与该链的安全进度一起提交。若回调失败，本次运行返回错误。关闭后沿用两个存储位置、配置和 `BuildID`，换一个新的 `OpenID` 即可继续。各链已成功提交的回调都会保留。如需处理尚未最终确认的区块，可启用实时轮询。[双库 Isolated 实时追块](#双库-isolated-实时追块) · [完整的双链 Go 示例和恢复断言](../historical/isolated_finalized_test.go) · [运行模式矩阵](RUNTIME_SUPPORT.md)

要在回填前为各链写入初始业务数据，请在 Go 配置里声明合约（例如 `Token`），并通过 `config.BuildSourceIndexing` 注册 `Token:setup`。合约的逐链 `startBlock` 决定该链初始化时的默认 RPC 读取高度，省略时从 0 读取。该高度必须已在本链最终确认。下面的回调适用于上方的 `seen` 表。

```go
initToken := config.SourceIndexingFunction{
    Name: "Token:setup",
    Fn: indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
        _, err := a.Context.DB.InsertOne(ctx, "seen", store.Row{
            "chainId": a.Context.Chain["id"], "height": 0,
        }, store.Conflict{})
        return err
    }),
}
```

每条链的全部初始化回调与该链的初始检查点在同一笔业务事务中提交。只订阅事件的链也会记录自己的初始检查点。某条链的初始化失败时，运行器仍会尝试其余链，然后返回错误。用原存储与 `BuildID`、新的 `OpenID` 重启，仅重试尚未提交的链。所有链完成初始化后才开始投递区块和事件。[三链初始化与重启测试](../historical/isolated_setup_test.go)

### 双库 Isolated 实时追块

来源配置使用 `"ordering": "experimental_isolated"`，业务表继续用 `chain_id` 作为主键的一部分。在上一节的 `historical.RuntimeOptions` 上启用轮询，再打开运行器。需要一直运行时省略来源的 `endBlock`。各链均设置了结束高度时，运行器可追到该高度后退出。

```go
opts.Live = &historical.RuntimeLiveOptions{PollingInterval: time.Second}
runner, err := historical.OpenRuntimePostgres(ctx, opts)
if err != nil {
    return err
}
defer runner.Close(context.Background())
_, err = runner.Run(ctx)
return err
```

PGlite 可使用 `historical.OpenRuntimePGlite`。双库 Isolated 实时部署的存储格式为 `managed-live-isolated-continuous-v1`。首次启用时，请选择新的业务和同步命名空间或两个新的数据目录。重启同一实时部署时，沿用其存储位置和 `BuildID`，并分配新的 `OpenID`。

运行中若 A 链分叉，回滚和重放只涉及 A 链的孤块。B 链可以独立推进安全检查点。整个进程重启时，各链从自己的安全检查点恢复，其后的未确认回调可能再次执行。将需要随区块回滚的状态写入回调的业务事务。遇到可重试的取块错误时，双库运行器会重建所有链的读取队列。若业务要求一条链长期断线时其他链仍持续推进，普通入口 `runtime.NewIndexer` 的 Isolated 模式已有该场景的测试。[分叉与重启测试](../historical/isolated_live_test.go)

### 为同一条链配置多个 RPC 节点

在 Go 配置中将同一条链的 RPC 地址放入 `config.Chain.RPC`。下面的 `firstURL` 和 `secondURL` 指向链 ID 均为 101、创世区块相同的两个节点。

```go
chain := config.Chain{
    Name: "A",
    ID:   101,
    RPC:  []string{firstURL, secondURL},
}
```

原生客户端会在这些地址之间分配请求，遇到临时错误会重试。若一条链的两个节点分别返回 HTTP 503 和 429，`/ready` 返回 503，已提交的业务数据和进度保留。若恢复的节点返回重组窗口内的新分支，索引器会找到共同祖先，撤销旧分支发现的工厂子地址，再按新分支处理日志、合约 CALL、交易和转账。

- 使用 Isolated 时，重组只影响发生分叉的链。其他链保留已提交的数据和进度，已处理的回调不会重复触发。
- 使用默认多链或 Omnichain 时，运行器会重新执行受影响的跨链回调。例如链 B 的日志处理函数读取了链 A 的数值，链 A 分叉后，链 B 的处理函数会在重放时读到更新后的数值。

完成重放后可以重启应用，沿用相同配置和数据库即可续接已提交的进度。原节点同步到新分支后也可以重新接手。[混合来源与双后端验证](../parity/runs/native-mixed-source-reorg/RESULT.md) · [原节点恢复测试](../parity/runs/native-rpc-pool-reorg/RESULT.md)

## 按全局顺序索引多条链

要让多条链的回调共同维护一张表，例如所有链的总余额，创建存储和运行器时都使用 `schema.Omnichain`。沿用独立多链示例中的 `cfg.Chains`、事件来源和处理函数，即可把运行器交给同一个 HTTP 应用。

```go
db, err := store.New(ctx, backend, model, namespace, schema.Omnichain)
if err != nil {
    return err
}
cfg.Ordering = schema.Omnichain
worker, err := runtime.NewIndexer(cfg, db, handlers)
if err != nil {
    return err
}
```

运行器按区块时间、链 ID、区块高度和区块内事件位置排序。只有各参与链的进度足以确定顺序，才会执行对应回调。某条链的 RPC 暂时不可用时，各链等待它恢复；网络较慢的链也会限制整体进度。所有来源都设置 `EndBlock` 的链抵达最终确认高度后，会释放对其他链排序进度的限制。所有链都完成后，查询服务继续提供数据。

每次回调的数据库写入与事件检查点一起提交。重组会从共同祖先撤销共享状态中受影响的写入，再按规范链重放。超出已确认边界的重组会停止运行，需要使用新命名空间重新索引。需要关闭 RPC 缓存时，使用 `runtime.NewIndexerWithOptions(cfg, db, handlers, runtime.Options{DisableCache: true})`。[跨链排序和恢复实测](../parity/runs/native-ordered-indexing/RESULT.md)

## 使用本地 PGlite

准备好 Go、C 编译器、Node.js 和 npm。以下命令在新目录中生成索引器。先按上文设置 `PONDER_LOCAL_RPC_URL` 和 `PONDER_LOCAL_CONTRACT`，指向链 ID 为 1 的本地 Anvil 及其 ERC-20 合约。

```sh
ponder init local-indexer
cd local-indexer
go mod tidy
npm install
env -u PONDER_LOCAL_DATABASE_URL -u DATABASE_PRIVATE_URL -u DATABASE_URL -u PONDER_PGLITE_DIR \
  ponder start --schema ponder_go_token
```

生成的 `package.json` 固定了 PGlite 0.2.13。Go 进程在同一应用中提供索引和 HTTP 查询。数据默认写入项目的 `.ponder/pglite`。重启时使用同一目录和 schema 即可恢复已提交进度。开发时改用 `ponder dev --schema ponder_go_token`，CLI 会显示实际使用的开发 schema。应用运行期间可按前文的 `/status` 和 `/sql/db` 示例查询。

生成项目优先使用 `PONDER_LOCAL_DATABASE_URL` 指定的 PostgreSQL，其次使用 `PONDER_PGLITE_DIR` 指定的目录。两者均未设置时，依次检查 `DATABASE_PRIVATE_URL`、`DATABASE_URL`，最后使用默认 PGlite 目录。相对路径从 Go 项目目录解析。需要稳定的视图名称时，`start --views-schema` 会在首次追上链头后发布视图。独立的 `ponder serve` 使用 PostgreSQL。[上游独立查询范围](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/bin/commands/serve.ts#L102-L109)

按 Ctrl+C 停止索引器后，在安装驱动的项目目录中管理本地部署。将下面的 schema 名替换为你的实际部署名称。

```sh
ponder db list --pglite-dir .ponder/pglite
ponder db create-views --pglite-dir .ponder/pglite --schema ponder_go_token --views-schema token_live
ponder db prune --pglite-dir .ponder/pglite --schema ponder_go_old
```

`--pglite-dir` 读取已经由本版 Go 后端打开过的 PGlite 目录。`prune` 会删除指定部署的业务数据和配对的同步数据；正在发布的部署返回 `published`。目录正由 Go 引擎占用时，管理命令会报错。路径不存在时会报错且保留原状。[索引、重开与目录管理实测](../parity/runs/native-local-pglite/RESULT.md)

## 发布查询视图与清理部署

以下命令使用 PostgreSQL。先在索引项目中用当前版本打开部署，让框架登记业务表和配对的同步 schema。下面用 `ponder_go_blue`、`ponder_go_green` 表示两次部署，`indexer_api` 是查询方固定使用的专用 schema。

```sh
ponder db list --database-url "$PONDER_LOCAL_DATABASE_URL"
ponder db create-views --database-url "$PONDER_LOCAL_DATABASE_URL" --schema ponder_go_blue --views-schema indexer_api
```

查询方可以从 `indexer_api` 读取业务表、已声明的视图以及 `_ponder_checkpoint`。等新部署追上进度，再运行同一条命令把 `--schema` 改为 `ponder_go_green`，即可在一个事务中切换所有查询视图。外部查询依赖旧列类型时，数据库会拒绝不兼容的切换，原视图和数据继续可用。发布目标应使用新的专用 schema，或此前由该命令或生命周期管理器创建的视图 schema。

也可以在启动新部署时指定同一个稳定视图命名空间，让 Go 项目在首次追上链头后自动切换。

```sh
./bin/ponder start --root ./examples/erc20 --schema ponder_go_green --views-schema indexer_api --port 42071
```

新部署回填期间，`indexer_api` 继续返回旧部署的数据，新进程的 `/ready` 返回 503。追上链头并提交视图切换事务后，`/ready` 返回 200。监听稳定视图的 SQL/SSE 查询会重新读取已提交的结果，已有数据可以立即展示。若数据库拒绝切换，新进程返回错误，原视图仍指向旧部署。同一进程只自动发布一次，短暂落后后再次追平会保留此后手动指定的部署。自行编写 Go 入口时，可在调用 `runtime.LaunchApplication` 前用 `runtime.PublishViewsOnReady` 包装索引器。[实测记录](../parity/runs/native-ready-view-publication/RESULT.md)

停掉旧索引器、关闭其数据库连接，然后清理旧部署。该操作会删除登记的业务数据、撤销日志、checkpoint 和配对的同步数据。

```sh
ponder db prune --database-url "$PONDER_LOCAL_DATABASE_URL" --schema ponder_go_blue
```

返回的 JSON 数组逐项给出 schema 和处理结果。`pruned` 表示已清理，`active` 表示仍有索引器连接或有效租约，`busy` 表示另一个操作正在使用该部署。`published` 表示仍在为查询视图提供数据，`unregistered` 表示缺少当前部署登记，`absent` 表示已不存在。外部依赖或其他未登记对象会让该部署的清理事务回滚。数据库级的独立 RPC 缓存会保留。

省略 `--schema` 会检查连接所指数据库中的所有候选部署，每个部署单独提交。命令遇错时会输出已经处理的结果；再次运行可以继续。使用独立双库运行器的部署有自己的存储所有权和恢复流程。

## PostgreSQL 写入与部署管理账号

原生 Go 项目可以由两个 PostgreSQL 登录账号运行。部署管理账号创建并拥有业务、同步和缓存 schema，负责发布视图和清理旧部署。写入账号重开已准备好的部署，处理回调、提交 checkpoint 和撤销重组。独立查询服务还可以使用单独的只读账号。[只读查询验证](../parity/runs/native-standalone-serve/RESULT.md)

下面以**新的专用数据库** `indexer` 为例，登录角色 `deploy_manager` 和 `index_writer` 已由管理员创建。两者都使用普通登录权限。先连接 `indexer`，由数据库管理员设置该库的初始权限。

```sql
REVOKE ALL ON DATABASE indexer FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT CONNECT, CREATE ON DATABASE indexer TO deploy_manager;
GRANT CONNECT ON DATABASE indexer TO index_writer;
```

1. 用 `deploy_manager` 的连接设置 `PONDER_LOCAL_DATABASE_URL`，运行 `ponder start --schema ponder_go_live`。设置 RPC 和合约地址的方法见前文。首次启动会创建业务对象、通知触发器和原生 RPC 缓存。在 `/ready` 返回 200 后停止进程。自定义 Go 入口也可调用 `store.New`、`EnableQueryNotifications` 和 `EnableProtocolCache` 完成准备。
2. 以 `deploy_manager` 登录，为新部署执行下方链接里的 SQL，例如 `psql "$MANAGER_URL" -X -v ON_ERROR_STOP=1 -f docs/examples/postgres-writer.sql`。示例适用于 ERC-20 项目的 `account` 表。使用其他部署名称或业务表时，替换 SQL 中的名称，并给回调实际读写的业务表授予对应权限。默认多链模式在回放时还会写入 `multichain_replay` 元数据；脚本用 PostgreSQL 行级安全策略将元数据写入限于这一行。[部署写入授权 SQL](examples/postgres-writer.sql)
3. 使用下列命令切换到写入账号。`PONDER_DATABASE_MODE=existing` 让 `ponder init` 生成的 Go 入口校验并打开指定部署。首次创建仍使用默认的 `create` 模式。自定义 Go 入口可直接调用 `store.OpenExisting`。

```sh
PONDER_LOCAL_DATABASE_URL="$WRITER_URL" PONDER_DATABASE_MODE=existing \
  ponder start --schema ponder_go_live
```

视图发布由 `deploy_manager` 运行 `ponder db create-views`，写入进程使用普通的 `start`。部署和首次发布所需的 schema 均已创建后，管理员可执行 `REVOKE CREATE ON DATABASE indexer FROM deploy_manager`。该账号仍能把已有视图切换到已准备好的下一部署，或清理已停用、未发布的旧部署。要创建新的部署或新的视图 schema，再授予数据库级 `CREATE`。

测试使用独立登录账号和真实 Anvil 交易，覆盖 ERC-20 回调、重启后的重组、跨链回放标记、发布切换和定向清理。使用独立业务库与同步库的 `historical.RuntimeRunner` 有自己的对象与启动流程。[受限账号验证](../parity/runs/native-deployment-roles/RESULT.md)

## 独立业务库与同步库的中断恢复

使用 `historical.OpenRuntimePostgres` 或 `historical.OpenRuntimePGlite` 的默认多链持续索引器发生中断时，保留原业务库、同步库和 `BuildID`。PostgreSQL 恢复原数据库实例后使用原命名空间，PGlite 重新打开原来的两个目录。为新运行实例设置新的 `OpenID`，调用 `Run`，并用 `Ready` 检查是否已完成重放。业务库已经提交重组回滚、而同步库尚未确认时，运行器会核对并完成这次操作。[恢复测试](../parity/runs/native-coordinated-engine-recovery/RESULT.md)

尚未最终确认的区块可能再次触发回调。需要随重组撤销的业务状态应通过回调的数据库事务写入。重复投递到外部系统的处理由应用负责。

## 常见问题

| 现象 | 检查方法 |
| --- | --- |
| 启动时要求设置 `PONDER_LOCAL_…` | 索引时需要 RPC URL 和合约地址。使用 PostgreSQL 时再提供数据库 URL；本地 PGlite 需要先安装其驱动。独立 `serve` 使用 PostgreSQL URL 和已有部署。 |
| SQL 返回空数组 | 对照 `deploy.json` 里的 `deployedTo`、当前 Anvil 地址和 `/status`。确认索引已读到部署区块，且请求的是启动日志中的 HTTP 地址。 |
| 打开已有命名空间失败 | 检查是否复用了不同 schema 定义、旧存储格式或由其他应用创建的命名空间。保留原库，在新的命名空间重新索引。 |

## 复现三链 WETH 历史对账

`feature-multichain` 模拟应用用地址作为 `account` 表的主键，同一地址在不同链上的 WETH 存款会累加。固定的三段历史数据随仓库提供。将 `PONDER_TEST_POSTGRES` 指向专用测试库，并按本指南配置 PGlite 后，在仓库根目录运行以下命令。Go 的回放使用本地归档，不需要公网 RPC。

```sh
go test -tags simulation -count=1 -timeout=1h -v -run '^TestSimulationFeatureMultichain$' ./runtime
```

测试在 PostgreSQL 和 PGlite 上分别创建业务表，检查三条链交付的事件数、全局顺序和全部地址的余额。参考表由固定上游版本的 WETH 回调生成。种子、归档来源、复算参考表和更新历史数据的命令见[验收记录](../parity/runs/native-seeded-multichain/RESULT.md)。
