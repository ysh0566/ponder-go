# 原生 Go 索引器使用指南

这份教程使用仓库里的 ERC-20 示例、一个临时 PostgreSQL 数据库和一条本地 Anvil 链。你将部署一个只在构造时发出标准 `Transfer` 事件的演示合约，启动索引器，再通过 HTTP 查到这笔事件产生的两条余额记录。[索引器](../examples/erc20/main.go) · [表定义](../examples/erc20/schema.json)

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

- **从 Go 示例开始。** 在生成的项目中修改 `schema.json` 定义表和主键，在 `main.go` 配置链 ID、RPC、合约地址、ABI 和起始区块。示例从区块 0 开始，使用已有合约时可将 `config.Contract.StartBlock` 设为部署高度。[配置](SOURCE_CONFIGURATION.md) · [示例入口](../examples/erc20/main.go)
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

## 常见问题

| 现象 | 检查方法 |
| --- | --- |
| 启动时要求设置 `PONDER_LOCAL_…` | 索引时需要 RPC URL 和合约地址。使用 PostgreSQL 时再提供数据库 URL；本地 PGlite 需要先安装其驱动。独立 `serve` 使用 PostgreSQL URL 和已有部署。 |
| SQL 返回空数组 | 对照 `deploy.json` 里的 `deployedTo`、当前 Anvil 地址和 `/status`。确认索引已读到部署区块，且请求的是启动日志中的 HTTP 地址。 |
| 打开已有命名空间失败 | 检查是否复用了不同 schema 定义、旧存储格式或由其他应用创建的命名空间。保留原库，在新的命名空间重新索引。 |
