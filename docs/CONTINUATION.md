# 继续工作

任务没有完成。先读 `AGENTS.md`、本文件、`parity/runs/summary.json`、`parity/test-ledger.json` 和 `parity/feature-ledger.json`。不要把新增集成测试或差分向量算成上游逐例移植。

## 已保留的状态

- 工作目录 `/Users/ysh0566/Documents/Codex/2026-10-23/task/ponder-go`。
- 完整上游 `/Users/ysh0566/Documents/Codex/2026-10-23/task/ponder-upstream`，固定 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`。
- 已创建独立 Go Git 仓库并发布到私有 `https://github.com/ysh0566/ponder-go`。用户后续明确授权推送经验证阶段；每次确认账号、远端与 SHA，不开 PR、不部署。Foundry 安装仍待批准。首次发布凭据不落盘、不切全局账号；receipt 在 `parity/runs/publication-receipt.json`。
- 用户已明确批准一次历史身份修正并已执行：13个错误author/committer全部改为ysh0566，旧e9747c7对应新a7a1f30；原M14提交6c9b2f1安全重接为8dc922b。14个提交的tree逐一相同，日期/消息不变。旧新SHA映射在 `parity/runs/author-repair.json`；保留 `refs/backup/author-before-ysh0566`、`refs/backup/before-author-repair-20261023` 和忽略的 `.local/author-repair-20261023/` 工作区备份。修正后恢复普通阶段推送，不再强推。Foundry许可仍未收到。
- 已启动现有 Docker Desktop，并创建 `ponder-go-task-20261023` / `ponder_go_task_20261023`。先 `docker inspect` 核对存在和 `127.0.0.1:53398`，直接复用；不要操作其他容器。
- Go 缓存位于 `.toolcache/`，基线 npm 依赖已按冻结 lockfile 安装。所有 SDK 源码原样保留在 `sdk/typescript/`。
- 计划 PDF `libfile_90c2e75070c481918ecace631e841e0f` 下载失败，没有本地副本；完整需求已经在初始委托中给出，无需等待它。

## 下一可执行批次

1. 从最新提交和 summary.json 恢复。M21 后累计 288 条映射（284 Go + 4 SDK），Go oracle 191 通过（含4条选择性未改动声明）、93 fixture oracle 阻塞，617 静态记录未实现；精确执行结果看 batch-19.json。不要重做已通过源例，也不要把静态发现当成动态穷尽证明。
2. Database actions.test.ts 的11个源例及 database/index.test.ts 的16个生命周期源例已映射；M17已补齐剩余2个database/index记录（historical-baseline recovery rows和camelCase/views-schema），以及 ORM insert-then/full SQL 原例和 SQL 真编译错误义务。M16 sync迁移完整保留原SQL，但仅接受已拥有shadow，不代表真实旧DB布局接管；M17已将9表协议缓存接入默认单链runtime。M18已验证冷缓存重组，M19已接入自动单链staged runtime，M20已补32条filter/fragments；继续真实deploy/mint/readonly prefetch原例、typed JSON/bytes/array/custom codec/强枚举/Hex、关系DSL和自动profile/指标；随后接自动staged runtime及跨链重组通知调度。
3. runtime 已有 factory 动态地址单链路径（runtime/factory.go + store/sync.go），继续源 runtime/filter/fragments剩余Foundry源例、events构建/转换、sync-store原事件和factory原E2E；交易、原生转账、trace、完整 receipt/source 语义仍缺。66 个 factory 差分向量和双后端追加集成测试不能代替原 E2E。
4. 加入跨链事件调度，覆盖 omnichain/multichain/experimental_isolated 顺序。runtime.New 仍明确拒绝多链；仅存储层全局后缀/isolated 回滚已验证。自动单链staged runtime和自动finalize已接入，多链尚未实现；无 journal/无 checkpoint finalize 空操作已对应源 camelCase 验证，不能外推未初始化且有 journal 的情况。
5. 完整关系/查询/GraphQL、SQL/SuperJSON 全类型、全部 CLI/模板/热重载/观测/示例，以及严格源码兼容和旧 DB 接管的独立轨，仍按 feature-ledger 推进。默认新影子 schema，不访问业务数据。
6. ABI 原生类型生成和 53 个内联成功 fixture 已建立，另有 53 额外负向控制、1 个源 null-topic 错误 fixture；原两份 ABI 文件 tsc 通过。剩余 10 个内联类型断言、72 条专门 native 类型记录及绝大部分类型错误指令尚待移植。Simulation 的 8 个应用、无限 seed 空间尚未执行。

