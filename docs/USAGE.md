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

在终端 B 按 Ctrl+C 停止 `start`，等待命令退出。保留数据库、链、合约地址和三个环境变量，再用相同的 `--schema` 执行以下命令。

```sh
./bin/ponder db list --database-url "$PONDER_LOCAL_DATABASE_URL"
./bin/ponder serve --root ./examples/erc20 --schema ponder_go_erc20_demo
```

`db list` 会列出示例的业务和同步命名空间。`serve` 会重新打开同一数据库，只提供查询。用终端 C 的 SQL 再次查询应得到相同的两行。要恢复跟链，停止 `serve` 并重新运行前面的 `start` 命令。如果换了链、要从头索引或现有库格式已过期，请选择新的 `ponder_go_…` 命名空间。[数据库格式与重开约定](../parity/runs/current-database-formats/RESULT.md)

完成后，在终端 B 停止查询服务，在终端 A 停止 Anvil。若要丢弃本次教程数据，运行 `docker stop ponder-erc20-guide`，并删除终端 B 中 `$demo_dir` 指向的临时目录。

## 开发时自动重载

在原生 Go 项目目录中启动开发模式。它会监视 Go 源码、模块文件、JSON 配置和 SQL 文件，开发服务器会打印实际监听地址和数据所在的 schema。

```sh
ponder dev --hostname 127.0.0.1 --port 42069 --schema ponder_go_demo
```

保存修改且编译成功后，CLI 会停止上一个进程并启动新的索引器。编译失败时，之前成功启动的进程继续运行，下一次编辑会重新构建。开发 schema 的名字由前缀和项目内容的摘要生成，修改处理函数或表定义会选择新的命名空间并重新索引。按 Ctrl+C 退出。

`--hostname` 和 `--port` 也可用于 `start` 和 `serve`。端口 0 让操作系统分配空闲端口。使用 `runtime.LaunchApplication` 的 Go 应用会读取这两个选项。自行创建 HTTP 服务的应用可从 `PONDER_HOSTNAME`、`PONDER_PORT` 读取对应值。

## 用于自己的项目

在自己的工作目录中安装 CLI、创建索引器并构建。首次使用需要能访问 Go 模块代理或 GitHub。

```sh
go install github.com/ysh0566/ponder-go/cmd/ponder@main
"$(go env GOPATH)/bin/ponder" init my-indexer
cd my-indexer
go mod tidy
go build .
```

示例使用 PostgreSQL。选择 PGlite 的独立应用需在项目目录安装 `@electric-sql/pglite@0.2.13` 并提供 Node.js，Go 程序已内置适配器脚本。

如果设置了 `GOBIN`，CLI 位于该目录。要使用自己的仓库地址，可在项目中执行 `go mod edit -module 你的模块路径`。生成的项目包含自己的 `go.mod`、Go 事件处理函数和表定义。沿用上文的数据库、RPC 和合约地址，在项目目录执行 `ponder start --schema ponder_go_my_indexer`，其中 `ponder` 取安装后的实际路径。

- **从 Go 示例开始。** 在生成的项目中修改 `schema.json` 定义表和主键，在 `main.go` 配置链 ID、RPC、合约地址、ABI 和起始区块。示例从区块 0 开始，使用已有合约时可将 `config.Contract.StartBlock` 设为部署高度。[配置](SOURCE_CONFIGURATION.md) · [示例入口](../examples/erc20/main.go)
- **编写处理函数。** 示例在 `Token:Transfer` 中解码参数，通过事件上下文的 `c.DB` 更新两行。将写入放在回调事务中，以便索引器一起提交业务数据和 checkpoint，并在重组时撤销。[存储操作](DATABASE_ACTIONS.md)
- **生成模型。** 使用 `codegen --schema … --typed` 生成类型化模型。对自己的应用，将 `--out` 指向目标 Go 包目录，并用 `--package` 指定包名。`codegen --abi` 可生成事件和函数类型，`--app-config` 可生成回调名称与链类型。[类型化配置](TYPED_SOURCE_CONFIGURATION.md)
- **提供查询。** 示例用 `query.New` 创建 SQL 服务，通过 `runtime.LaunchApplication` 启动索引和 HTTP 监听，在查询模式下传入 `nil`。需要 GraphQL 时可用 `graphql.New` 创建处理器，通过 `http.ServeMux` 挂到 `/graphql` 并把 mux 赋给 `query.Server.API`。示例默认提供上表中的接口。[示例入口](../examples/erc20/main.go) · [GraphQL 实现](../graphql)

