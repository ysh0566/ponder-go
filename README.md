# Ponder-Go

Ponder-Go 把 Ethereum 链上事件交给 Go 处理函数，将索引结果写入数据库，并通过 HTTP 提供查询。它移植自 Ponder 0.18.2，固定上游提交为 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`。[上游源码](https://github.com/ponder-sh/ponder/tree/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334)

**可以用 Go 编写单链索引器，也可以让多条链独立运行，或按全局顺序写入同一份业务数据。** 第一次使用可以从本地 ERC-20 示例开始。它会从 Anvil 读取一条 `Transfer` 日志，在 PostgreSQL 中记录两个地址的余额变动，并让你通过 SQL 核对结果。[本地使用指南](docs/USAGE.md) · [独立多链示例](docs/USAGE.md#从同一个-go-项目索引多条链)

## 快速开始

在仓库根目录使用 Go 1.25 或以上版本、启用 CGO 并准备好 C 编译器，然后构建 CLI。

```sh
go build -o bin/ponder ./cmd/ponder
./bin/ponder --version
```

CLI 版本为 `0.18.2-go.0`。运行 `ponder init my-indexer` 创建独立 Go 索引器，进入项目执行 `go mod tidy` 后即可构建。也可直接运行仓库内的 `examples/erc20`。[示例源代码](examples/erc20/main.go) · [从零启动并查询示例](docs/USAGE.md)

## 功能状态

| 功能 | 可以使用的范围 |
| --- | --- |
| 链上索引 | 单链支持历史同步、持续索引、重组撤销和重新索引。单链和独立多链可用 WebSocket 新区块通知追块，并按重组窗口清理撤销日志。可处理静态合约及 factory 发现的日志；账户、交易和合约调用 trace 的支持范围见矩阵。[运行模式](docs/RUNTIME_SUPPORT.md) |
| 多链 | 默认多链运行器已验证部分有限区间和持续索引组合。原生 Omnichain 支持持续追块、历史回填和跨链重组重放；已验证区块、静态和工厂合约日志、账户交易与转账、合约调用 trace 的组合。Isolated 模式可让各链独立索引、重试和撤销重组。[多链边界](docs/RUNTIME_SUPPORT.md) |
| 回调查链 | 类型化 Go 回调提供上游列出的 21 个高层方法，包括历史合约调用、gas 估算、ABI 模拟、交易及 ENS 地址、文本和头像查询。EIP-3668 支持网关取数、合约验证和 ENS 批量查询。原生脚手架的普通回调还可直接发送 JSON-RPC 请求。[方法清单](docs/RUNTIME_SUPPORT.md#callback-rpc-methods) · [链下读取](docs/USAGE.md#合约的-eip-3668-链下读取) |
| 数据库 | PostgreSQL 和 PGlite 均有验证。业务写入、撤销日志和 checkpoint 在事务中提交。各存储组件只支持自己的当前格式；旧库需使用新命名空间重新索引。[存储格式](parity/runs/current-database-formats/RESULT.md) |
| 查询 | 支持只读 SQL、SSE 订阅、状态与指标接口。Go 项目可用 `serve` 独立查询正在写入的 PostgreSQL 部署，只需数据库连接和部署命名空间。应用可另行挂载 GraphQL 处理器，支持实体、过滤、分页、关联及视图。网络地址、时间、间隔和几何列均可用于查询。[列类型与表示](docs/USAGE.md#列类型与-graphql-表示) · [HTTP 查询示例](docs/USAGE.md#查询索引结果) · [GraphQL](graphql) |
| CLI 与应用 | `dev` 可热重载，`start` 和 `serve` 可启动索引或查询服务，均支持 `--hostname` 和 `--port`。全局 `--log-level` 和 `--log-format json` 同时控制 CLI 和生成的 Go 应用的诊断日志。生成的 PostgreSQL 项目通过 `start --views-schema` 在追上链头后切换查询视图。另有代码生成和 checkpoint 编解码。PostgreSQL 部署可用 `db list` 列出命名空间、`db create-views` 发布查询视图、`db prune` 清理已停用的部署。[数据库命令](docs/USAGE.md#发布查询视图与清理部署) · [日志选项](docs/USAGE.md#控制-cli-和-go-应用日志) · [CLI 使用](docs/USAGE.md#用于自己的项目) |

## 原生 Go 索引器还缺什么

| 使用场景 | 当前边界 |
| --- | --- |
| 在同一 Go 应用中协调多条链 | 原生 `runtime.NewIndexer` 支持独立多链和按全局顺序索引。默认协调模式仍需使用 `historical.RuntimeRunner`。还需验证更多工厂日志、交易、转账和函数调用 trace 的重叠组合，以及多家 RPC 同时中断时的恢复。[全局排序使用方式](docs/USAGE.md#按全局顺序索引多条链) |
| 管理部署和日志 | PostgreSQL 已支持视图发布和部署清理。PGlite 仍缺独立进程管理数据目录、与写入进程共享目录提供查询的支持。热重载和全局日志选项已可用，开发终端 UI 仍待实现。 |
| 生产故障验收 | 已在写事务提交前强制终止 PostgreSQL 和 PGlite 引擎，验证重启后的余额、检查点、重放和撤销。全局排序的纯日志历史任务也覆盖了 PGlite 应用异常退出。仍需验证主机掉电、协调多链运行中数据库引擎崩溃，以及生产数据库角色与权限。[故障恢复实测](parity/runs/native-engine-recovery/RESULT.md) |

这些条目按使用 Go 编写索引器的目标维护。数据库范围为当前格式。[各运行模式的具体限制](docs/RUNTIME_SUPPORT.md)

## 当前验证与计数

固定上游共有 **905 条测试记录**。其中 **883 条已移植为 Go 用例并执行，866 条通过，17 条服务商 RPC 用例失败**。另有 5 条保留的 SDK 用例通过。17 条失败用例依赖特定网关的响应或真实链的历史日志，自建节点的实测结果已逐项记录。其余为上游跳过 10 条、TODO 2 条，以及已退出 Go 支持范围的旧缓存迁移 5 条。[逐例台账](parity/test-ledger.json) · [RPC 实测结果](parity/runs/provider-policy-assertions/RESULT.md)

在已归档的全模块回归基线上，Go 普通和竞态运行各覆盖 30 个包，各通过 4,877 个叶子用例。此后新增功能的验证结果列在各功能的测试报告中。两种离线运行各跳过了上述 17 条在线用例，它们另行执行后仍失败。上游原版全量复验为 876 通过、17 失败、10 跳过、2 TODO。[Go 全模块回归](parity/runs/complete-module-regression/RESULT.md) · [固定上游复验](parity/runs/current-release-verification/RESULT.md)

## 开发与验证资料

- 配置链和合约、生成 Go 类型并编写回调。[使用指南](docs/USAGE.md#用于自己的项目) · [来源配置](docs/SOURCE_CONFIGURATION.md) · [类型化配置](docs/TYPED_SOURCE_CONFIGURATION.md)
- 使用索引数据库、恢复索引或了解同步缓存。[数据库生命周期](docs/DATABASE_LIFECYCLE.md) · [数据库操作](docs/DATABASE_ACTIONS.md) · [同步缓存](docs/SYNC_MIGRATIONS.md)
- 对接浏览器和 React，或复现服务商相关测试。[浏览器 SDK](sdk/typescript/browser/README.md) · [React SDK](sdk/typescript/react/README.md) · [历史日志 RPC](docs/HISTORICAL_LOG_RPC.md)
- 查阅里程碑、历史命令和原始测试证据。历史执行文件可按归档说明恢复。[里程碑](docs/MILESTONES.md) · [此前的开发记录](https://github.com/ysh0566/ponder-go/blob/cc0e1ef16a2f1fd31035e30c05c1aefb91de6ba3/README.md) · [验证资料与归档](parity/README.md)

## 来源与许可

项目保留上游 MIT 许可证和各 SDK 的许可证。Go 依赖许可列在清单中。本地 `third_party/pg_query_go` 基于 v5.1.0，保留其原始许可证。[LICENSE](LICENSE) · [依赖许可证清单](licenses/manifest.json) · [SQL 解析器源码](third_party/pg_query_go)