M16完整verify退出0：Go/race479/479，PG141/PGlite138分别通过，无skip。新增8个migrateSync源例共13断言、15个适用后端源叶子；10个native边界、4个真实进程退出和1个SQL资产校验不加源credit。store/values.go修复int/uint/int64/uint64在JSON桥接上的舍入（真实红灯int64最大值变为9223372036854776000）。sourceSQL三份字节/hash与固定上游相同，schema名执行时才重映射。PG真实ACCESS EXCLUSIVE竞争验证同一copy快照。原版数据库oracle再次收集前缺generated.js，0测试/1失败suite。完整证据/hash见batch-14.json和SYNC_MIGRATIONS.md；自动runtime和SQL/SSE外部通知仍待接入。当前无运行中测试。


`parity/public-manifest.json` 包含各包 exports、CLI 命令与选项、配置属性、模板/示例文件 hash 与当前文档路径。`parity/discovery-audit.json` 描述发现范围和静态限制。全套 Vitest 的 runtime collection 尚未完成，905 不是已证明穷尽的动态执行总数。

## 验证与台账更新

```sh
export PONDER_TEST_POSTGRES='postgres://ponder:ponder-local-test@127.0.0.1:53398/ponder_test?sslmode=disable'
./tools/verify.sh
```

脚本先生成发现台账，最后 `tools/record-results.mjs` 合入真实 Go/oracle/SDK 结果。单独运行 `manifest.mjs` 会清空执行状态，必须随后重新合入已有结果；不要把中间文件误报为最终结果。失败运行的旧报告保留供审计，当前结论读取 `parity/runs/summary.json`。

原版 34 条纯函数测试使用自有配置运行未改动的上游测试文件。该配置不执行与纯函数无关的 Anvil/合约全局 fixture。默认 core 入口曾在 `pnpm wagmi generate` 写全局工具缓存时失败；本机已有 Foundry/Anvil 0.2.0，完整上游要求 1.8.5。未安装升级系统工具，也未将默认入口失败算作测试通过。

## 重新准备 SDK 链接

`tools/sdk-bootstrap.mjs` 仅在本任务 SDK 目录创建指向固定基线依赖的链接，并为 React 指向保留的 client 包。不要改动 SDK 源码断言。随后构建 client 的忽略输出：

```sh
node tools/sdk-bootstrap.mjs
(cd sdk/typescript/client && ./node_modules/.bin/tsup)
(cd sdk/typescript/client && CI=1 ./node_modules/.bin/vitest run --configLoader native)
(cd sdk/typescript/react && CI=1 ./node_modules/.bin/vitest run --typecheck.only --configLoader native)
```

上游共享 `tsconfig.base.json` 已原样保留为 `sdk/tsconfig.base.json`。最初缺该文件导致 React typecheck 失败；补齐后原 fixture 已通过，早期失败报告保留。

## 尚未覆盖的边界

- 应用进程退出测试覆盖四个事务边界，尚无 PostgreSQL 服务端/宿主断电试验。
- PGlite SQL 正在执行时的即时取消、通用 SuperJSON 嵌套类型、外部 SQL 写导致的 SSE 通知和视图依赖尚未完成。
- 严格源码兼容与旧 DB 接管没有实现。新配置/handler 应使用新的影子 schema，不能复用旧数据并假定已重建。
- SDK React 浏览器验证、外部 provider 测试、simulation 与 benchmark 未执行。

## 第二批检查点

在 dcab6c7 后新增 49 条精确映射，累计 87/905（83 Go + 4 SDK）；本批整套 Go/race 各 118/118，无 skip，core oracle 83/83。见 `parity/runs/core-oracle.json`。枚举、raw/aggregate/aliased/nested view 的真实 PG/PGlite 回滚与重开通过。

Foundry v1.8.5 项目级安装批准正在由父会话向用户请求，尚未收到授权，不得先安装。已有 Foundry 0.2.0 不能充当该版本。下一批继续完整 DB action/store 原测试与 ABI 参数 runtime/类型 fixture；基础 raw SQL 已统一使用数据库 trigger journal。

## 第三批检查点

累计 89/905 映射（85 Go + 4 SDK），816 未实现；Go/race 各 138/138，所选 core oracle 83/83，SDK 4/4，另 2 条原版 SQL oracle 仍阻塞。所有映射的 oracle 通过数为 87，与 Go 执行数 89 分开。

`store/triggers.go` 现在统一捕获所有已声明 business table 的 INSERT/UPDATE/DELETE，包括 Writer、批量 raw SQL 和可更新视图；已删除旧的手动 journal 插入。`Writer.SQL` 使用每语句 savepoint，错误恢复后可继续查询；禁止 DDL、内部表写和设置内部 GUC。请保持此写入入口，不能加入绕过 trigger 的写路径。普通 Backend 事件外写会被数据库 guard 拒绝。