脚手架的 `runtime.Handler` 通过 `c.Client.Call` 发 JSON-RPC 请求。使用生成的回调注册表时，客户端类型是 `*indexing.Client`，可调用 `GetBalance`、`ReadContract` 等方法，也可用 `Request` 传入 RPC 方法和参数。使用原始 RPC 读取历史状态时，请显式传入事件的区块号。使用 `Multicall` 批量读合约时，请在 `MulticallOptions.MulticallAddress` 指定目标链上已部署的 Multicall3 合约地址。[回调客户端方法](RUNTIME_SUPPORT.md#callback-rpc-methods)

在仓库根目录查看为 `account` 表生成的 Go 模型。

```sh
model_dir=$(mktemp -d)
./bin/ponder codegen --schema examples/erc20/schema.json --typed --out "$model_dir/model.go"
head "$model_dir/model.go"
```

生成的模型用 `[]byte` 表示二进制，`json.RawMessage` 表示 JSON，`[2]float64` 和 `[3]float64` 表示点和线。SQL 数组使用 `[]orm.Nullable[T]`，可通过 `orm.Some(value)` 和 `orm.Null[T]()` 分别写入值和空元素。生成的 ABI 回调保留 tuple 的原始组件名，两个组件转换成相同 Go 名字时会得到不同的字段名。

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

已有的默认多链或全局排序配置使用 `historical.OpenRuntimePostgres` 或 `historical.OpenRuntimePGlite` 创建协调运行器，也可以将该运行器作为 `worker` 传给同一个 `runtime.LaunchApplication`。这些模式允许的来源和区间见[运行模式矩阵](RUNTIME_SUPPORT.md)。

## 发布查询视图与清理部署

以下命令使用 PostgreSQL。先在索引项目中用当前版本打开部署，让框架登记业务表和配对的同步 schema。下面用 `ponder_go_blue`、`ponder_go_green` 表示两次部署，`indexer_api` 是查询方固定使用的专用 schema。

```sh
ponder db list --database-url "$PONDER_LOCAL_DATABASE_URL"
ponder db create-views --database-url "$PONDER_LOCAL_DATABASE_URL" --schema ponder_go_blue --views-schema indexer_api
```

查询方可以从 `indexer_api` 读取业务表、已声明的视图以及 `_ponder_checkpoint`。等新部署追上进度，再运行同一条命令把 `--schema` 改为 `ponder_go_green`，即可在一个事务中切换所有查询视图。外部查询依赖旧列类型时，数据库会拒绝不兼容的切换，原视图和数据继续可用。发布目标应使用新的专用 schema，或此前由该命令或生命周期管理器创建的视图 schema。

`start --views-schema` 会把视图 schema 名称通过 `DATABASE_VIEWS_SCHEMA` 传给 Go 子进程。当前脚手架完成历史同步后仍需手动运行 `db create-views` 才会切换查询方看到的数据。

停掉旧索引器、关闭其数据库连接，然后清理旧部署。该操作会删除登记的业务数据、撤销日志、checkpoint 和配对的同步数据。

```sh
ponder db prune --database-url "$PONDER_LOCAL_DATABASE_URL" --schema ponder_go_blue
```

返回的 JSON 数组逐项给出 schema 和处理结果。`pruned` 表示已清理，`active` 表示仍有索引器连接或有效租约，`busy` 表示另一个操作正在使用该部署。`published` 表示仍在为查询视图提供数据，`unregistered` 表示缺少当前部署登记，`absent` 表示已不存在。外部依赖或其他未登记对象会让该部署的清理事务回滚。数据库级的独立 RPC 缓存会保留。

省略 `--schema` 会检查连接所指数据库中的所有候选部署，每个部署单独提交。命令遇错时会输出已经处理的结果；再次运行可以继续。使用独立双库运行器的部署有自己的存储所有权和恢复流程。

## 常见问题

| 现象 | 检查方法 |
| --- | --- |
| 启动时要求设置 `PONDER_LOCAL_…` | 在启动 CLI 的同一个终端导出数据库 URL、RPC URL 和合约地址。示例的 `serve` 入口也会读取这三个变量。 |
| SQL 返回空数组 | 对照 `deploy.json` 里的 `deployedTo`、当前 Anvil 地址和 `/status`。确认索引已读到部署区块，且请求的是启动日志中的 HTTP 地址。 |
| 打开已有命名空间失败 | 检查是否复用了不同 schema 定义、旧存储格式或由其他应用创建的命名空间。保留原库，在新的命名空间重新索引。 |
