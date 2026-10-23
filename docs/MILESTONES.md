# 可检查里程碑

## M0：固定基线与审计

完整上游已下载并固定到 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`，版本 0.18.2。`parity/public-manifest.json` 保存 public exports、配置、9 个 CLI 命令构造、选项和模板/示例 hash。`parity/test-ledger.json` 保存 104 个文件、897 个测试声明的 905 条静态展开记录与原始源码、参数和断言。

`parity/discovery-audit.json` 独立扫描所有跟踪的 JS/TS 文件，记录测试导入/别名、4 个 each 声明、hooks、247 个本地 support/fixture 模块及 8 个 simulation 应用。没有将静态扫描当成全部 runtime collection。有限记录中的 unresolved 为 0；不代表无限 simulation seed 或任意动态 JS 已穷尽。

## M1：基础逐例移植

checkpoint 15 条、interval 19 条原版用例均映射至同名 Go subtest。保留实际输入和全部断言，包括输入不变性。原版 oracle 与 Go 各 34/34；另有 2,400 个固定种子的差分向量独立验证。

## M2：真实数据库与回滚

PostgreSQL 16.15 和 PGlite 0.2.13 均验证 CRUD/upsert、精确大整数 snapshot、失败原子性、去重、multichain 全局 operation_id 后缀回滚、isolated 回滚和 finalize 保留安全前缀。应用进程在业务写后、journal 后、进度后、commit 后分别退出；两个后端共 8 个边界验证通过。取消的 PGlite commit 也验证清理事务。

这些是新增 Go 回归测试，没有据此把原版 indexing-store/database 用例标为已移植。SQL raw write journal、完整源 schema/默认值/关系/视图语义仍未实现。

## M3：单链运行和客户端协议

真实本地 Anvil 生成 ERC-20 事件，Go 读取日志和 receipt、执行 setup/block/事件 handler、持久化 PostgreSQL，遇到真实链重组后撤销并重新索引。HTTP/WS JSON-RPC 与新区块订阅已验证。安装的 Anvil 0.2.0 仅用于新增垂直验证，不冒充上游要求的 1.8.5 oracle 环境。

原 TS 客户端源代码连接 Go SQL/SSE 服务，在两个后端各验证 7 条协议断言：大整数参数/结果、SQL 列顺序、status、初始 hash 不推送、同结果不推送、变化结果推送、取消订阅。保留 SDK 自身的 3 条原 unit tests 和 React 1 条原 type fixture，共 4/4。

当前 ledger 为 **89 条已映射、执行并通过，816 条未实现**；其中 2 条原版 SQL oracle 尚阻塞。当前全部 Go 与 race 的确切叶子用例数量、实际版本和原始日志读取 `parity/runs/summary.json`，避免文档与新一轮结果计数漂移。

## 当前批次后的工作

继续 `docs/CONTINUATION.md` 中的 schema/ABI/finality 源测试逐例移植，再推进账户、trace、factory、多链调度、完整数据库与 GraphQL、CLI/模板/热重载、全部测试及 simulation。完整功能范围逐项登记在 `parity/feature-ledger.json`，其中 `verified_subset` 只表示已验证的子集。

没有接管旧 DB、修改其他业务仓库、创建远端、push、PR 或部署。计划 PDF 下载失败；没有声称读过该 PDF。

## M4：schema / event / finality 精确移植

新增 24 条 build/schema、19 条 decodeEventLog（21 条原断言）、6 条 finality，均分别在 Go 与未修改的原测试通过。事件 fixture 从源断言提取预期，保留完整 snapshot 字符串与错误匹配；没有使用被测实现生成预期。类型化 enum 和五种 view 在 PG/PGlite 执行、回滚、重开验证通过。当前 Go/race 各 118/118，所选 core oracle 83/83；参数解码器内联类型断言与完整 ORM/SQL journal 仍需继续。

## M5：统一 trigger journal 与 raw SQL

删除 Go 手动 journal，所有 business table DML 通过同一数据库 trigger 捕获。双后端验证批量 SQL、可更新视图、事件外写拒绝、savepoint 错误恢复、全局后缀回滚、PK/DDL/内部设置保护、journal CHECK 故障原子性。原客户端分别在 base/nested view、PG/PGlite 四种组合执行，每种 7 条协议断言通过。新增 2 个源 SQL case，Go 双后端通过；原版 oracle 因待批准的 Foundry 1.8.5 setup 阻塞，单独列出。当前整套 Go/race 各 138/138、无 skip；整体复刻仍未完成。

## M6：ABI 参数、类型 fixture 与数据库 actions

从 e6d0a4d 新增 74 条精确映射：ABI 参数 69 条活动 case（78 条 runtime 断言、53 条内联类型要求），database actions 4 条，indexing-store delete 1 条。ABI 的 2 条原版 skip 保留、不运行、不计通过。累计 163/905（159 Go + 4 SDK），742 未实现。原版 oracle 156 映射通过（152 Go + 4 SDK），7 条数据库 oracle 阻塞，不能称全部差分验证通过。

真实 PostgreSQL/PGlite 本批各新增 5 条源 case 验证（计入单一 PG 映射，PGlite 为伴随验证），保留原 schema、NULL、10n/12n/88n、checkpoint 和全部断言。原 raw write + commitBlock 映射为原生原子 Apply。全套 Go/race 各 221/221；ABI typed runtime/race 各 53/53 另计。成功编译 fixture 53/53；额外负向控制 53/53 按预期拒绝，未充作源测试。所选 core oracle 活动 152 通过、原版 skip 2；原 ABI tsc 通过。vet/build/保留 SDK/2400 差分向量均通过，详见 batch-4-verification.txt 和 summary.json。

台账独立列 source_records / mapped_go / retained_sdk / original_oracle / type_fixtures / simulation。63 个内联类型断言与 84 条错误指令现在显式发现，不能用 73 个专门类型记录代表全部类型义务；尚余 10 个内联义务、绝大部分专门类型测试和错误指令待移植。静态发现仍非动态穷尽证明。

下一批继续 factory/offset 原测试与动态地址持久化、更多 DB cache/CRUD/actions。raw SQL 审计还发现 SELECT INTO 可能创建非声明表，下一步补显式拒绝与回归；完整 Drizzle、自动 finalize、无表 finalization 和多链 runtime 仍待实现。Foundry 安装仍待批准，不安装。

## M7：factory / offset 与写入边界

在 8923f8f 后再增 13 条源映射（factory 6 + offset 7），全部 20 个源断言在 Go 与原版通过。累计 176/905：172 Go 映射通过，其中 165 原版 oracle 也通过、7 DB oracle 阻塞；另 4 SDK 通过，729 未实现。所选 core oracle 活动 165/165，原 skip 2（共 167）。全套 Go/race 各 238/238、无 skip；其中明确命名的 PostgreSQL 32、PGlite 32，分别全部通过。ABI typed runtime/race 各 53/53 另计。确定性差分 2466/2466（factory 66 + 基础 2400）单列。

已实现 factory 参数/显式 location、事件 selector、地址归一化/排序/来源 ID、嵌套 ABI offset。Config.Contract.Factory 接入单链引擎；发现保存在独立 sync schema 的 factory_addresses，按 canonical block/hash/log index 重放替换，遇到重组 rewind 删除旧分支。原子业务 handler 失败后同步发现可以重试，PG 连接/PGlite 磁盘重开、同区块 child 日志、后续日志、去重与重组改换 child 地址均通过新增集成测试。原版完整 factory E2E/过滤片段/trace/多链仍待实现，未据此计通过。

SELECT INTO（含 TEMP）、多语句写入显式拒绝；双后端另验证 CTE DML 与可更新视图 DELETE 都进入同一 base-table journal，删除重组恢复、finalize 清理与 safe checkpoint 拒绝回滚均通过。SQL 仍是声明内单语句子集，不是全部 Drizzle。

类型审计补出此前 event errors:namehash 的 @ts-expect-error（null topics），现有独立 Go 成功/失败 fixture，且原 event 文件与 parameter 文件一起 tsc 通过。53 个内联类型要求和 1 条源错误要求分开计数。成功/失败编译证据见 compile-fixtures.json，不能把辅助 53 个负向控制当成新增源用例。Foundry 1.8.5 安装仍待用户批准，未尝试。

## M8：原子 rewind 与提交确认恢复

最后复核补上两个实际恢复窗口：原先业务 Revert 与 sync RewindSync 为两个提交；原先 block header 也晚于 handler 提交。现在 runtime 使用 Store.RevertAndRewind，在同一个 PG/PGlite 事务中撤销业务、journal、events/progress、同步 block/interval 和 factory 地址。sync/indexing schema 仍分离，但当前 native backend 在同一个数据库中保证提交原子性。每个 block 的 header/provenance 在任何 handler 提交前先存储，完整区间标记仍只在处理成功后提交。缺失 indexed header 会明确要求 shadow rebuild，不继续叠加未知旧状态。

新增应用进程退出边界：after_undo、after_sync_rewind、after_commit，两个后端共 6/6，通过真实 os.Exit(73) 与重开验证业务余额、进度、journal/events、sync header、缺失区间、factory 地址全部处于提交前或提交后状态。另外双后端模拟 handler commit 成功但确认丢失，在 CompleteBlock 前重开并换链，能凭预存 header 撤销孤块写并重新索引。没有声称服务端/宿主断电验证，也没有把新增回归测试算成源移植。

最终全套 Go/race 各 244/244，PostgreSQL/PGlite 明确后端叶子各 35/35；typed ABI runtime/race 各 53/53，vet/build、选定原版 oracle/SDK 和 2466 差分向量通过。源映射仍 176（172 Go + 4 SDK），其中 Go oracle 165 通过、7 DB oracle 待 Foundry 授权，729 条未实现。具体命令、hash 和限制见 parity/runs/batch-6.json。

下一步仍为完整 ORM/cache/defaults/actions 源例、source/filter/fragments、账户/转账/trace、raw event 持久化与多链调度、完整查询/CLI/类型/simulation。当前 source startBlock 之前没有持久共同祖先等恢复边界会明确报错要求重建；不得把现有单链测试扩大解释为完整 multichain/crash parity。
