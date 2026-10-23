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

1. 审阅 `schema/schema.go`、`abi/abi.go` 的现有子集，对照 `build/schema.test.ts`、`decodeAbiParameters.test.ts`、`decodeEventLog.test.ts` 逐例移植。不能只补测试标题：保留同一 setup、输入、每条断言、异常消息、skip/todo，并在 `parity/mappings.json` 登记准确目标。本批 24 条 schema、19 条 event-log 已逐例通过。ABI 参数解码算法已移植，但 decodeAbiParameters.test.ts 的 runtime 与内联类型断言尚未完成映射；先补这些测试，不能把已实现函数等同于测试通过。
2. 将上游 finality 的 lowerBound、blockTime 初始猜测、时间戳相等边界逐例移植。本批 6 条 finality 原测试已完成映射并通过，Engine 已委托相同算法；自动 finalize 调度与数据库 actions 原测试仍待接入。
3. 扩展 runtime 到账户交易、原生转账、trace、factory 持久动态地址与 receipt 语义，再逐项补 sync-store 原始事件持久化、过滤片段、历史缺口与实时故障恢复。
4. 加入跨链事件调度，验证 omnichain/multichain/experimental_isolated 的完整处理顺序。当前 `runtime.New` 对多链配置明确报未实现；存储层已经支持并验证全局后缀和 isolated 回滚。
5. 逐例补齐 PG CRUD 细节、函数默认值、raw SQL journal、关系、enum、view，再实现 GraphQL/完整 SQL 类型协议、所有 CLI/模板/热重载与观测。
6. Go 类型成功/失败 fixture 仍待建立；73 条源类型测试中的 React 1 条由保留 SDK 执行，其余原生类型测试不可用普通 runtime 测试替代。Simulation 仍缺本地可复用的完整数据集和种子验证。

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
