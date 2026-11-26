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

用 Go 新写索引器，普通入口 `runtime.NewIndexer` 已支持默认多链、Isolated 和 Omnichain 的历史回填、持续追块和重组恢复。使用编译后的 Go 来源配置时可选 `runtime.NewIndexerFromSource`。两个入口都使用 `runtime.Handler`。[运行模式矩阵](docs/RUNTIME_SUPPORT.md) · [选择运行入口](docs/USAGE.md#选择-go-索引器的运行入口)

业务数据和同步缓存分开管理时，`historical.RuntimeRunner` 已支持默认多链和 Isolated 持续追块。Isolated 遇到分叉时仅撤销该链的未确认写入。进程重启时，各链从自己的安全检查点继续。[双库 Isolated 用法](docs/USAGE.md#双库-isolated-实时追块) · [分叉与重启验证](parity/runs/native-isolated-live/RESULT.md)

**双库 Omnichain 实时索引仍待实现。** 固定区间的作业可以提前启动，等每条链的结束区块和初始化区块都达到全局最终确认位置后再回填。默认在打开时检查一次，Go 应用也可配置轮询间隔。[提前启动的用法](docs/USAGE.md#等待双库-omnichain-的区间最终确认) 回填结束时，还需要把尚未最终确认的事件交给实时运行器。持续追块时需要按全局检查点排列各链事件。一条链分叉时，需要协调两个库撤销受影响的写入，并按顺序重放依赖这些写入的其他链回调。[最终性边界](historical/live_runtime_omnichain_source_test.go)

| 使用场景 | 当前能用什么 | 还缺什么 |
| --- | --- | --- |
| 从现成模板创建 Go 索引器 | 上游创建菜单的 16 个示例都有 Go 模板。默认 `erc20` 适合本地入门。`reference-erc20` 提供主网固定区间的余额、授权历史及账户转账关系。另有 NFT、金库、工厂、多链存款、Friendtech、Uniswap V3 闪电贷等模板。参考模板提供示例的 GraphQL 入口。[选择模板与启动](docs/USAGE.md#选择-go-项目模板) | 主网模板已在自建节点上重现事件并验证查询。实际公链历史回放仍需相应网络的归档数据。[模板验证](parity/runs/native-reference-erc20/RESULT.md) |
| 用 Go 在同一个进程索引多链 | `runtime.NewIndexer` 支持默认、Isolated 和 Omnichain 三种排序。工厂发现、合约 CALL、日志、交易和转账都已有节点切换与分叉测试。Isolated 保留另一条链已提交的回调，默认多链和 Omnichain 会重新执行受影响的跨链回调。已验证在替换分支的合约 CALL 回调中中断数据库并恢复。两种全局排序还覆盖了重放另一条链的日志回调时中断。[数据库中断与恢复](parity/runs/native-mixed-engine-recovery/RESULT.md) · [混合来源重组与重启](parity/runs/native-mixed-source-reorg/RESULT.md) | 主机掉电和双库运行器的混合来源中断仍待验证。[运行模式矩阵](docs/RUNTIME_SUPPORT.md) |
| 双库默认多链 | 默认多链支持实时追块、链重组、从创世区块开始、工厂来源和按链配置 RPC 缓存。可在各链达到结束高度后退出，也可用 `ponder start` 持续运行，通过 HTTP 查询已提交的进度与业务数据。停止后可以重启补块。PostgreSQL 部署也可通过 `ponder serve` 启动独立的只读 SQL/SSE 服务。`ponder dev` 支持修改 Go 处理函数后按修订重新索引，编译失败时仍可查询原进程。[热重载用法](docs/USAGE.md#双库-go-项目的热重载) · [独立查询进程](docs/USAGE.md#双库-postgresql-的独立查询进程) · [双库 CLI 示例](docs/USAGE.md#用-cli-启动独立存储的多链索引器) · [有限区间用法](docs/USAGE.md#独立存储的有限区间索引) | 更多来源组合和 RPC 故障场景仍待验证。[运行模式矩阵](docs/RUNTIME_SUPPORT.md) |
| 双库 Isolated | 可按链初始化、回填和持续追块。运行中一条链分叉，另一条链已经提交的回调和进度保留。重启会重放各链安全检查点之后的区块。[实时追块与重启](docs/USAGE.md#双库-isolated-实时追块) | 混合来源分叉时的数据库引擎中断和长时间 RPC 故障仍待验收。[双链分叉测试](historical/isolated_live_test.go) |
| 双库 Omnichain，使用账户、CALL 或区块回调 | 支持已最终确认的有限区间。各链可独立开关缓存，处理静态或工厂发现的合约日志与 CALL、账户交易与转账。账户交易、转账和合约 CALL 均可单独订阅。合约 CALL 支持静态地址、工厂子地址及逐链开关缓存。[合约 CALL 回填与重启](parity/runs/native-omnichain-sparse-contract-calls/RESULT.md) [关闭缓存的账户回填](parity/runs/native-omnichain-uncached-accounts/RESULT.md)其他链可以只监听日志，也可以注册区块回调。每条链支持不同的采样间隔和区间，同块命中的多个区块回调一同提交。[按间隔回填](parity/runs/native-omnichain-block-intervals/RESULT.md) 所有链的 `setup` 在首个事件前一起提交。[账户回填与重启](parity/runs/native-omnichain-sparse-accounts/RESULT.md) · [混合回调用法](docs/USAGE.md#在不同链上使用不同的回调) · [工厂发现与恢复](parity/runs/native-omnichain-factory/RESULT.md) · [初始化与重启](parity/runs/native-omnichain-setup/RESULT.md) | 还缺实时追块与跨链重组恢复。[配置校验](historical/live_runtime_omnichain_callbacks.go) |
| 双库 Omnichain，所有链只监听日志 | 支持已最终确认的静态合约日志和工厂子合约日志，各来源的区间可重叠或不相交。可逐链关闭 RPC 缓存。即使区间中间或末尾没有可解码的事件，也能核对扫描进度、就绪并重启。[工厂发现与重启](parity/runs/native-omnichain-log-factory/RESULT.md) · [逐来源区间](parity/runs/native-omnichain-log-ranges/RESULT.md) · [关闭缓存与恢复](parity/runs/native-omnichain-uncached-logs/RESULT.md) | 实时追块与跨链重组仍待实现。 |
| 在 RPC 故障或链重组时保持索引一致 | PostgreSQL 和 PGlite 均已验证同一条日志命中三个 Go 回调、同一笔 CALL 命中两个函数选择器相同的回调，以及回调失败后的重启和链重组重放。默认多链和 Omnichain 在同链节点切换、分支改变后均会重放受影响的跨链回调。 | 仍有 17 条依赖特定网关或历史链数据的上游 RPC 用例未通过。[节点切换测试](parity/runs/native-rpc-pool-reorg/RESULT.md) · [回调重放测试](parity/runs/native-callback-overlap/RESULT.md) |
| 验收生产部署 | 已验证只读查询、受限写入账号处理事件与重组、部署管理账号发布和清理原生部署，也验证了单库写事务中强杀数据库引擎。原生默认多链和 Omnichain 还验证了在替换分支的回调中强杀数据库后重启。独立双库默认多链的恢复测试覆盖重组意图写入、业务回滚提交、同步确认三个时点。独立双库 Omnichain 的已确认区间也覆盖 Go 进程在区块或静态日志回调内、以及运行器就绪后的异常退出。在静态日志回调写入业务数据后，还测试了 PostgreSQL 服务以及 PGlite 业务或同步引擎退出时的恢复。[双库引擎重启](parity/runs/native-omnichain-engine-recovery/RESULT.md) · [双库应用重启](parity/runs/native-omnichain-app-recovery/RESULT.md) · [原生多链恢复](parity/runs/native-multichain-engine-recovery/RESULT.md) · [PostgreSQL 账号配置](docs/USAGE.md#postgresql-写入与部署管理账号) | 主机掉电、双库 Omnichain 初始化或发布就绪时的引擎中断、其他 PGlite 运行模式的异常退出，以及独立双库的写入与部署管理账号最小权限仍待验收。[双库恢复测试](parity/runs/native-coordinated-engine-recovery/RESULT.md) · [读角色测试](parity/runs/native-standalone-serve/RESULT.md) |

还有两处会影响 Go 项目的具体写法。

- 双库 Isolated 和 Omnichain 的有限区间支持 `OnCommitted`，可在事件组与安全检查点提交后读取进度。Omnichain 回填还可以调整分页大小 `QuerySize` 和调度时钟 `Clock`。三种排序都支持用 `MaxQueryParameters` 控制批量写入。双库默认多链和实时模式的组提交通知，以及历史重试等待 `RetryWait` 仍待实现。[接收回填提交通知](docs/USAGE.md#接收双库回填的提交通知) · [设置批量写入大小](docs/USAGE.md#独立存储的有限区间索引) · [配置校验](historical/live_runtime.go)
- Go 模型生成器把数据库枚举和 `hex` 列生成为 `string`。枚举取值的编译期约束和自定义列编解码器的生成仍待实现。JSON、字节串和数组已有生成及数据库读写测试。[生成器](orm/generate.go) · [模型测试](orm/container_models_test.go)

## 当前验证与计数

ERC-20 转账和工厂创建交易对这两个上游端到端场景均已移植。Go 回调处理自建 Anvil 上的真实合约事件，测试通过 HTTP 核对 PostgreSQL 和 PGlite 上的结果。[端到端测试](runtime/application_source_test.go)

还有两项独立的验收工作。上游 8 个模拟应用需要用可复现的随机种子运行 Go 索引器，并将结果与预期数据逐项比较。性能方面，尚缺 rETH 和 Uniswap 在相同数据、数据库和 RPC 条件下的回填吞吐量对比。[核查记录](parity/runs/native-go-indexer-reaudit/RESULT.md) · [验证台账](parity/feature-ledger.json)

固定上游共有 **905 条测试记录**。其中 **883 条已移植为 Go 用例并执行，866 条通过，17 条服务商 RPC 用例失败**。17 条失败中，16 条断言服务商对区块范围或响应大小的限制，1 条要求查到 Swell 指定区间的 58 条历史日志。另有 5 条保留的 SDK 用例通过。自建节点的实测结果已逐项记录。其余为上游跳过 10 条、TODO 2 条，以及已退出 Go 支持范围的旧缓存迁移 5 条。[逐例台账](parity/test-ledger.json) · [RPC 实测结果](parity/runs/provider-policy-assertions/RESULT.md)

在已归档的全模块回归基线上，Go 普通和竞态运行各覆盖 30 个包，各通过 4,877 个叶子用例。此后新增功能的验证结果列在各功能的测试报告中。两种离线运行各跳过了上述 17 条在线用例，它们另行执行后仍失败。上游原版全量复验为 876 通过、17 失败、10 跳过、2 TODO。[Go 全模块回归](parity/runs/complete-module-regression/RESULT.md) · [固定上游复验](parity/runs/current-release-verification/RESULT.md)

## 开发与验证资料

- 配置链和合约、生成 Go 类型并编写回调。[使用指南](docs/USAGE.md#用于自己的项目) · [来源配置](docs/SOURCE_CONFIGURATION.md) · [类型化配置](docs/TYPED_SOURCE_CONFIGURATION.md)
- 使用索引数据库、恢复索引或了解同步缓存。[数据库生命周期](docs/DATABASE_LIFECYCLE.md) · [数据库操作](docs/DATABASE_ACTIONS.md) · [同步缓存](docs/SYNC_MIGRATIONS.md)
- 对接浏览器和 React，或复现服务商相关测试。[浏览器 SDK](sdk/typescript/browser/README.md) · [React SDK](sdk/typescript/react/README.md) · [历史日志 RPC](docs/HISTORICAL_LOG_RPC.md)
- 查阅里程碑、历史命令和原始测试证据。历史执行文件可按归档说明恢复。[里程碑](docs/MILESTONES.md) · [此前的开发记录](https://github.com/ysh0566/ponder-go/blob/cc0e1ef16a2f1fd31035e30c05c1aefb91de6ba3/README.md) · [验证资料与归档](parity/README.md)

## 来源与许可

项目保留上游 MIT 许可证和各 SDK 的许可证。Go 依赖许可列在清单中。本地 `third_party/pg_query_go` 基于 v5.1.0，保留其原始许可证。[LICENSE](LICENSE) · [依赖许可证清单](licenses/manifest.json) · [SQL 解析器源码](third_party/pg_query_go)