当前业务 SQL 和 journal 位于同一语句内，无法在应用中观察到两者之间的独立提交；旧进程测试的 after_business/after_journal 都位于业务语句结束后，后者额外确认 journal 行存在。注入 journal CHECK 失败另外验证语句内部全回滚。不要把这些说成服务端断电验证。

新增两个源 SQL case 位于 `store/sql_test.go:TestUpstreamSQLCases`，保留相同 account fixture、零地址、10n、RawSqlError 和后续查询成功断言，PG 是 ledger 主映射，PGlite 为独立伴随验证。完整 `sql` 源 case 含内联编译错误断言，仍未映射，不能忽略它来计通过。

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

## M9：缓冲 ORM、列映射与 cache 源例

本批新移植 28 条源记录，并将旧 delete 映射升级为实际缓冲 ORM；原 eager Writer delete 保留为额外回归，不再加一份源 credit。29 条审计记录见 parity/orm-source-cases.json，每条关联源 ID/hash、所有断言、fixture 边界和语言适配。所有原始输入、操作顺序、断言源码在主 ledger 保留。sourceORMTransaction 采用原版直接事务边界，不做 runtime 自动 flush；无物理 schema 的源例在专用影子中移除表。

新功能由 store/indexing.go、column_values.go、copy_text.go 和 schema.Column 承载：缓冲读写、冲突分支、copy-on-write 语义（原生深副本）、logical/physical 列名、数组/bytes/JSON/timestamp/point/line、默认值和 NULL、精确大整数 recordset 批量写、savepoint 恢复、事件返回后 guard。原生 flush 当前用 recordset 而非 COPY，不声称 COPY 性能等价。raw SQL 只读查询刷新涉及表，视图刷新全部，DML 刷新并使缓存失效；trigger journal 保持唯一写入来源。直接 Writer API 与缓冲 API 混用已增加实测可见性修复。失败回滚、批量 undo 后重放、isolated 复合键/跨链拒绝单独作为原生回归。

完整 verify 的实际数量和证据见 parity/runs/batch-7.json 与 summary.json。Foundry 安装仍 pending_approval，未执行安装；数据库源 oracle 不冒称通过。normalization 两条原版测试已实际运行。

紧接着补以下已知边界（不要扩大解释本批通过范围）：
- 上述 JSON U+0000 边界在 M10 已补：journal before_row 为 json，trigger 直接读取主键字段，普通快照用 row_to_json，写入/undo 按列绑定。undo JSON 使用 RawMessage 保留 raw SQL 大数字。jsonb 的 PostgreSQL 原生限制不被绕过；不删除 JSON NUL。继续验证全部 JSON/数组/自定义 codec 组合。
- bytes 主键与 undo 在 M10 双后端已验证；PGlite 仅按明确 binaryParams 位置将 Go base64 还原 Uint8Array，普通文本不作猜测转换。复合键已测整数+hex，仍不能宣称全部键类型完成。
- 自定义列 mapper 已有接口，完整 codec、默认函数返回不同 Go 类型、xy/abc geometry modes、nested arrays 和自定义指针副本仍需验证。
- 已补 1001 行第二批错误 -> 整个 flush savepoint 回滚 -> 删除非法 pending 行 -> 重插重试，验证无重复 journal；已修复删除后重插留下重复顺序条目。InsertMany API 输入归一化中途失败、取消/SQL 错误传播、跨事件缓存 profile 和 eviction 仍需更多源例。
- 缓存与 raw/direct SQL 混合操作必须继续通过 base trigger journal；savepoint 恢复失败会标记 Writer error 以禁止提交进度。

## M10：无损 JSON undo 与版本化影子

修复原生实现额外暴露的边界，不增加源移植 credit。PostgreSQL json 能存储实际 U+0000，但 json ->/populate_record 与 to_jsonb 都会解码整行而拒绝；因此当前 flush 不再用 recordset。store/row_sql.go 使用受控参数绑定到每列，最大 1000 行/60000 参数一批，所有批次受原 cache savepoint 保护。trigger 直接访问 NEW/OLD 的主键字段构造 jsonb row_key，before_row 为 json；undo 在 Go 中按列恢复，JSON 字段用 RawMessage 保留超出 JS number 精度的大数字和转义。用户读值保持原 JS number 语义，恢复路径独立保留数据库原值。

存储格式为 json-v2；默认 schema 名改为 ponder_go_shadow_v2_<hash>，fingerprint 含格式版本。显式旧 schema 会报 storage format changed 并要求新影子，不在本任务中原地改 journal 或接管旧数据。PGlite binaryParams 传输标记修复 bytes 主键直接查找/删除和撤销。测试含真实 NUL/字面量反斜杠 u0000 的区分、raw JSON 大数、ORM/raw SQL update/delete、abort、重开、逐块 undo、旧格式拒绝、1001 行第二批失败后的全回滚与缓存修正重试。

