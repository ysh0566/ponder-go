# 原版历史范围获取的 Go 接口与验证

`runtime.NewSourceInMemoryHistoricalSync` 与 `NewSourceHistoricalSync` 分别实现原版内存流和持久化范围获取。它们消费真实来源过滤器、所需区间、factory 子地址及 RPC；持久化接口写入已有 `SourceSyncStore`，内存接口直接产出 `SourceEventData`，不打开数据库。

| 接口或组件 | 行为 |
| --- | --- |
| `SyncBlockData` | 合并按区块排序的异步流，在 yield 前请求下一项，最多预取 100 个尚未消费的区块。 |
| `SyncBlockRangeData` | 合并 factory 依赖、并发获取区块和日志，写入实际 blocks、transactions、receipts、logs、traces 表。 |
| `RequiredSourceIntervals` | 使用原版 fragment 规则恢复缺失区间，保留过滤器及 factory 的对象身份与依赖失效规则。 |
| `SourceHistoricalChildren` | 共享 factory ID 的分页与子地址，保留最早发现区块；业务区间为空时也完成 factory drain。 |
| `GetSourceBlockByNumber` | 标准化实际 RPC 区块并核对 transaction、trace、receipt 的区块一致性。 |
| `rpc.SourceHistoricalProvider` | 为五类历史 RPC 结果提供具体回调类型；同一个 provider 拥有分页学习与 receipt fallback 状态。 |
| `config.CompileSourceFilter` | 将公开历史同步输入编译为封闭的过滤器变体；必须先缩窄为 log filter 才能读取或修改地址。 |

内存分页使用 RPC 对象拥有的 `Actions`。持久化日志获取拥有独立的范围学习状态；固定原版在建议范围重试时使用 `end-start`，与内存分页的包含端点长度分别实现。两个同步器共享 provider 的 receipt fallback；相同 URL 不会合并两个独立 RPC 对象的状态。

完整原版 `sync-historical/in-memory.test.ts` 与 `index.test.ts` 共 40 条展开记录、102 个断言和 12 项类型错误义务。原 hooks、Anvil、测试输入、断言、状态、返回清理与 RPC 对象身份保留。Go 独立执行每个公开方法序列：原始 RPC 响应仅作为外部 provider 输入；原版标准化结果和预期 SQL 行不用于填充 Go 状态。严格比较返回值、Map 插入顺序及键身份、字段存在性、undefined/null/BigInt，以及调用后的九张实际物理表。每个后端、每种 Go 模式执行 136 次公开调用。

12 项类型义务分别构造具体负向 Go 编译控制，验证未缩窄过滤器的地址成员被拒绝，或宽泛 RPC 返回值不能替代 `[]rpc.SourceLog`。正向程序使用原始修改前的 callback 输入，自行修改具体变体并执行真实获取。每个程序在其对应原例比较完、Go 对照数据库关闭后，通过独立 afterEach hook 运行；原例返回的 Anvil cleanup 随后执行。持久化程序使用独立实际 PG/PGlite schema，内存程序保持无数据库行为。两个后端和 normal/race 合计 48 次正向程序执行；原版 TypeScript 则用四个只读程序验证每个文件 pristine 无诊断、仅在内存中移除指令后各产生恰好六个诊断。

补充的 44 个区块、25 个子地址、12 个异步合并、25 个所需区间向量来自实际固定源码；另有十项有界预取、factory drain、真实 EVM/数据库和 provider 状态所有权回归。这 116 项不增加原例映射数。

早期失败与对应输入均保留。观察器曾替换 RPC 对象，破坏原版 WeakMap/WeakSet 的身份；实际对照证明后改为保留同一对象。两个 typed provider 构造器曾各建 `Actions`，现由 provider 持有。正向程序曾将第三阶段扩展到新区块的部分缓存请求误认为全缓存，现保留原版第三阶段的一次新区块 RPC。错误在 afterEach 中延后报告，使原版返回清理仍执行；原始输入、断言和超时未改。

`tools/verify.sh` 先生成实际源码向量、独立 pristine、原版 TypeScript 审计和 Go 编译控制，再冻结原生输入。`tools/source-historical-results.mjs` 只从本次完整 normal/race 日志收集接受证据；专项成功不能代替完整模块验收。最新结果见 `parity/runs/summary.json`。

这些接口补齐历史范围获取的模块原例。完整历史调度器、历史/实时交接、多链排序、finality 编排、metrics/context、完整配置 DSL 和任意 JavaScript 行为仍需继续实现；既有 `ReadSourcePage` / `DispatchSourceEvents` 的事务页消费另见 [SOURCE_SYNC_STORE.md](SOURCE_SYNC_STORE.md)。
