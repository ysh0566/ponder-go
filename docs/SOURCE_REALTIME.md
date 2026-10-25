# 原版实时同步接口

固定源码为 Ponder0.18.2 / `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`。`runtime.NewSourceRealtimeSync` 实现该版本 `createRealtimeSync` 的原生内存接口：输入真实 RPC requester、可共享的 `rpc.Actions`、factory child map、finalized block、原生 filter 对象及链参数；`Sync` 返回 Go `iter.Seq2[SourceRealtimeEvent,error]`，另有 `OnError` 和 `UnfinalizedBlocks`。

`block`、`finalize`、`reorg` 事件分别保留原字段。区块事件携带消费者提供的回调；同步器不会自动调用 `true`，消费者完成自己的处理后负责确认。重复块、重组、获取失败或超出补抓上限时调用 `false`。同步器本身不开数据库，也不替代业务事务提交和 checkpoint 协调。

获取阶段在锁外运行；协调阶段的锁跨越迭代 yield。提前停止消费者会释放锁，同时保留原版在 yield 之后才进行 finalization 的行为。缺块补抓保留原版右端不包含的范围：远端头超过50块时，第一轮获取49个中间块。错误重试保留至少10次且严格超过10分钟的阈值；shutdown 产生明确的 `SourceShutdownError`。时间戳 finality、重复获取、父块搜索、深重组恢复及共享父 factory 的 child 出生块删除均保留源语义。

日志 bloom、异步执行链绕过、零 transaction hash、factory 弱匹配之后的完整过滤、所需 transaction/receipt 获取及最终裁剪复用已有原生协议与过滤实现。`rpc.FlattenSourceCallTraces` 保留原始 provider frame 的身份：先遍历并原地设置所有 traceAddress，再按源顺序标准化。现有复制 frame 的辅助接口保持自身语义。

M34 对完整 `sync-realtime/index.test.ts` 的30条展开原例、203个断言逐例建立映射，保留原始 hooks、输入、断言、状态和返回 cleanup。原版 PostgreSQL/PGlite 各自使用真实任务数据库环境与 Anvil；Go 运行实际无数据库的实时内存接口。每个 backend/mode 对照83个公开调用和46个原始 provider 请求，另外3个原测试准备 RPC 请求单独记录。原版归一化输出不用于填充 Go 状态或返回值。

对照包括实际消费者 next/return 步骤、独立状态、输入块的修改、Map/Set 顺序、frame 身份、字段是否存在、undefined/null/BigInt 与 Error 的 name/message/code。原版警告及断言保持完整；额外的原生警告比较仅排除不可确定的计时时长。另有两种后端独立执行的未插桩完整原文件作为 pristine oracle。

24个额外实际源码向量不增加原例计数：12组 trace 身份/修改顺序/错误向量，12组回调所有权、提前结束、并发去重、锁、补抓及重试边界向量。旧复制 helper 在同输入下10项失败，新原地 helper 全部通过。独立 RPC observer 控制验证原 RPC 对象的分页/receipt fallback 元数据、后续 once-mock 及并行请求各只采集一次。

验收记录见 `parity/runs/source-realtime-verification.json`、`parity/runs/realtime-m34/` 与 `parity/runs/batch-33.json`。初轮采集器误拒绝测试准备请求、漏选 trace 测试名和磁盘耗尽导致的编译中断均保留原始结果，未冒充断言通过。完整验证在准备后冻结原生输入，在两种模式之间按需清理本任务可重建编译缓存；不删除源码、fixture、模块、类型正向程序或证据。

这份证明覆盖实时获取与内存协调 API。完整 runtime 历史/实时/多链调度、业务消费事务协同、metrics/logger context、完整 RPC 泛型与任意 JavaScript 行为尚未全部实现；runtime/index 与 runtime/historical 原例以及其他剩余模块继续属于整体任务。

M34完整验证的首轮race中，两组PostgreSQL写入遇到SQLSTATE53100（磁盘耗尽）。完整正常模式、其余真实成功race批次及原始失败均保留；同一1065项输入下重跑受影响完整批次。最终race日志由56组实际成功命令的原始stdout逐字节组合，原始失败与总58次命令尝试有独立来源清单。full-verify-02是完成剩余严格汇总后的恢复验收，不冒称原full-verify-01成功。