先通过定向 8 个后端叶子，再运行完整 verify；最终结果读取 batch-8.json 与 summary.json。下一步仍按上方完整未实现范围，尤其原生类型编译 fixture 和剩余原版 ORM/actions/cache profile/source/trace/多链/查询工具链。Foundry 许可未收到，不得安装。

M10 最终完整 verify 退出 0：Go/race 各 314/314；明确 PostgreSQL 69、PGlite 68，分别全部通过，vet/build/oracle/SDK/2466 差分向量通过。源映射/原版 oracle 计数不变。batch-7/8-evidence 为不可变证据副本。

## M11：profile pattern 原例

store/profile.go 新增 RecordProfilePattern / RecoverProfilePattern / ProfilePatternKey，包括 chainId/id、log/trace args/result、block 时间分组、transfer/transaction/receipt、分隔符及 hint。store/profile_upstream_test.go 逐例保留 13 个原名、全部 19 个 pattern/recovery 断言和键/事件/schema 输入；schema/chain helpers 的 callback 元数据仅保留在源 ledger，因为 profiler 不读取它们。7 个其他原生分支测试分开统计。parity/profile-source-cases.json 保存源 ID/hash/输入/断言与适配说明。

独立原版 Vitest 实际尝试：profile-oracle-blocked.json 报告收集前缺少 _test/generated.js，0 个测试执行、1 个 suite 失败，不算原例通过。Foundry 仍 pending；没有改源断言或替换 import。FieldOrder 用于保留有相等值的 args/result 的原 JS 顺序；native 缺少该元数据时按 key 排序，这不是任意原 JS object 的无修改兼容。运行时 ABI 事件还须携带顺序并接 profile/prefetch，不把纯函数移植说成完整缓存。

下一安全批次：
1. 先按 summary/batch-9.json 核实全量结果和最后提交；PG 容器继续复用 localhost:53398。
2. 将 profile 元数据接入跨事件 cache 与 prefetch/eviction，继续 cache.test.ts 2 原例；其中 prefetch profile 含类型错误指令，必须连同 Go 编译失败 fixture 移植。
3. 剩余 index 原例 missing rows/notNull/PK 更新/onchain table/完整 SQL 也含类型义务，不能用仅 runtime 通过抢先给 credit。需要可检查的原生 typed table/key/update API 或生成器；原动态 Row API 不能假称编译约束已覆盖。
4. database/actions 原例继续 commitBlock/完整 trigger 布局、empty schema finalize、通知大 payload；之后 source/filter/fragments/raw event store/trace/多链/GraphQL/CLI 仍为完整目标。

M11 最终完整 verify 退出 0：Go/race 各 334/334，PG 69/69、PGlite 68/68；完整构建和既有可运行 oracle/SDK/type/differential 均通过。累计 217 映射，213 Go 通过、167 Go 原版 oracle 也通过、46 原版 fixture 阻塞，SDK4通过，688未实现。阶段结束时无正在等待的测试；下一轮先确认 git diff/status 与 batch-9.json。

## M12：typed ORM、真实编译 fixture 与 cache 预取

新增 orm/orm.go、orm/generate.go 与 CLI codegen --typed。当前生成器支持 scalar 模型，提供模型专属 Key/Insert/Update/Row、必填参数构造器、nullable/unchanged patch、排除主键的 update、typed CRUD/Select、标准 JSON 往返。big.Int 用值类型拒绝隐式 nil，编码时深拷贝到原生 *big.Int。生成 row 的 MarshalJSON 避免按值编码 big.Int 得到 {}，Nullable 区分 null 与零值。JSON/bytes/array/custom column 的 typed 生成暂明确报不支持，不降为 any。Go 的零值语法不可禁止，零值 Key/Insert 在运行时由构造器守卫拒绝；不能宣传缺字段 struct literal 会像 TS 一样编译失败。

编译证据：tools/orm-compile-fixtures.mjs 真正 go build 成功/失败文件。5 条源错误义务对应 PK 更新两条、missing rows、offchain 绑定和 nullable/strict 模型重赋值；另有 6 个补充负向检查（错误列/值/模型/NULL/key）。每项要求正例编译成功、负例因预期诊断失败，并保存文件 hash、生成模型和 API/tool 输入 hash。record-results 合并 ABI/ORM 编译义务，未满足就阻塞源用例，不靠运行时返回 error 给类型 credit。5 个新增源记录与 14 条 runtime 断言在 parity/typed-orm-source-cases.json。

