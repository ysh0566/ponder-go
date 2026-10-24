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

## M9：缓冲 ORM 与 cache/defaults 源例

新增 28 条源映射，旧 delete 映射升级为缓冲 ORM；累计 204/905（200 Go + 4 SDK），701 未实现。200 条 Go 映射通过，167 条原版 oracle 也通过、33 条 DB oracle 因待批准 Foundry fixture 阻塞。保留 SDK 4 条通过。新增 mapping 的 fixture/断言审计见 parity/orm-source-cases.json；没有用 native 边界测试扩增源数量。

实现事件内读写缓存、返回值深副本、insert/update/upsert/delete、默认值/NULL/Undefined、数组/bytes/JSON/timestamp/point/line 映射、批量 recordset flush 与 savepoint 恢复、typed table Select、raw/direct SQL 可见性、事件结束后 guard。通过双后端批量失败回滚、global suffix undo 后重放、isolated 复合键隔离、视图读取刷新和 SQL 错误后恢复。完整 Drizzle 关系 DSL、profile/prefetch/eviction、COPY 性能和剩余含类型错误指令的原例仍未实现。

全套 Go/race 各 306/306，明确 PostgreSQL 65/65、PGlite 64/64；原 recovers-error 用例仅在 PG 运行。所选原版 core oracle 活动 167 通过、原 skip 2，SDK 4/4，vet/build 和 2466 差分向量通过。53 typed ABI runtime/race 与编译 fixture 另计。Foundry 未安装。准确命令、hash 和已知 JSON NUL/bytes PK/custom codec 边界见 batch-7.json 与 CONTINUATION.md，整体复刻仍未完成。

## M10：无损 JSON/binary undo 与批次恢复

新增 8 个原生后端叶子回归，源映射仍 204（200 Go +4 SDK），701 未实现；200 Go 通过，167 原版 oracle 也通过、33 DB oracle 阻塞。JSON U+0000、raw SQL 超 JS 精度 JSON 数字、bytes 主键、显式旧 journal 格式拒绝、1001 行第二批失败后的完整 savepoint 回滚和纠正重试均在 PG/PGlite 通过。修复删除后重插造成顺序表重复发出同一 key 的问题。

存储格式 json-v2：before_row 为 json，主键 trigger 直接读取 OLD/NEW 字段，row_to_json 快照，逐列参数化批量写与无损 raw JSON undo；默认新影子 ponder_go_shadow_v2_<hash>，不迁移旧 schema。全套 Go/race 各 314/314，PostgreSQL 69/69、PGlite 68/68；vet/build、所选 oracle/SDK、53 typed ABI/runtime/compile fixture 和 2466 差分向量通过。Foundry 安装仍待批准。batch-7/8-evidence 保存不可变日志副本与 hash，避免刷新当前日志后失去证据。完整功能与测试移植继续按 CONTINUATION.md。

## M11：profile 原例与独立 oracle 阻塞

新增 13 个 profile 源例，保留 19 个原始 pattern/recovery 断言、事件/schema/键/hint/分隔符输入；完整源/适配在 profile-source-cases.json 与 ledger。7 个额外原生分支用例单列。原版 profile Vitest 实际尝试，因 _test/generated.js 缺失在收集前失败：0 例执行、1 suite 失败，不算 oracle 通过。当前函数尚未接入完整 prefetch/eviction 或跨事件 cache，原生 FieldOrder 与 ABI 对象顺序接入待完成。

完整 verify 退出 0：Go/race 各 334/334、无 skip；PG 69/69、PGlite 68/68；vet/build、167 个所选 core 活动原例（原 skip 2）、保留 SDK 4/4、53 typed ABI/runtime/compile fixture 和 2466 差分向量通过。累计 217/905（213 Go +4 SDK），213 Go 通过，其中167 原版 oracle 也通过、46 原版 fixture oracle 阻塞；688 静态记录未实现。Foundry 未安装。所有最终日志的不可变副本/hash 见 batch-9.json。

## M12：typed ORM、编译义务与 cache 预取/淘汰

