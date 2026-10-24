# 原版同步存储的 Go 接口与验证

`ProtocolCache.SourceSyncStore(0)` 在任务拥有的版本化同步表上提供原版 `SyncStore` 操作。默认参数预算为 16000；区间、factory 子地址、协议数据、分页查询和 RPC 结果共用实际 PostgreSQL 或 PGlite 后端。

原版 `packages/core/src/sync-store/index.test.ts` 包含 38 个活动声明、63 个断言表达式和 9 条类型错误指令。完整原文件保留原始 hooks、生成 ABI、Anvil 输入和断言，Go 在独立 schema 执行同一次真实调用。比较返回值、调用后的九张物理表、Map 插入顺序、对象身份、undefined 与 NULL；原版返回值继续用于原版断言。独立 pristine 执行和带观察器的执行分别记录。

| 接口 | 行为 |
| --- | --- |
| `InsertIntervals` / `GetIntervals` | 使用原版 fragment ID、相邻区间合并和 `[start,end+1]` 存储。按原版预算分批提交，保留 Map 键身份和顺序。 |
| `InsertChildAddresses` / `GetChildAddresses` | factory 元数据与子地址分两阶段写入；冲突保留首次发现的区块，支持共享父地址。 |
| 协议数据插入 | 写入 blocks、transactions、receipts、logs、traces，复用实际协议编码。 |
| `GetEventData` | 合并日志及 trace 范围，按所有过滤器选择列，保留字段缺失、undefined、NULL、BigInt、trace 顺序和原版分页游标。 |
| `PruneByChain` | 清理原版指定的六张链数据表；intervals、RPC 结果与 factories 的处理遵循各自接口。 |
| `GetSafeCrashRecoveryBlock` | 使用严格时间戳上界和倒序区块查询。 |
| RPC 结果接口 | 使用原版 `MD5(lowercase(JSON.stringify(orderObject(request))))`，冲突不覆盖；block 0 存为 NULL，超过 100 个数字区块请求使用范围查询。 |

`SourceRPCRequestCache` 明确选择原版 RPC 哈希；既有 Go 缓存继续使用自身 SHA256 键和覆盖语义。两种键可以在同一归属同步区内共存。事件中的 source RPC 结果通过 `Writer.SourceRPCRequestCache` 与业务写入、journal 和 checkpoint 一起提交或回滚。

`rpc.SourceCallRequest` 的 `Params` 使用 `SourceCallParameters`，拒绝原测试中的错误字符串数组。9 个负向编译 fixture 保留各自原始错误字面量，正向 fixture 只将错误交易参数改为 `{}`，其余存储参数保留。正向程序在原测试使用的真实 Anvil 上发出 `eth_call`，并执行实际存储、哈希检查和 pruning。所有 38 项原例的方法与状态比较完成后，先关闭 Go 对照数据库，再通过逐项 afterAll hook 运行 9 个独立正向程序；原测试的 Anvil 持续到 global teardown，原始超时保留。这样限制同时存活的 PGlite WASM 实例数量。当前类型接口覆盖 `eth_call`，更广的 RPC schema 与返回类型推断仍需实现。

`Engine.ReadSourcePage` 将缓存页送入已有事件构建和 ABI 解码器；`DispatchSourceEvents` 使用已有业务事务或 staged actions。真实 EVM 集成覆盖两个后端、缓存开关、handler 失败回滚、重复重放和换分支后重新索引。范围获取、缺失区块补齐、区块来源及 finality 由调度器负责；这些页接口不构成完整历史同步调度器。

`tools/verify.sh` 在原生测试前准备真实源码向量、编译控制、原版 TypeScript 诊断与两个后端的 pristine 结果，再冻结原生输入。`tools/source-sync-results.mjs` 从完整 normal/race 日志找到本次实际运行目录，校验全部原例、方法调用、原版断言和 9 项编译义务；专项结果不能替代完整模块验收。最新完整验收状态见 `parity/runs/summary.json`。

40 个 RPC 哈希向量、12 个范围向量、RPC 边界、区间 rewind 和 EVM 页处理均为补充验证，不增加原测试映射数量。任意 JavaScript 对象行为、所有无效输入的强制转换、指标和 context、完整历史调度与多链排序仍需继续。