store/prefetch.go 已有 callback profile 采样、阈值预测、批量 SELECT、缺失行负缓存、spillover 一轮保留、两轮无预测淘汰和内存预算淘汰。profile 通过 Store 在事件间共享，业务行与未提交 buffer 始终留在事件事务作用域，防止回滚/reorg 后脏缓存。默认 native 预算 128MiB，可 CacheOptions.MaxBytes 指定；字节计数是 Node 风格估计、native 安全复合键编码会与原 key 长度不同，不声称精确 V8 heap 行为。runtime 仍须提供 Cache.SetProfileEvent(callback/count/data/FieldOrder)，完整自动接入及指标还未完成。

原版 eviction 原例逐例移植（invalidate、0 地址/10n、flush、两次空 prefetch、has=false）。原版 uses profile metadata 保持未实现：真实 deploy/mint fixture 与 readonly event 类型义务不能用本轮合成 native 事件替代。补充验证覆盖两个后端的存在/缺失预测、32 goroutine 更新与副本隔离、跨事件 profile、事务失败后的预取、reorg 后负缓存、内存预算、typed CRUD/nullable/JSON。它们不增加源数量。

生成依赖审计在 parity/foundry-generation-dependencies.json：packages/core/wagmi.config.ts Foundry plugin 默认运行 forge config + forge build --root src/_test/contracts，然后读 out artifacts 生成 _test/generated.ts；当前 out 不存在，CI 要求1.8.5，本机0.2.0。许可仍 pending，未安装/未重写 import/未提供替代 fixture。

本轮下一步先读 batch-10.json、summary 和最终提交。原版 database/actions 的 commitBlock/isolated 仍需显式覆盖 MAX checkpoint 暂态、对应 chain commit 和所有 journal 断言，不能用现有 Apply 的最终结果冒充；empty schema finalize 原例允许更新 finalized/safe 而保留 latest，当前 native Finalize 仍要求 indexed progress，此处尚未实现。live query 大 payload、完整 source/trace/multichain/GraphQL/CLI/typed容器/全部其余测试仍按 feature ledger 继续。

M12 最终完整 verify 退出0：Go/race各360/360，PG81/PGlite80分别全通过；source222（Go218+SDK4），Go oracle167通过、51阻塞，683未实现。ABI源类型错误1+ORM5共6条natively verified，额外ABI53/ORM6负向控制分开。原profiles/fixture oracle限制不变。无正在等待的测试；先核对最后提交及batch-10.json，再推进剩余database actions。

## M13：staged actions 与真实 durable checkpoint 边界

新增 store/actions.go。NewActions 使用与原子 Store.New/Apply 分离的 fingerprint/mode；互相打开同一 namespace 会拒绝。RawBatch 业务+journal 同事务提交，checkpoint 为75位MAX sentinel；外部 Actions.CommitBlock 是独立提交，按表（isolated还按chain）赋值；AdvanceLatest 又是独立metadata步骤。Writer.CommitBlock/AdvanceLatest可置于RawBatch内，共享同一事务；赋latest后再出现pending写会拒绝提交。public Actions 方法开自己的事务，不能从RawBatch callback嵌套调用。

Journal 升级 json-v3，增加持久 row_data 和 operation：insert存NEW，update/delete存OLD；before_row仍为无损json。Actions.Journal投影读取真实历史记录全部列+operation/id/checkpoint，不从当前业务表伪造。全局journal不是源每表_reorg布局，不声称旧数据库原位兼容。默认新namespace为ponder_go_shadow_v3_<hash>/ponder_go_actions_v3_<hash>，旧v2显式namespace拒绝、不做原地ALTER。

新增6条源映射：createTriggers、duplicate、commitBlock、isolated、empty schema、empty-schema matching-chain finalize。原 finalize/revert/composite 3映射改为完整staged原fixture，保留旧Apply测试作补充，不重复credit。9条审计记录共10条原runtime断言在parity/staged-actions-source-cases.json；actions.test.ts除large notify外10/11已映射。createIndexes仍保留此前真实数据库原例。

Empty-schema finalize只更新已有目标链safe/finalized，不碰latest或其他链；无metadata仍按源成功。非空staged finalization要求初始化链metadata，防止无state时清除undo。pending检查在同一namespace事务锁内进行，isolated只检查目标链，默认ordering的global operation suffix回滚保留。Create/Drop capture之外有永久BEFORE guard：drop后不允许public无journal业务写，只有controlled undo可用reverting context。