新增5条源映射（14 runtime断言、5个编译错误义务），累计222/905：218 Go通过，167原版oracle也通过、51 fixture oracle阻塞；4 SDK通过，683未实现。5个源错误要求和6个额外负向控制都配有可编译正例及预期诊断的真实Go拒绝。生成模型/API/tool输入hash一并验证，编译结果不再由运行时error代替。

Scalar typed CRUD、nullable/unchanged、JSON往返与CLI codegen --typed 已实现。缓存增加采样/预测/批量预取/负缓存/spillover淘汰/内存预算，共享profile元数据，业务行留在事件事务内。原 eviction逐例映射；合成事件预取、32并发更新、跨事件/rollback/reorg/内存等补充测试单独计数。原 deploy/mint/readonly metadata prefetch仍未映射。

最终完整 verify退出0：Go/race各360/360，无skip；PG81/81、PGlite80/80；vet/build、所选core原oracle167活动通过（原skip2）、SDK4、ABI typed runtime/race各53、原类型检查及2466差分向量通过。所有边界、生成依赖、未实现项与不可变日志/hash见batch-10.json和CONTINUATION.md。Foundry1.8.5仍待批准，未安装。剩余database actions尚未完成，整体任务继续。

## M13：durable staged database actions

新增6条源映射，3条既有finalize/revert映射升级为完整staged fixture；累计228/905（224 Go+4 SDK），677未实现。224 Go通过，167原版oracle也通过、57 fixture oracle阻塞。原actions.test.ts目前10/11映射，large-payload notify未移植。

RawBatch持久化业务+MAX journal；CommitBlock按表且isolated按链赋checkpoint；AdvanceLatest单独更新metadata，也支持Writer作用域共事务执行。CrashRecovery撤销全部保留journal并保留metadata，调用方再复位latest=safe。空schema finalize保留latest/其他链；非空未初始化metadata和pending边界拒绝丢弃undo。永久guard防止drop capture后绕过journal。json-v3持久row_data/operation并隔离staged/atomic模式，新影子升级不接管旧DB。

14个新应用进程退出叶子与6个guard/ordering补充叶子通过；最终Go/race各398/398，PG100/100、PGlite99/99，无skip。vet/build、可运行oracle/SDK、ABI/ORM编译fixture及2466差分向量通过。Foundry未安装。精确命令、限制和不可变证据hash见batch-11.json；自动staged runtime/migration和所有剩余功能继续。

## M14：真实数据库通知与发布身份审计

新增1条原版 large-payload notification 映射，保留160表名输入、两种临时表生命周期和全部3条断言。actions.test.ts 的11条源记录均已有映射，完整原版 oracle 仍受生成合约fixture阻塞；不能视为整个数据库测试已完成。原生LISTEN/NOTIFY、7900字节JSON分片、表集合收集、ORM/视图写入和checkpoint同事务提交已实现。PGlite响应与异步通知分流，PG专用连接；另14个后端叶子验证回滚不发通知、订阅/取消/关闭、多消费者、慢消费者不丢300消息、UTF-8转义和撤销/finalize的collector边界。PG撤销缺临时表的实际红灯已修复，保留失败和通过记录。

完整verify退出0：Go/race各414/414，无skip；PG108/108、PGlite107/107。vet/build、原版所选core167活动通过（另2个原skip）、SDK4、ABI/ORM编译义务、2466差分向量通过。累计229/905映射（225 Go +4 SDK），Go oracle167通过、58 fixture阻塞，676静态记录未实现。证据及hash见batch-12.json。自动staged runtime/重组通知调度、外部通知接入SQL/SSE、views-schema通道、角色权限与重连/重放均未完成。

用户授权后创建私有 https://github.com/ysh0566/ponder-go 并推送至e9747c7；远端owner/private/SHA核验记录在publication-receipt.json。随后用户指出作者应为ysh0566，审计发现13个已发布提交使用plato9966/Codex。项目local身份已修正为ysh0566及API确认账户ID对应的官方noreply地址；全局不变。备份引用refs/backup/author-before-ysh0566保留，未改写/强推，后续推送等待用户确认历史修复。当前GitHub协作者API仅列owner，不能由此断言无人clone。Foundry安装仍待批准。

## M15：启动租约、失败迁移和恢复事务

