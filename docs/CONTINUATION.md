# 继续工作

任务没有完成。先读 `AGENTS.md`、本文件、`parity/runs/summary.json`、`parity/test-ledger.json` 和 `parity/feature-ledger.json`。不要把新增集成测试或差分向量算成上游逐例移植。

## 已保留的状态

- 工作目录 `/Users/ysh0566/Documents/Codex/2026-10-23/task/ponder-go`。
- 完整上游 `/Users/ysh0566/Documents/Codex/2026-10-23/task/ponder-upstream`，固定 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`。
- 已创建本地 Go Git 仓库；不添加远端，不推送、开 PR 或部署。
- 已启动现有 Docker Desktop，并创建 `ponder-go-task-20261023` / `ponder_go_task_20261023`。先 `docker inspect` 核对存在和 `127.0.0.1:53398`，直接复用；不要操作其他容器。
- Go 缓存位于 `.toolcache/`，基线 npm 依赖已按冻结 lockfile 安装。所有 SDK 源码原样保留在 `sdk/typescript/`。
- 计划 PDF `libfile_90c2e75070c481918ecace631e841e0f` 下载失败，没有本地副本；完整需求已经在初始委托中给出，无需等待它。

## 下一可执行批次

1. 从最新提交和 summary.json 恢复，保留已通过 176 条映射（172 Go + 4 SDK）；其中 165 Go 原版 oracle 通过，7 DB oracle 阻塞。不要重做已完成的 ABI 参数 69、event 19、schema 24、finality 6、factory 6、offset 7。
2. 继续 indexing-store/index.test.ts 的 find/insert/update 返回值、缓存副本、mutable values、missing rows、default/$default/$onUpdateFn 等原例。当前 Writer.Write 只返回 error，不能把现有简化 CRUD 当成完整 ORM；尤其 find 源例的 clear/invalidate 后可见性不同。actions 的 commitBlock、trigger 原内部布局、空 schema finalization 和通知大 payload 原例仍待完成。
3. runtime 已有 factory 动态地址单链路径（runtime/factory.go + store/sync.go），继续源 runtime/filter/fragments、sync-store 原事件和 factory 原 E2E；交易、原生转账、trace、完整 receipt/source 语义仍缺。66 个 factory 差分向量和双后端追加集成测试不能代替原 E2E。
4. 加入跨链事件调度，覆盖 omnichain/multichain/experimental_isolated 顺序。runtime.New 仍明确拒绝多链；仅存储层全局后缀/isolated 回滚已验证。自动 finalize 和无表 finalization 边界也待实现。
5. 完整关系/查询/GraphQL、SQL/SuperJSON 全类型、全部 CLI/模板/热重载/观测/示例，以及严格源码兼容和旧 DB 接管的独立轨，仍按 feature-ledger 推进。默认新影子 schema，不访问业务数据。
6. ABI 原生类型生成和 53 个内联成功 fixture 已建立，另有 53 额外负向控制、1 个源 null-topic 错误 fixture；原两份 ABI 文件 tsc 通过。剩余 10 个内联类型断言、72 条专门 native 类型记录及绝大部分类型错误指令尚待移植。Simulation 的 8 个应用、无限 seed 空间尚未执行。


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