CrashRecovery按源primitive撤销全部保留journal（assigned和MAX），不改checkpoint metadata；runtime随后须AdvanceLatest(safe)。不能把它说成仅撤销MAX，也不能冒称自动runtime已接入。14个新增进程exit叶子覆盖两后端：raw事务commit前、同事务赋cp/latest后commit前、raw durable、block assigned、latest、finalized、undo中断；另6个后端native guard/global suffix/isolated用例。进程退出不是PG服务端/宿主断电。完整边界说明见docs/DATABASE_ACTIONS.md。

下一步：先核对batch-11.json、summary和最后提交。继续剩余live query大payload原例（真实LISTEN/NOTIFY、160长表名、UTF-8 payload<8000），再database/index migration/cache/crash源例与staged scheduler自动集成。Current runtime仍是atomic Apply路径，迁移/重启需explicit staged CrashRecovery+latest复位，尚未统一接入。全部source/filter/fragments/raw事件/trace/多链/GraphQL/CLI与剩余类型/SDK/simulation范围仍在feature ledger。Foundry1.8.5仍pending且未安装；DB oracle不算通过。

M13最终verify退出0：Go/race各398/398，PG100/100、PGlite99/99；source228（Go224+SDK4），Go oracle167通过、57阻塞，677未实现。M13未改变类型/differential计数。结束时无挂起测试，先读最后提交、batch-11.json及DATABASE_ACTIONS.md，再继续通知/migration/source/trace等范围。

## M17：生命周期剩余源例、ORM then/SQL、规范化缓存事件接入

本批4条源记录14个原断言、1个原SQL编译错误义务；原输入/fixture/动作顺序与适配在 `parity/lifecycle-orm-completion-source-cases.json`。PG4/PGlite3源后端叶子（SQL原例在PGlite fixture前返回）。累计249/905，Go245通过其中oracle167通过/78阻塞，SDK4，656未实现。Go/race531/531、PG157/PGlite154，零skip/fail；编译ORM6源错误/6额外控制按预期，ABI53运行/类型不变；原版core167活动通过/2源skip、SDK4和2466旧差分通过。新增20个protocol编码向量由未改动source encode/lowercase执行产生，20Go/race通过且不加源credit。完整数据库/ORM oracle再次实际尝试为0测试/2收集失败suite，缺generated.js，Foundry仍pending。

HistoricalBatch仍逐写journal，初始safe metadata与安全前缀清理同事务；默认全局后缀拒绝越过未知链，isolated分链。双后端metadata失败、四个真实进程退出、别名schema归属、通知与重开均通过。typed SQL走已有raw/savepoint/trigger/cache边界，正反编译fixture均实际build。

Store.EnableProtocolCache + runtime默认路径已接入完整RPC block/transactions/logs/receipt，区间/factory镜像与attached-cache重组删除同事务；PG/PGlite真实已有Anvil分别验证写入、重放、fork与旧日志删除。原native factory简略RPC fixture明确使用DisableCache，未冒充完整RPC oracle。source完整CRUD fixtures、过滤分页、source MD5请求哈希、自动trace、冷disable/re-enable失效、自动staged runtime/多链仍缺。详见PROTOCOL_CACHE.md。

M17当时记录的冷reopen禁用缓存后rewind再启用缺口已由下方M18修复并验证。继续把Lifecycle/Actions真实接入staged事件调度与重组/通知，保持MAX暂态、逐事件业务/journal和最终latest原子性，接续source/trace/多链及查询工具链。

## M18：冷缓存重组与跨链 factory 镜像

在M17发布提交3c35307之后，新增8个原生回归叶子，无新增源credit。真实disk PGlite/PG连接关闭重开复现了未attach缓存旧分支残留、cold DELETE未参与undo事务，以及同native factory ID/address跨链镜像冲突。修复为rewind事务内只发现已归属/完整schema，不启用缓存、不创建schema；镜像factory身份显式含chain ID。测试还验证closed adapter、另一链保留、真实DELETE触发器失败全回滚、重启后重新启用/重放、不存在schema保持不存在和拒绝未归属schema。详见batch-16.json与protocol-cold-red/fixed-race日志。

下一项以自动staged runtime为先：Engine.Store当前只接受atomic Store.Apply，而Lifecycle/Actions的原版granular MAX/commitBlock/latest路径尚未接入Engine。接入前须决定并验证启动migrate返回safe与持久latest的协调、历史到实时切换、handler逐事务journal、MAX暂态进程退出、commitBlock+latest/通知边界、revert/finalize后的事件去重与cache失效。当前source原例cache/history/runtime/filter/trace仍未移植，不可拿原生Anvil回归代替。

M18完整verify退出0：Go/race539/539；PG161/PGlite158均通过，无skip/fail。vet/build、ABI53编译/运行/race、ORM6源错误+6额外控制、core167活动/2原skip、SDK4和2466差分均通过。源映射249，原版Go oracle167通过/78阻塞，SDK4，656未实现。batch-16-evidence冻结16份日志/hash，batch-15-evidence保留M17原始结果。当前下一实现入口仍为自动staged runtime/重组通知；保留M17发布SHA和所有历史身份修复备份。

