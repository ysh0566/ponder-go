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
| 数据库 | 生成项目默认将 PGlite 数据存到 `.ponder/pglite`，设置数据库 URL 后使用 PostgreSQL。业务写入、撤销日志和 checkpoint 在事务中提交。各存储组件只支持自己的当前格式；旧库需使用新命名空间重新索引。自定义 Go 类型可以通过列编解码器用于主键、普通列和数组。回调中可使用生成的列组合 AND、OR 条件，对单表数据筛选、排序和分页，也可以对双表内连接和左连接结果筛选、排序和分页，支持复合键和自连接。可继续追加表，用已联接的任一侧匹配新表。[单表筛选与分页](docs/USAGE.md#在-go-回调中过滤和分页) · [查询地址的转入和转出](docs/USAGE.md#查询地址转入或转出的记录) · [跨表查询](docs/USAGE.md#在-go-回调中连接两张表) · [按发送方余额分页](docs/USAGE.md#按发送方余额分页查询转账) · [三表授权查询](docs/USAGE.md#查询授权事件的所有者和当前额度) · [自定义类型读写](docs/USAGE.md#为数据库列绑定自定义-go-类型) · [本地 PGlite 使用与管理](docs/USAGE.md#使用本地-pglite) · [存储格式](parity/runs/current-database-formats/RESULT.md) |
| 查询 | 支持只读 SQL、SSE 订阅、状态与指标接口。Go 项目可用 `serve` 独立查询正在写入的 PostgreSQL 部署，只需数据库连接和部署命名空间。应用可另行挂载 GraphQL 处理器，支持实体、过滤、分页、关联及视图。网络地址、时间、间隔和几何列均可用于查询。[列类型与表示](docs/USAGE.md#列类型与-graphql-表示) · [HTTP 查询示例](docs/USAGE.md#查询索引结果) · [GraphQL](graphql) |
| CLI 与应用 | `dev` 支持热重载，交互终端显示每链 RPC 速率、回填进度和 ETA，以及各回调的执行次数和平均耗时。`start` 和 `serve` 可启动索引或查询服务，均支持 `--hostname` 和 `--port`。全局 `--log-level` 和 `--log-format json` 同时控制 CLI 和生成的 Go 应用的诊断日志。生成的项目通过 `start --views-schema` 在追上链头后切换查询视图。另有代码生成和 checkpoint 编解码。PostgreSQL 和已停机的本地 PGlite 部署可用 `db list` 列出命名空间、`db create-views` 发布查询视图、`db prune` 清理已停用的部署。[数据库命令](docs/USAGE.md#发布查询视图与清理部署) · [日志选项](docs/USAGE.md#控制-cli-和-go-应用日志) · [CLI 使用](docs/USAGE.md#用于自己的项目) |

## 原生 Go 索引器还缺什么

用 Go 新写索引器，普通入口 `runtime.NewIndexer` 已支持默认多链、Isolated 和 Omnichain 的历史回填、持续追块和重组恢复。使用编译后的 Go 来源配置时可选 `runtime.NewIndexerFromSource`。两个入口都使用 `runtime.Handler`。[运行模式矩阵](docs/RUNTIME_SUPPORT.md) · [选择运行入口](docs/USAGE.md#选择-go-索引器的运行入口)

如果项目使用单库，上述三种排序都可以从回填一直运行到实时。把业务数据和同步缓存分开部署时，主要功能缺口是 Omnichain 的持续索引。

**双库 Omnichain 从回填切换到实时。** 双库作业可以等各链的有限区间分别最终确认后回填。即使两条链的结束块时间不同，作业也会按全局顺序提交较晚区块的回调，失败后可从已提交位置恢复。[结束时间不同的双库作业](docs/USAGE.md#等待双库-omnichain-的区间最终确认) 剩下的是按全局顺序持续写入新区块，在一条链分叉时协调两库撤销和重放受影响的跨链回调，并支持在此期间中断后恢复。需要立即上线的项目可使用单库 Omnichain。自行管理持久化的应用也可消费 Go 实时事件流。[双库最终性检查](historical/live_runtime_omnichain_source_test.go) · [单库 Omnichain](docs/USAGE.md#按全局顺序索引多条链) · [Go 事件流](docs/USAGE.md#读取-omnichain-历史和实时事件)
双库 Isolated 已支持部分链离线时首次启动。固定或省略区块边界并启用持续索引后，健康链可以先提交，离线链首次恢复时会核验节点、运行本链的初始化并从配置的起点追块。仅执行已确认区间回填的作业仍需各参与链在线。[节点离线时首启与恢复](docs/USAGE.md#双库-isolated-实时追块)

PGlite 的查询、写入、开启事务和订阅通知在等待连接时支持 Go `context` 取消。同一事务内的排队语句也可取消。已经发给引擎的 SQL 仍需等待数据库返回。监控方面，Go 写入进程还缺 8 个 Prometheus 指标族。[PGlite 使用说明](docs/USAGE.md#使用本地-pglite) · [指标明细](docs/RUNTIME_SUPPORT.md#prometheus-coverage)

还有四类验证工作。

- **故障恢复**。复验主机掉电、双库在混合来源分叉期间中断，以及更多 PGlite 异常退出场景。[已覆盖的恢复场景](docs/RUNTIME_SUPPORT.md)
- **应用结果**。8 个参考应用中，三链 WETH 和主网 ERC-20 已完成固定区间对账，其余 6 个仍需比较 Go 处理函数写出的各张业务表。[应用清单](docs/RUNTIME_SUPPORT.md#application-verification)
- **性能**。rETH 和 Uniswap 的回填仍需在相同链数据、数据库和 RPC 条件下测量。[性能基准范围](docs/RUNTIME_SUPPORT.md#application-verification)
- **节点行为**。17 条在线 RPC 用例在自建节点上仍失败。其中 16 条检查特定服务商的请求范围或响应大小限制，另 1 条需要 Swell 指定区间的 58 条历史日志。[逐项实测](parity/runs/provider-policy-assertions/RESULT.md)

| 使用场景 | 当前能用什么 | 还缺什么 |
| --- | --- | --- |
| 从现成模板创建 Go 索引器 | 上游创建菜单的 16 个示例都有 Go 模板。默认 `erc20` 适合本地入门。`reference-erc20` 提供主网固定区间的余额、授权历史及账户转账关系。另有 NFT、金库、工厂、多链存款、Friendtech、Uniswap V3 闪电贷等模板。参考模板提供示例的 GraphQL 入口。[选择模板与启动](docs/USAGE.md#选择-go-项目模板) | 部分主网模板仍需用真实链历史逐表对账。已有固定区间结果的是三链 WETH 和主网 ERC-20。[应用对账范围](docs/RUNTIME_SUPPORT.md#application-verification) |
| 用 Go 在同一个进程索引多链 | `runtime.NewIndexer` 支持默认、Isolated 和 Omnichain 三种排序。工厂发现、合约 CALL、日志、交易和转账都已有节点切换与分叉测试。Isolated 保留另一条链已提交的回调，默认多链和 Omnichain 会重新执行受影响的跨链回调。已验证在替换分支的合约 CALL 回调中中断数据库并恢复。两种全局排序还覆盖了重放另一条链的日志回调时中断。[数据库中断与恢复](parity/runs/native-mixed-engine-recovery/RESULT.md) · [混合来源重组与重启](parity/runs/native-mixed-source-reorg/RESULT.md) | 主机掉电和双库运行器的混合来源中断仍待验证。[运行模式矩阵](docs/RUNTIME_SUPPORT.md) |
| 双库默认多链 | 默认多链支持实时追块、链重组、从创世区块开始、工厂来源和按链配置 RPC 缓存。可在各链达到结束高度后退出，也可用 `ponder start` 持续运行，通过 HTTP 查询已提交的进度与业务数据。停止后可以重启补块。PostgreSQL 部署也可通过 `ponder serve` 启动独立的只读 SQL/SSE 服务。`ponder dev` 支持修改 Go 处理函数后按修订重新索引，编译失败时仍可查询原进程。[热重载用法](docs/USAGE.md#双库-go-项目的热重载) · [独立查询进程](docs/USAGE.md#双库-postgresql-的独立查询进程) · [双库 CLI 示例](docs/USAGE.md#用-cli-启动独立存储的多链索引器) · [有限区间用法](docs/USAGE.md#独立存储的有限区间索引) 有限区间的作业在未确认区块分叉时也会自动撤销并重放跨链回调，退出前会核对各链的未确认结束区块。[分叉与重放验收](parity/runs/native-bounded-reorg/RESULT.md) | 更多来源组合和 RPC 故障场景仍待验证。[运行模式矩阵](docs/RUNTIME_SUPPORT.md) |
| 双库 Isolated | 可按链初始化、回填和持续追块。进入实时阶段后，即使一条链的 RPC 持续返回 503，健康链仍能提交新区块。故障链恢复时从自己的已提交位置核对分支。[RPC 离线与恢复](parity/runs/native-isolated-rpc-outage/RESULT.md) 运行中一条链分叉，另一条链已经提交的回调和进度保留。重启会重放各链安全检查点之后的区块。[实时追块与重启](docs/USAGE.md#双库-isolated-实时追块) | 混合来源分叉时中断数据库、只对部分 RPC 方法返回错误等场景仍待验收。[双库重试逻辑](historical/live_runtime_continuous.go) · [健康链独立推进测试](runtime/isolated_indexer_test.go) |
| 双库 Omnichain，使用账户、CALL 或区块回调 | 支持已最终确认的有限区间。各链可独立开关缓存，处理静态或工厂发现的合约日志与 CALL、账户交易与转账。账户交易、转账和合约 CALL 均可单独订阅。合约 CALL 支持静态地址、工厂子地址及逐链开关缓存。[合约 CALL 回填与重启](parity/runs/native-omnichain-sparse-contract-calls/RESULT.md) [关闭缓存的账户回填](parity/runs/native-omnichain-uncached-accounts/RESULT.md)其他链可以只监听日志，也可以注册区块回调。每条链支持不同的采样间隔和区间，同块命中的多个区块回调一同提交。[按间隔回填](parity/runs/native-omnichain-block-intervals/RESULT.md) 所有链的 `setup` 在首个事件前一起提交。[账户回填与重启](parity/runs/native-omnichain-sparse-accounts/RESULT.md) · [混合回调用法](docs/USAGE.md#在不同链上使用不同的回调) · [工厂发现与恢复](parity/runs/native-omnichain-factory/RESULT.md) · [初始化与重启](parity/runs/native-omnichain-setup/RESULT.md) | 还缺实时追块与跨链重组恢复。[配置校验](historical/live_runtime_omnichain_callbacks.go) |
| 双库 Omnichain，所有链只监听日志 | 支持已最终确认的静态合约日志和工厂子合约日志，各来源的区间可重叠或不相交。可逐链关闭 RPC 缓存。即使区间中间或末尾没有可解码的事件，也能核对扫描进度、就绪并重启。[工厂发现与重启](parity/runs/native-omnichain-log-factory/RESULT.md) · [逐来源区间](parity/runs/native-omnichain-log-ranges/RESULT.md) · [关闭缓存与恢复](parity/runs/native-omnichain-uncached-logs/RESULT.md) | 实时追块与跨链重组仍待实现。 |
| 在 RPC 故障或链重组时保持索引一致 | PostgreSQL 和 PGlite 均已验证同一条日志命中三个 Go 回调、同一笔 CALL 命中两个函数选择器相同的回调，以及回调失败后的重启和链重组重放。默认多链和 Omnichain 在同链节点切换、分支改变后均会重放受影响的跨链回调。 | 服务商对日志请求区间和响应大小的限制，以及 Swell 归档数据的完整性，仍待在具备相应策略和链历史的端点复验。[节点实测](parity/runs/provider-policy-assertions/RESULT.md) · [节点切换测试](parity/runs/native-rpc-pool-reorg/RESULT.md) · [回调重放测试](parity/runs/native-callback-overlap/RESULT.md) |
| 在分叉后复用 RPC 结果 | 普通 Go 索引器会保留共同祖先及更早高度的回调 RPC 结果，替换分支上的请求会重新读取。有限日志区间按结束高度判断，未参与回滚的链保留缓存。[用法与边界](docs/USAGE.md#在分叉后复用按高度读取的-rpc-结果) | 仅给出区块哈希、动态标签或无法确定高度的请求，在该链回滚时仍会清除。 |
| 用 Prometheus 监控索引器 | 写入进程和独立查询进程的 `/metrics` 提供 Go 版本、运行命令和数据库类型。写入进程还报告排序模式、按链和事件统计的回调耗时、历史回填每批等待取数与处理的累计耗时、各步骤耗时、按链和方法统计的 RPC 耗时与失败次数，以及每条链成功处理的分叉次数。写入进程可按链查看已处理的区块高度、区块时间和实时或完成状态。用 `ponder_indexing_timestamp` 可查看已提交事件的链上时间，Omnichain 各链共享同一个值。普通 Go 索引器和双库持续索引作业还会按链记录出块到接收、接收到提交的耗时。写入进程按链报告回填区块总数、可复用区块数、本次补齐区块数，以及开始与结束时间。[回填区块数](docs/USAGE.md#查看历史回填的区块数) [回填起止时间](docs/USAGE.md#查看历史回填的起止时间) 写入与查询进程都可查看已标记数据库操作的耗时、错误次数，以及 PostgreSQL 各连接池的 SQL 提交次数。[数据库指标](docs/USAGE.md#查看数据库操作) · [同步位置与状态](docs/USAGE.md#查看每条链的同步位置) · [查询进程信息](docs/USAGE.md#查看版本与运行配置) · [回调耗时](docs/USAGE.md#查看-go-回调耗时) · [实时区块延迟](docs/USAGE.md#查看实时区块延迟) · [回填指标](docs/USAGE.md#查看历史回填耗时) · [RPC 指标](docs/USAGE.md#查看-rpc-耗时与错误数) · [分叉指标](docs/USAGE.md#查看发生分叉的链) | 上游 39 个具名指标族中，Go 写入进程已注册 31 个同名指标族。仍缺历史同步链上时间 3 个、索引执行 5 个。[缺少的指标分组](docs/RUNTIME_SUPPORT.md#prometheus-coverage) |
| 验收生产部署 | 单库已验证受限账号写入、分叉回滚、发布与清理，以及写事务中断后的恢复。双库默认多链已验证在重组的三个提交时点恢复。双库 Omnichain 的已确认区间已验证初始化、运行回调、就绪后的应用退出，以及区块回填后就绪事务提交前的数据库中断。仅监听日志时，还覆盖写入首个区块头分页后和就绪事务提交前中断数据库。[日志扫描恢复](parity/runs/native-omnichain-scan-activation/RESULT.md) · [PostgreSQL 账号配置](docs/USAGE.md#postgresql-写入与部署管理账号) · [原生多链恢复](parity/runs/native-multichain-engine-recovery/RESULT.md) · [双库默认多链恢复](parity/runs/native-coordinated-engine-recovery/RESULT.md) · [双库 Omnichain 中断恢复](docs/USAGE.md#独立业务库与同步库的中断恢复) 双库 PostgreSQL 已验证写入账号在撤销数据库级建 schema 权限后完成回填、重启和默认多链重组，缓存对象由独立角色持有。[双库账号配置](docs/USAGE.md#双库-postgresql-的写入与管理权限) | 仍需验收主机掉电和其他 PGlite 运行模式的应用异常退出。 |

双库索引器还支持以下回调和回填选项。

- 双库 Go 索引器可通过 `RuntimeOptions.OnCommitted` 接收已提交事件组的进度，包括实时区块和分叉后的重放。通知带有事务编号、链、区块哈希和当前安全状态。默认多链和实时部署重启时可能再次处理尚未安全确认的组。[订阅提交通知](docs/USAGE.md#订阅双库索引器的事件组提交) · [回填、分叉与重启验证](parity/runs/native-runtime-commit-notifications/RESULT.md)
- 双库三种排序都可以用 `QuerySize` 调整历史事件 SQL 查询的初始行数，用 `MaxQueryParameters` 调整同步库批量写入大小。有限区间 Omnichain 还支持调度时钟 `Clock`。PostgreSQL 的初始化和回填事务遇到冲突时，可以用 `RetryWait` 调整等待。[回填选项](docs/USAGE.md#独立存储的有限区间索引) · [事务重试](docs/USAGE.md#双库-postgresql-回填的事务重试)

Go 来源配置支持带符号的区块采样间隔，例如从第 2 块开始、间隔为 `-3` 时，会依次触发第 2、5、8 块。直接构造 `config.BlockSource` 时可写 `Interval: config.BlockPeriod(-3)`。[区块采样与重启](docs/USAGE.md#配置双库-omnichain-的区块回调)

## 当前验证与计数

ERC-20 转账和工厂创建交易对这两个上游端到端场景均已移植。Go 回调处理自建 Anvil 上的真实合约事件，测试通过 HTTP 核对 PostgreSQL 和 PGlite 上的结果。[端到端测试](runtime/application_source_test.go)

8 个上游模拟应用中，已有两个完成固定历史区间对账。`feature-multichain` 回放三条链的 11,756 条 WETH 存款，核对 332 个地址的合并余额。`reference-erc20` 回放 Ethereum 上的 4,970 条 Adventure Gold 事件，校验余额、授权额度和两张事件历史表，共 6,925 行。该用例还按上游种子在 39 个高度首次读取区块时注入故障，验证重试后的数据。仍待逐表对账的 6 个应用是 `assessment`、`super-assessment`、`basepaint`、`project-uniswap-v3-flash`、`the-compact` 和 `uniswap-v4`。WETH 和 ERC-20 的实时追块、随机故障与重启组合也待验收。rETH 和 Uniswap 的同条件回填性能对比仍待完成。[三链余额对账](docs/USAGE.md#复现三链-weth-历史对账) · [ERC-20 四表对账](docs/USAGE.md#复现-erc-20-历史对账) · [应用清单](docs/RUNTIME_SUPPORT.md#application-verification)

固定上游共有 **905 条测试记录**。其中 **883 条已移植为 Go 用例并执行，866 条通过，17 条服务商 RPC 用例失败**。17 条失败中，16 条断言服务商对区块范围或响应大小的限制，1 条要求查到 Swell 指定区间的 58 条历史日志。另有 5 条保留的 SDK 用例通过。自建节点的实测结果已逐项记录。其余为上游跳过 10 条、TODO 2 条，以及已退出 Go 支持范围的旧缓存迁移 5 条。[逐例台账](parity/test-ledger.json) · [RPC 实测结果](parity/runs/provider-policy-assertions/RESULT.md)

在已归档的全模块回归基线上，Go 普通和竞态运行各覆盖 30 个包，各通过 4,877 个叶子用例。此后新增功能的验证结果列在各功能的测试报告中。两种离线运行各跳过了上述 17 条在线用例，它们另行执行后仍失败。上游原版全量复验为 876 通过、17 失败、10 跳过、2 TODO。[Go 全模块回归](parity/runs/complete-module-regression/RESULT.md) · [固定上游复验](parity/runs/current-release-verification/RESULT.md)

## 开发与验证资料

- 配置链和合约、生成 Go 类型并编写回调。[使用指南](docs/USAGE.md#用于自己的项目) · [来源配置](docs/SOURCE_CONFIGURATION.md) · [类型化配置](docs/TYPED_SOURCE_CONFIGURATION.md)
- 使用索引数据库、恢复索引或了解同步缓存。[数据库生命周期](docs/DATABASE_LIFECYCLE.md) · [数据库操作](docs/DATABASE_ACTIONS.md) · [同步缓存](docs/SYNC_MIGRATIONS.md)
- 对接浏览器和 React，或复现服务商相关测试。[浏览器 SDK](sdk/typescript/browser/README.md) · [React SDK](sdk/typescript/react/README.md) · [历史日志 RPC](docs/HISTORICAL_LOG_RPC.md)
- 查阅里程碑、历史命令和原始测试证据。历史执行文件可按归档说明恢复。[里程碑](docs/MILESTONES.md) · [此前的开发记录](https://github.com/ysh0566/ponder-go/blob/cc0e1ef16a2f1fd31035e30c05c1aefb91de6ba3/README.md) · [验证资料与归档](parity/README.md)

## 来源与许可

项目保留上游 MIT 许可证和各 SDK 的许可证。Go 依赖许可列在清单中。本地 `third_party/pg_query_go` 基于 v5.1.0，保留其原始许可证。[LICENSE](LICENSE) · [依赖许可证清单](licenses/manifest.json) · [SQL 解析器源码](third_party/pg_query_go)
