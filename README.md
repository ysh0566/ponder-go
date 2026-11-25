# Ponder-Go

Ponder-Go 把 Ethereum 链上事件交给 Go 处理函数，将索引结果写入数据库，并通过 HTTP 提供查询。它移植自 Ponder 0.18.2，固定上游提交为 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`。[上游源码](https://github.com/ponder-sh/ponder/tree/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334)

**可以用 Go 编写单链索引器，也可以让多条链独立运行，或按全局顺序写入同一份业务数据。** 第一次使用可以从本地 ERC-20 示例开始。它会从 Anvil 读取一条 `Transfer` 日志，在 PostgreSQL 中记录两个地址的余额变动，并让你通过 SQL 核对结果。[本地使用指南](docs/USAGE.md) · [独立多链示例](docs/USAGE.md#从同一个-go-项目索引多条链)

## 快速开始

在仓库根目录使用 Go 1.25 或以上版本、启用 CGO 并准备好 C 编译器，然后构建 CLI。

```sh
go build -o bin/ponder ./cmd/ponder
./bin/ponder --version
```

CLI 版本为 `0.18.2-go.0`。运行 `ponder init my-indexer` 创建独立 Go 索引器，进入项目执行 `go mod tidy` 后即可构建。本地使用 PGlite 时在生成目录执行 `npm install` 安装固定版本驱动。也可直接运行仓库内的 `examples/erc20`。[示例源代码](examples/erc20/main.go) · [从零启动并查询示例](docs/USAGE.md)

## 功能状态

| 功能 | 可以使用的范围 |
| --- | --- |
| 链上索引 | 单链支持历史同步、持续索引、重组撤销和重新索引。单链和独立多链可用 WebSocket 新区块通知追块，并按重组窗口清理撤销日志。可处理静态合约及 factory 发现的日志；账户、交易和合约调用 trace 的支持范围见矩阵。[运行模式](docs/RUNTIME_SUPPORT.md) |
| 多链 | 默认多链可通过 `runtime.NewIndexer` 处理有限区间或持续追块。已编译的 Go 来源配置可以直接交给 `runtime.NewIndexerFromSource`，一次启动有注册回调的所有链。默认排序下，较快的链可先提交回调，链重组时会重放受影响的跨链回调。原生 Omnichain 支持持续追块、历史回填和跨链重组重放；已验证区块、静态和工厂合约日志、账户交易与转账、合约调用 trace 的组合。Isolated 模式可让各链独立索引、重试和撤销重组。[编译配置用法](docs/USAGE.md#用编译后的-go-来源配置启动多链) · [多链边界](docs/RUNTIME_SUPPORT.md) |
| 回调查链 | 类型化 Go 回调提供上游列出的 21 个高层方法，包括历史合约调用、gas 估算、ABI 模拟、交易及 ENS 地址、文本和头像查询。EIP-3668 支持网关取数、合约验证和 ENS 批量查询。原生脚手架的普通回调还可直接发送 JSON-RPC 请求。[方法清单](docs/RUNTIME_SUPPORT.md#callback-rpc-methods) · [链下读取](docs/USAGE.md#合约的-eip-3668-链下读取) |
| 数据库 | 生成项目默认将 PGlite 数据存到 `.ponder/pglite`，设置数据库 URL 后使用 PostgreSQL。业务写入、撤销日志和 checkpoint 在事务中提交。各存储组件只支持自己的当前格式；旧库需使用新命名空间重新索引。[本地 PGlite 使用与管理](docs/USAGE.md#使用本地-pglite) · [存储格式](parity/runs/current-database-formats/RESULT.md) |
| 查询 | 支持只读 SQL、SSE 订阅、状态与指标接口。Go 项目可用 `serve` 独立查询正在写入的 PostgreSQL 部署，只需数据库连接和部署命名空间。应用可另行挂载 GraphQL 处理器，支持实体、过滤、分页、关联及视图。网络地址、时间、间隔和几何列均可用于查询。[列类型与表示](docs/USAGE.md#列类型与-graphql-表示) · [HTTP 查询示例](docs/USAGE.md#查询索引结果) · [GraphQL](graphql) |
| CLI 与应用 | `dev` 支持热重载，交互终端显示每链 RPC 速率、回填进度和 ETA，以及各回调的执行次数和平均耗时。`start` 和 `serve` 可启动索引或查询服务，均支持 `--hostname` 和 `--port`。全局 `--log-level` 和 `--log-format json` 同时控制 CLI 和生成的 Go 应用的诊断日志。生成的项目通过 `start --views-schema` 在追上链头后切换查询视图。另有代码生成和 checkpoint 编解码。PostgreSQL 和已停机的本地 PGlite 部署可用 `db list` 列出命名空间、`db create-views` 发布查询视图、`db prune` 清理已停用的部署。[数据库命令](docs/USAGE.md#发布查询视图与清理部署) · [日志选项](docs/USAGE.md#控制-cli-和-go-应用日志) · [CLI 使用](docs/USAGE.md#用于自己的项目) |

## 原生 Go 索引器还缺什么

| 使用场景 | 当前能用什么 | 还缺什么 |
| --- | --- | --- |
| 在同一个 Go 入口协调多链 | `runtime.NewIndexer` 支持默认、Isolated 和 Omnichain 三种排序。默认模式已验证无写入回调的跨链重放、空块重组、两条链的 RPC 同时中断后分批恢复和冷启动。 使用独立业务库和同步库的 `historical.RuntimeRunner` 在默认排序下支持有限区间的 setup 回调和逐链配置 RPC 缓存。开启 `SourceCheckpoints` 后可从区块 0 开始索引。[有限区间用法](docs/USAGE.md#独立存储的有限区间索引) | 独立存储的 Omnichain 仅支持已最终确认的有限区间，要求开启缓存，支持特定的静态日志和区块回调组合。实时追块、工厂事件、账户交易与转账、函数调用 trace 和 setup 仍未支持。[选用默认模式](docs/USAGE.md#使用默认多链排序) · [双后端验证](parity/runs/native-default-multichain/RESULT.md) |
| 在 RPC 故障或链重组时保持索引一致 | PostgreSQL 和 PGlite 均已验证同一条日志命中三个 Go 回调、同一笔 CALL 命中两个函数选择器相同的回调，以及回调失败后的重启和链重组重放。默认多链和 Omnichain 在同链节点切换、分支改变后均会重放受影响的跨链回调。 | 特定商业网关的限流与归档响应仍有 17 条上游断言未通过。[节点切换测试](parity/runs/native-rpc-pool-reorg/RESULT.md) · [回调重放测试](parity/runs/native-callback-overlap/RESULT.md) |
| 验收生产部署 | 已验证只读查询、受限写入账号处理事件与重组、部署管理账号发布和清理原生部署，也验证了单库写事务中强杀数据库引擎。独立双库默认多链的恢复测试覆盖重组意图写入、业务回滚提交、同步确认三个时点。[PostgreSQL 账号配置](docs/USAGE.md#postgresql-写入与部署管理账号) | 主机掉电、原生多链运行器和独立双库 Omnichain 运行中强杀数据库引擎、其他 PGlite 运行模式的异常退出，以及独立双库的最小数据库权限仍待验收。[双库恢复测试](parity/runs/native-coordinated-engine-recovery/RESULT.md) · [读角色测试](parity/runs/native-standalone-serve/RESULT.md) |

该清单以开发者用 Go 编写新索引器、使用当前数据库格式为范围。表中列出独立存储的功能边界，以及上线前仍待验收的故障和环境。表中的独立存储指 `historical.RuntimeRunner` 分开管理业务数据与同步数据。PostgreSQL 使用同一个数据库 URL、不同命名空间和连接池。PGlite 使用两个独立目录和引擎进程。[存储构造](store/independent_storage.go) · [运行模式矩阵](docs/RUNTIME_SUPPORT.md)

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