M17已发布3c35307eab6eb8ec874bb5551cab88cefb77a465，M18已发布803995d04df55eab905b524c191ee095d14896cc；均核验private owner、remote SHA/tree和作者/提交者。发布证明见parity/runs/milestone-m17-m18-publication.json。本次后续文档提交只更新摘要与证明，不改变已验证代码。接续先git status/log确认最新head，不重复启动任务容器。

## M19：自动 single-chain staged runtime

新增 runtime.NewStaged/StagedOptions/Phase，store/runtime_actions.go 接入未迁移Lifecycle：setup与初始状态同事务、historical逐块safe/finalized、live整块MAX/commitBlock/latest事务、handoff索引/触发器/views/ready、自动finality。公开latest在reorg/recovery期间保持原值，私有_ponder_go_runtime_cursor用于durable resume，防止替换块失败后的跳过/重复；checkpoint通知在replacement commit后发。atomic构造器仍保留。

真实PG/PGlite+现有Anvil覆盖cache开/关、handler/metadata失败、提交确认失败、backfill中链头前进、实际LISTEN/NOTIFY去重/断开重连、六个真实进程退出、setup失败回滚、公开/私有cursor分离和empty schema。子进程退出后默认租约25秒等待属于测试的一部分。NOTIFY非durable队列，重连读取当前数据后接后续通知；完整SSE恢复尚未完成。query.Server.Ready仍需应用显式接线，DB app ready不等于HTTPReady自动完成。

handler内缓存RPC原先会嵌套获取store/PGlite事务锁。现改为EventProtocolCache复用事件事务，RPC Client.WithCache共享root请求ID、重试、全局限流；真实每块hash RPC miss与回滚已验证。未知block request metadata仍NULL并按链保守失效，不能冒称source MD5/参数解析协议完成。

新增一个确切源映射 runtime/events.test.ts splitEvents()，保留全部双事件输入与完整75位inline snapshot，Go事件字段使用原生RPC hex；原源码/contexts/断言在parity/runtime-source-cases.json。其余本轮native故障/进程/端到端/客户端测试不增加源credit。原版events suite实际执行结果0测试/1收集失败suite，缺generated.js，Foundry仍pending未安装。

最终证据入口为batch-17.json，最终完整验收退出0：Go/race570/570，PG175/PGlite172，零skip/fail；vet/build、编译、适用oracle/SDK/differential均通过。不要把staged-before-cursor-finalization中的中间版本日志当最终结果。继续时先读最终summary及HEAD。下一批按依赖推进原版filter/fragments/events及source/trace，随后多链完整调度和查询/工具链。当前NewStaged明确仅单链，historical为native逐块事务而非source范围批处理；concurrent fetch/profile/prefetch/metrics、全部Foundry源例仍未覆盖。

M19冻结batch-17-evidence共24份日志/源码审计快照及hash，implementation字段记录9个实现文件hash。唯一新增源credit是splitEvents()完整快照；本批30额外native叶子（含6真实进程退出）不加源credit。当前下一阶段继续原版runtime filter/fragments/events与source/trace，并推进多链、完整缓存查询和API/CLI，不能以570 native通过代替655未实现源码例。自动HTTPReady、完整SSE断线恢复仍没有接线，不外推LISTEN重连测试。


## M20：原版 filter/fragments 精确映射与日志路径接入

累计282/905映射（278 Go全部通过，其中185原版oracle通过、93阻塞；SDK4通过），623静态记录未实现。新增32源记录=18fragments+14filter，38静态断言表达式实际75次断言；source loops/snapshots/fixtures/context完整保留在parity/filter-fragments-source-cases.json与testdata/runtime。全部源fragments未改动Vitest18/18通过；完整filter oracle实际尝试0测试/1收集失败suite，仍缺generated.js，Foundry安装pending未执行。不能把867个补充向量或8个native后端叶子算源例。

filter包有ID/adjacency/recovery、地址/topic/factory/区间谓词、80% overlap range合并、50地址block请求放宽和include unions。runtime已用predicate再次过滤宽RPC结果，显式空地址不请求，动态child按持久发现区块核对，canonical hash在规范化缓存前验证。真实EVM+PG/PGlite的cache开/关验证宽请求多余日志、Created与Transfer同交易、发现前排除、实际fork替换及旧缓存清理。原历史完整coalesced planner、完整source config filter DSL和typed事件projection未完成，factory仍先discover再fetch child分离请求。

