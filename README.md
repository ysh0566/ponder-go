# Ponder-Go

Ponder-Go 把 Ethereum 链上事件交给 Go 处理函数，将索引结果写入数据库，并通过 HTTP 提供查询。它移植自 Ponder 0.18.2，固定上游提交为 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`。[上游源码](https://github.com/ponder-sh/ponder/tree/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334)

**可以用 Go 编写单链索引器，也可以让多条链独立运行，或按全局顺序写入同一份业务数据。** 第一次使用可以从本地 ERC-20 示例开始。它会从 Anvil 读取一条 `Transfer` 日志，在 PostgreSQL 中记录两个地址的余额变动，并让你通过 SQL 核对结果。[本地使用指南](docs/USAGE.md) · [独立多链示例](docs/USAGE.md#从同一个-go-项目索引多条链)

## 快速开始

在仓库根目录使用 Go 1.25 或以上版本、启用 CGO 并准备好 C 编译器，然后构建 CLI。

```sh
go build -o bin/ponder ./cmd/ponder
./bin/ponder --version
```

CLI 版本为 `0.18.2-go.0`。运行 `ponder init my-indexer` 创建 ERC-20 索引器，或用 `ponder init --template erc721 my-nfts` 选择其他 Go 模板。进入项目执行 `go mod tidy` 后即可构建。本地使用 PGlite 时在生成目录执行 `npm install` 安装固定版本驱动。也可直接运行仓库内的 `examples/erc20`。[示例源代码](examples/erc20/main.go) · [从零启动并查询示例](docs/USAGE.md)

## 功能状态

| 功能 | 可以使用的范围 |
| --- | --- |
| 链上索引 | 单链支持历史同步、持续索引、重组撤销和重新索引。单链和独立多链可用 WebSocket 新区块通知追块，并按重组窗口清理撤销日志。可处理静态合约及 factory 发现的日志；账户、交易和合约调用 trace 的支持范围见矩阵。[运行模式](docs/RUNTIME_SUPPORT.md) |
| 多链 | 默认多链可通过 `runtime.NewIndexer` 处理有限区间或持续追块。已编译的 Go 来源配置可以直接交给 `runtime.NewIndexerFromSource`，一次启动有注册回调的所有链。可按链名传入自定义 Go RPC 函数，其他链仍可使用节点 URL。[自定义传输](docs/USAGE.md#为单条链配置-go-rpc-传输) 默认排序下，较快的链可先提交回调，链重组时会重放受影响的跨链回调。原生 Omnichain 支持持续追块、历史回填和跨链重组重放；已验证区块、静态和工厂合约日志、账户交易与转账、合约调用 trace 的组合。Isolated 模式可让各链独立索引、重试和撤销重组。[编译配置用法](docs/USAGE.md#用编译后的-go-来源配置启动多链) · [多链边界](docs/RUNTIME_SUPPORT.md) |
| 回调查链 | 类型化 Go 回调提供上游列出的 21 个高层方法，包括历史合约调用、gas 估算、ABI 模拟、交易及 ENS 地址、文本和头像查询。EIP-3668 支持网关取数、合约验证和 ENS 批量查询。原生脚手架的普通回调还可直接发送 JSON-RPC 请求。[方法清单](docs/RUNTIME_SUPPORT.md#callback-rpc-methods) · [链下读取](docs/USAGE.md#合约的-eip-3668-链下读取) |
| 数据库 | 生成项目默认将 PGlite 数据存到 `.ponder/pglite`，设置数据库 URL 后使用 PostgreSQL。业务写入、撤销日志和 checkpoint 在事务中提交。各存储组件只支持自己的当前格式；旧库需使用新命名空间重新索引。[本地 PGlite 使用与管理](docs/USAGE.md#使用本地-pglite) · [存储格式](parity/runs/current-database-formats/RESULT.md) |
| 查询 | 支持只读 SQL、SSE 订阅、状态与指标接口。Go 项目可用 `serve` 独立查询正在写入的 PostgreSQL 部署，只需数据库连接和部署命名空间。应用可另行挂载 GraphQL 处理器，支持实体、过滤、分页、关联及视图。网络地址、时间、间隔和几何列均可用于查询。[列类型与表示](docs/USAGE.md#列类型与-graphql-表示) · [HTTP 查询示例](docs/USAGE.md#查询索引结果) · [GraphQL](graphql) |
| CLI 与应用 | `dev` 支持热重载，交互终端显示每链 RPC 速率、回填进度和 ETA，以及各回调的执行次数和平均耗时。`start` 和 `serve` 可启动索引或查询服务，均支持 `--hostname` 和 `--port`。全局 `--log-level` 和 `--log-format json` 同时控制 CLI 和生成的 Go 应用的诊断日志。生成的项目通过 `start --views-schema` 在追上链头后切换查询视图。另有代码生成和 checkpoint 编解码。PostgreSQL 和已停机的本地 PGlite 部署可用 `db list` 列出命名空间、`db create-views` 发布查询视图、`db prune` 清理已停用的部署。[数据库命令](docs/USAGE.md#发布查询视图与清理部署) · [日志选项](docs/USAGE.md#控制-cli-和-go-应用日志) · [CLI 使用](docs/USAGE.md#用于自己的项目) |

## 原生 Go 索引器还缺什么

新建 Go 项目可以用 `runtime.NewIndexer` 实时索引多条链。这个入口已支持默认、Isolated 和 Omnichain 三种排序。剩余的验证工作主要是多种事件来源遇到节点切换、数据库中断和跨链重组时的表现。[运行模式矩阵](docs/RUNTIME_SUPPORT.md)

如果需要分开管理业务数据与同步缓存，可以使用双库运行器 `historical.RuntimeRunner`。仍待实现的功能包括按 Isolated 模式让各链独立推进和回滚，以及在 Omnichain 模式下持续追块并恢复跨链重组。PostgreSQL 的双库使用同一数据库下的两个命名空间和连接池，PGlite 使用两个目录和引擎进程。[存储构造](store/independent_storage.go) · [双库使用方法](docs/USAGE.md#独立存储的有限区间索引)

| 使用场景 | 当前能用什么 | 还缺什么 |
| --- | --- | --- |
| 从现成模板创建 Go 索引器 | `ponder init` 默认生成 ERC-20 项目。用 `empty` 从空白项目开始，或用 `blocks` 每五个区块记录一次 Chainlink 报价。用 `read-contract` 在事件高度读取文件，用 `call-traces` 按调用者统计 Multicall3。用 `filter` 按事件主题筛选 Swap，用 `api-functions` 提供转账计数和排名接口。另有 `erc721`、`erc1155`、`erc4626`、`factory`、`accounts` 和 `proxy`。[选择模板与启动](docs/USAGE.md#选择-go-项目模板) | 上游菜单还有三项尚无专用 Go 模板：多链合约、Friendtech 和 Uniswap V3 闪电贷。多链索引已有 Go API 和仓库示例。[上游模板目录](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/create-ponder/src/index.ts#L60-L133) |
| 用 Go 在同一个进程索引多链 | `runtime.NewIndexer` 支持默认、Isolated 和 Omnichain 三种排序。Omnichain 可持续追块并在重组时重放跨链回调。 | 多链节点切换和数据库中断的组合测试主要使用区块回调。跨链重组中同时处理工厂日志、交易和转账的故障覆盖仍需扩展。Isolated 模式也需补测多链组合使用合约、账户、工厂及 trace 的场景。[运行模式矩阵](docs/RUNTIME_SUPPORT.md) |
| 双库默认多链与 Isolated | 默认多链支持实时追块、链重组、从创世区块开始、工厂来源和按链配置 RPC 缓存。可在各链达到结束高度后退出，也可用 `ponder start` 持续运行，通过 HTTP 查询已提交的进度与业务数据。停止后可以重启补块。PostgreSQL 部署也可通过 `ponder serve` 启动独立的只读 SQL/SSE 服务。`ponder dev` 支持修改 Go 处理函数后按修订重新索引，编译失败时仍可查询原进程。[热重载用法](docs/USAGE.md#双库-go-项目的热重载) · [独立查询进程](docs/USAGE.md#双库-postgresql-的独立查询进程) · [双库 CLI 示例](docs/USAGE.md#用-cli-启动独立存储的多链索引器) · [有限区间用法](docs/USAGE.md#独立存储的有限区间索引) | 双库运行器还不接受 Isolated 排序。[示例入口](examples/managed-multichain/main.go) |
| 双库 Omnichain，使用账户、CALL 或逐块回调 | 支持已最终确认的有限区间。各链可独立开关缓存，处理静态或工厂发现的合约日志与 CALL、账户交易与转账。账户交易和转账可以单独订阅。[关闭缓存的账户回填](parity/runs/native-omnichain-uncached-accounts/RESULT.md)其他链可以只监听日志，也可以使用逐块回调。所有链的 `setup` 在首个事件前一起提交。[账户回填与重启](parity/runs/native-omnichain-sparse-accounts/RESULT.md) · [混合回调用法](docs/USAGE.md#在不同链上使用不同的回调) · [工厂发现与恢复](parity/runs/native-omnichain-factory/RESULT.md) · [初始化与重启](parity/runs/native-omnichain-setup/RESULT.md) | 还缺实时追块与跨链重组恢复。处理合约 CALL 的链仍需要一个覆盖来源区间的逐块回调。[配置校验](historical/live_runtime_omnichain_callbacks.go) |
| 双库 Omnichain，所有链只监听日志 | 支持已最终确认的静态合约日志和工厂子合约日志，各来源的区间可重叠或不相交。可逐链关闭 RPC 缓存。即使区间中间或末尾没有可解码的事件，也能核对扫描进度、就绪并重启。[工厂发现与重启](parity/runs/native-omnichain-log-factory/RESULT.md) · [逐来源区间](parity/runs/native-omnichain-log-ranges/RESULT.md) · [关闭缓存与恢复](parity/runs/native-omnichain-uncached-logs/RESULT.md) | 实时追块与跨链重组仍待实现。 |
| 在 RPC 故障或链重组时保持索引一致 | PostgreSQL 和 PGlite 均已验证同一条日志命中三个 Go 回调、同一笔 CALL 命中两个函数选择器相同的回调，以及回调失败后的重启和链重组重放。默认多链和 Omnichain 在同链节点切换、分支改变后均会重放受影响的跨链回调。 | 仍有 17 条依赖特定网关或历史链数据的上游 RPC 用例未通过。[节点切换测试](parity/runs/native-rpc-pool-reorg/RESULT.md) · [回调重放测试](parity/runs/native-callback-overlap/RESULT.md) |
| 验收生产部署 | 已验证只读查询、受限写入账号处理事件与重组、部署管理账号发布和清理原生部署，也验证了单库写事务中强杀数据库引擎。原生默认多链和 Omnichain 还验证了在替换分支的回调中强杀数据库后重启。独立双库默认多链的恢复测试覆盖重组意图写入、业务回滚提交、同步确认三个时点。独立双库 Omnichain 的已确认区间也覆盖 Go 进程在区块或静态日志回调内、以及运行器就绪后的异常退出。在静态日志回调写入业务数据后，还测试了 PostgreSQL 服务以及 PGlite 业务或同步引擎退出时的恢复。[双库引擎重启](parity/runs/native-omnichain-engine-recovery/RESULT.md) · [双库应用重启](parity/runs/native-omnichain-app-recovery/RESULT.md) · [原生多链恢复](parity/runs/native-multichain-engine-recovery/RESULT.md) · [PostgreSQL 账号配置](docs/USAGE.md#postgresql-写入与部署管理账号) | 主机掉电、双库 Omnichain 初始化或发布就绪时的引擎中断、其他 PGlite 运行模式的异常退出，以及独立双库的写入与部署管理账号最小权限仍待验收。[双库恢复测试](parity/runs/native-coordinated-engine-recovery/RESULT.md) · [读角色测试](parity/runs/native-standalone-serve/RESULT.md) |

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