历史身份修复获得用户明确批准后完成：13条错误身份改为ysh0566，旧e9747c7对应a7a1f30，原M14的6c9b2f1重接为8dc922b。14棵tree逐一相同，日期/消息保留，旧引用和工作区备份保留；之后仅普通快进发布，修复审计提交b478a1a。见author-repair.json。该一次强推许可已使用完，不扩展到后续历史修改。

新增8条database/index源映射，保留21条断言及所有fixture、abc/def、heartbeat时间、重复kyle断言和原PGlite条件分支；PG8/PGlite7个源叶子通过。原物理meta/checkpoint/per-table reorg分别适配实际native app/progress/global journal，绝不声称原始布局兼容。NewLifecycle延迟到Migrate才做DDL；新影子创建、build/version/fingerprint检查、租约一次等待重试、heartbeat、条件unlock、same-build恢复和drop secondary index/trigger已实现。

补充12个后端边界、8个真实进程退出叶子验证token fencing、旧close不解锁新owner、同步/factory/RPC写、失败恢复DDL+数据原子回滚、初始化清理、finalized时间、取消等待和慢启动。旧SaveBlock绕过fence的PG/PGlite红灯保留并修复。恢复撤销assigned/MAX保留journal，返回safe而不改latest；自动runtime接续仍待实现。没有服务端/宿主断电或并发多进程PGlite保证。

完整verify退出0：Go/race各449/449，PG126/PGlite124各通过，无skip；vet/build、所选core167活动oracle（原skip2）、SDK4、ABI/ORM编译义务和2466差分向量通过。累计237/905（233 Go+4 SDK），Go原版oracle167通过、66fixture阻塞，668静态记录未实现。未改原版database/index测试的实际oracle尝试为收集前缺generated.js：0测试、1失败suite，不记oracle通过。精确命令和不可变证据hash见batch-13.json。Foundry仍未获安装许可。

## M16：版本化sync缓存、迁移快照和整数传输

新增8个migrateSync源例，保留13条断言和完整fixture/数据/异常/并发时序，PG8/PGlite7个适用源叶子通过。create.sql、copy.sql、legacy fixture三份资产与上游字节/hash相同；只将schema标识符重映射为任务拥有shadow。最新9表列/PK/identity/索引/范围类型完整保留。repeatable-read一事务复制源允许6表，按原SQL排除trace/transfer/factory数据；legacy行和Kysely history不变。PG真实锁等待证明late commit不进入迁移快照。并发命名冲突仅在核验已提交完整owner后恢复。

补充10个后端边界、4个真实应用退出及SQL资产校验，覆盖完整列值、NULL/大整数/opaque文本、部分copy回滚、提交确认丢失重试、16实例并发、未拥有/不完整schema拒绝和磁盘重开。原生补充fixture最初误用最新版trace_address，改为原legacy trace_index/subcalls，未改原SQL资产；失败记录保留。真实PGlite红灯暴露JSON number舍入int64最大值，store/values.go现统一以十进制字符串绑定Go整数；PG/PGlite完整回归均通过。

完整verify退出0：Go/race各479/479，无skip；PG141/PGlite138分别通过；vet/build、core167活动原oracle（另原skip2）、SDK4、编译义务与2466差分向量通过。累计245/905（241 Go+4 SDK），Go oracle167通过、74fixture阻塞，660静态记录未实现。原数据库oracle再次收集前缺generated.js（0测试/1失败suite），不能算原版通过；Foundry未安装。原runtime最小sync store与新9表协议cache仍分开，完整事件CRUD/自动runtime/真实旧DB接管未完成。见batch-14.json与SYNC_MIGRATIONS.md。

## M28：来源配置与原生运行时

新增35个精确配置原例/85断言及1项公共Filter联合编译义务；累计360/905，545未实现。正式full02 Go/race各1852，PG198/PGlite195，0fail/skip，20包138函数48批次。39个活动原例有69次实际Go调用、完整原版oracle与输入/输出比较；原skip保留，4例9个类型义务仍未映射。来源回调/overload/topics/receipt/实际handler/client/factory身份接入单链运行时；8个真实集成与1并发例不加source credit。原生规范metadata身份检查60次，含Hyperliquid2次。新增测试失败与首轮主动停止1473项部分执行均保留；正式证据batch-27及SOURCE_CONFIGURATION.md。完整目标继续。