最终完整verify退出0：Go/race1477/1477，其中867额外filter差分向量各自通过；PG179/PGlite176，零skip/fail。vet/build、53ABI运行/race/编译和既有ORM类型正反例、core185活动/2原skip、SDK4、2466旧差分及20协议向量均通过。first差分红灯保留，修正undefined topic恢复与额外非法dynamic对象的JS coercion；wide first红灯是fixture无等待receipt，已改为有期限实际挖矿等待。当前完整证据/hash见batch-18.json。历史batch17证据未改动。

下一可执行批次：先核验最新HEAD/summary与无挂起命令。原filter中的transaction/from、null transaction.to和transfer源例可沿_test/simulate.ts:transferEth与_test/utils.ts:getAccountsIndexingBuild检查真实ETH转账/trace fixture，不依赖forge生成合约ABI，保留原全部输入/断言/loop。其余range-selector/contract trace继续保留generated ABI阻塞。随后实现events.ts的sync-to-internal转换与buildEvents（receipt按transaction index、matched trace才增traceIndex、stable checkpoint排序/zkSync缺transaction），不以现有log路径代替。再接account/trace runtime、多链和查询/工具链。整体未完成，不将282映射当全量。


## M21：account/transfer运行时、规范化events和call-tree源例

本批6源记录12原断言：events的block/transfer/transaction解码与receipt索引/对象身份/两条缺失异常，以及rpc.actions的2个call-tree路径/失败子树。累计288/905（284Go通过，Go源码oracle191通过+93阻塞，SDK4）；617静态记录未实现。core原样201活动/2源skip，其中完整rpc/actions16/16通过但只2条已移植；选择性events4/4使用未改动声明/实际source Common/helper依赖，模式单列，不冒充整份events suite成功收集。声明、上下文、来源hash、endpoint适配和logger序列化opaque说明在parity/selected-runtime-declarations.json、parity/event-construction-source-cases.json。原full events依然缺generated.js。

新events包：BuildEvents完整block/transaction/log/trace/transfer过滤、receipt按(block,index)关联、原对象保留、缺失异常、factory birth、zkSync缺transaction、matched trace才增index并按transaction重置、稳定75位排序；五个RPC→internal转换保留大整数/nullable/各tx类型。DecodePublicEvents仅支持block/account，contract分支明确报未实现，未吞掉异常或冒充ABI完整解码。115源码生成额外向量全部通过，不加sourcecredit。

新config.AccountSource和runtime/accounts.go接staged/atomic单链handlers Name:transaction:{from,to}/Name:transfer:{from,to}。外层Block/Transaction/Receipt继续原生rawRPC API；Normalized提供source-shaped数据，Trace/Transfer额外字段。rpc.TraceBlock首选原blockHash方法，只有-32601才显式fallback每tx callTracer；TraceBlockByNumber不用不可变cache。DFS重建sibling位置并过滤失败父子树。真实已有Anvil0.2能力实测：两个block trace方法均缺，transaction tracer可用；三条原ETH filter/helper在断言前MethodNotFound失败，未映射/未伪造fixture/未替换method来换通过。Foundry仍pending未安装。见anvil-trace-capabilities、selected-with-eth-oracle报告。

真实PG/PGlite/cache开关4个新增native叶子验证zero、root/child revert、嵌套CALL、DELEGATECALL/CALLCODE、receipt/checkpoint/matched trace顺序、handler全块回滚、实际fork、磁盘/连接cold reopen后反向cache设置和旧9→新11trace失效。它们不是源原例。首轮两项失败仅为测试把Rows的JSON envelope当平铺列，已修正并保留red日志；后续cold/full全部通过。没有新增服务端/宿主断电保证。

最终完整verify退出0：Go/race1602/1602，PG181/PGlite178，零skip/fail；vet/build、ABI/ORM既有编译正反例、SDK、core201活动/2源skip、选择性源码4、2466基础/factory+867filter+115events+20协议向量独立通过。batch-19.json冻结证据。执行服务中断曾发生在采集器修正前，恢复后先核实无遗留进程和未落地命令再继续；当前运行结束，不重复旧命令。

下一批：完整contract decodeEvents（source日志与trace失败统计/日志去重、w/o output、native ABI args/result）和contract trace source接入，继续rpc.actions其余14已取得oracle但未移植源例；完整source event转换/筛选原例、factory account/trace、多链与query/CLI。原ETH三条fixture的blockHash方法仍真实阻塞，不把native fallback算一比一通过。原schema/full filtering DSL、完整include/projection/history/planner/profile/metrics、GraphQL/API/SSE/CLI/templates/dev、严格源码兼容和旧DB接管继续在feature ledger内。保留所有作者修复备份，仅正常快进发布经验证阶段。
