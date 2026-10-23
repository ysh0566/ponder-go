# Ponder-Go

固定 `ponder-sh/ponder@17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`（核心 `0.18.2`）的本地 Go 复刻，**仍在实现，不是全功能完成版**。

完整 Git 基线保留于相邻的 `../ponder-upstream`，处于固定提交的 detached HEAD。经验证的阶段提交发布到私有仓库 [ysh0566/ponder-go](https://github.com/ysh0566/ponder-go)；整体复刻仍未完成，没有部署或 PR。

目前已有可运行的单链路径：本地 Ethereum RPC → 静态或 factory 动态合约日志/receipt → Go handler → PostgreSQL 事务写入、journal、checkpoint → SQL HTTP/SSE。Anvil 重组后可以撤销并重新索引。默认单链 runtime 已写入版本化规范化缓存的 block/transaction/log/receipt；冷重开禁用缓存的重组也清理已归属缓存。PostgreSQL 与真实 PGlite 分别验证；没有 SQLite 替代实现。

## 当前验证与计数

权威结果是 [parity/runs/summary.json](parity/runs/summary.json)，本批冻结结果见 [M17](parity/runs/batch-15.json) 、[M18](parity/runs/batch-16.json) 、[M19](parity/runs/batch-17.json) 、[M20](parity/runs/batch-18.json) 、[M21](parity/runs/batch-19.json) 、[deposit修正](parity/runs/batch-20.json) 与 [M22云端交接](parity/runs/batch-21.json)。用例发现、移植、执行、通过分别登记，不合并计数。

- 104 个原测试文件、897 个测试声明，905 条静态展开记录；包含 73 条专门类型测试、63 个运行时文件内联类型断言、84 条类型错误指令，以及客户端/React/E2E。
- 已逐例映射 292 条：288 条 Go + 原样保留的 4 条 SDK。Go 映射 288 条均通过，其中 210 条原版 oracle 也通过、78 条 fixture oracle 阻塞（数据库 65、profile 13）；SDK 4 条通过。另有 **613 条未实现**。
- 全套原生 Go/race 各 1634/1634（含867个filter/fragments及135个events额外向量）；真实 PostgreSQL 183/183、PGlite 180/180。53 条生成 ABI 解码器的伴随 runtime/race 另计。53 条源内联类型要求的成功 fixture 全通过，53 条额外负向控制及 1 条源 null-topic 错误要求均按预期编译失败；两份原 ABI 文件 tsc 通过。ORM 6 条源类型错误义务及 6 条额外负向控制也分别验证真实编译拒绝。
- 新增 Go 存储、SQL/SSE、RPC、Anvil 和崩溃测试独立计数；不冒充原版测试移植。
- 2,466 个基础/factory差分向量，另有867个filter/fragments、135个events向量及20个协议编码向量，分别计数。
- 原版 10 个 skip、2 个 todo 的状态保留，不计作本移植通过。

静态发现不能证明任意 JavaScript 动态执行的全部实例均已收集。4 个 literal `test.each` 声明已展开；本基线未发现动态测试生成、别名测试遗漏、`describe.each` 或 `only`。Simulation 的 8 个应用和无限 seed 空间单列。详见 [discovery-audit.json](parity/discovery-audit.json)，完整测试源码和 247 个依赖/fixture 模块分别保存在 `parity/source-tests/`、`parity/source-support/`。早期 1,957 个断言表达式计数重复计算了链式 `expect().rejects`，已修正为 1,905；905 条测试记录不变。

## 验证

当前工具：Go 1.27.1、Node 25.2.1、pnpm 12.10.1（工作区 corepack）、Vitest 5.0.2、PGlite 0.2.13、PostgreSQL 16。已安装的 Anvil 为 0.2.0，不符合上游全套测试要求的 Foundry 1.8.5，不能据此声称整套 oracle 已通过。

本任务 Docker 容器 `ponder-go-task-20261023`，卷 `ponder_go_task_20261023`，监听 `127.0.0.1:53398`，数据库 `ponder_test`。账户/密码 `ponder` / `ponder-local-test` 仅用于此隔离测试库。未操作已有业务容器或业务库。后续先 inspect 现有任务容器，避免重复创建。

```sh
export PONDER_TEST_POSTGRES='postgres://ponder:ponder-local-test@127.0.0.1:53398/ponder_test?sslmode=disable'
./tools/verify.sh
```

该脚本执行 `go test -count=1 -json ./...`、`go test -count=1 -race -json ./...`、`go vet ./...`、CLI/示例编译、原版 201 条活动 core 测试（另保留 2 条原 skip）、4条选择性未改动事件源码声明、15条已有filter/genesis/split源码声明及差分 harness。本环境的沙箱限制本地 TCP/IPC，执行数据库、HTTP 和 Anvil 测试需要已授权的工具网络能力。缺少 PostgreSQL 环境变量的单独 `go test` 会明确报告相关集成测试未运行；完整验收脚本直接要求该变量。

SDK 测试单独运行，保留原断言与 inline snapshots：

```sh
(cd sdk/typescript/client && CI=1 ./node_modules/.bin/vitest run --configLoader native)
(cd sdk/typescript/react && CI=1 ./node_modules/.bin/vitest run --typecheck.only --configLoader native)
```

SDK 源码与原版相同；本机的 `node_modules` 为复用基线依赖的忽略链接。重新准备环境时见 [CONTINUATION.md](docs/CONTINUATION.md)。原客户端对 Go 的协议验证在 `query/http_test.go` / `bridge/client-compat.ts`，覆盖大整数、列顺序、status、SSE 初始 hash 与结果去重。React 已通过原类型 fixture；尚未进行 React 浏览器集成验证。

## 使用代码

`config.Config`、`schema.Schema`、`runtime.Handler` 是 Go 原生 API。`examples/erc20/main.go` 展示显式本地数据库、链和合约配置，HTTP 服务仅监听 `127.0.0.1:42069`。它需要显式设置 `PONDER_LOCAL_DATABASE_URL`、`PONDER_LOCAL_RPC_URL` 和 `PONDER_LOCAL_CONTRACT`，不会读取普通 `DATABASE_URL` 自动连接数据库。

```sh
go build -o bin/ponder ./cmd/ponder
bin/ponder --help
bin/ponder codegen --schema examples/erc20/schema.json --out examples/erc20/ponder_generated.go
```

`start` / `serve` 编译并启动原生 Go 应用；`serve` 的语义由应用读取 `PONDER_COMMAND=serve` 实现。示例已实现该分支。当前 `dev`、`db prune`、`db create-views` 明确返回未实现，不伪装成功。完整 CLI 选项、模板与全部配置保留在 `parity/public-manifest.json`，功能状态在 `parity/feature-ledger.json`。

## 存储边界

- 默认创建新的 `ponder_go_shadow_v3_<schema hash>`，索引 schema 与 sync schema 分离；拒绝接管没有本项目 ownership marker 的 schema。
- 业务 DML 与 undo journal 在同一数据库语句的 trigger 内完成；事件去重与 checkpoint 进度在同一事务内提交。默认 multichain 回滚从目标链边界后的最小 operation_id 撤销全局后缀；isolated 只回滚目标链。
- 四个应用进程边界（业务 SQL 后、确认 journal 后、进度后、commit 后）在两个后端分别测试；前两个现已位于同一业务/trigger SQL 语句完成之后。额外 journal 约束失败验证语句内回滚。没有测试数据库服务端断电或磁盘损坏，不外推这些保证。
- `reorgWindow` 按时间戳秒计算。调度器目前明确限制单链；多链调度尚未实现，存储层的多链回滚已经有真实验证。
- 当前原生 schema/ABI 已补齐 build/schema、event-log 和 finality 所选源测试，并在两个后端执行枚举及视图；业务 raw SQL 已统一经过数据库 trigger journal 并验证 savepoint 错误恢复；完整参数/类型测试、关系、factory、trace、GraphQL 等尚待实现。

Go 功能等价与任意 TS/Hono/Drizzle 源码零修改执行、旧 DB 原地接管，是不同工作轨道。后两项均未实现。已实现的 sync migration 仅操作任务所属影子 schema，不代表真实旧库原地接管。当前模型 hash 不包含 handler/config 内容，修改这些内容时必须选择新的影子 schema；自动开发重建尚待实现。

## 来源与许可

保留原 MIT [LICENSE](LICENSE)、SDK 各自许可证、实际 Go 依赖许可证清单 [licenses/manifest.json](licenses/manifest.json)。`third_party/pg_query_go` 固定为 v5.1.0，保留原许可证；仅将本地 `strchrnul` fallback 重命名，解决 macOS SDK 15.4+ 同名声明冲突，未改变 SQL 解析规则。`sqlquery/rules.json` 从固定源码的 83 个允许节点、62 个函数与 264 个系统表限制生成。

未取得计划 PDF 的本地可读副本：Library 已解析给定文件，但所提供下载经指定 helper 两次失败；未伪造本地文件。实现以完整委托范围和固定源码为依据。

当前 292 条映射中，214 条也取得源码 oracle 通过（210 Go + 4 SDK，含4+15条选择性源码声明），78 条数据库/profile oracle 因待批准的 Foundry 1.8.5 及其生成 fixture 仍阻塞。见台账 `oracle_passed` / `oracle_blocked`，不将 Go 通过冒充 oracle 通过。

原生 `NewLifecycle` 已实现新影子启动、build ID 校验、heartbeat 租约、带 fencing 的业务/同步写入和事务恢复；新增8条生命周期源例及真实进程退出验证。恢复返回 safe checkpoint 并保留 latest，NewStaged 自动协调私有恢复游标及安装 capture。自动单链 staged runtime 已接入生命周期和实时事务；完整多链调度、真实旧版接管、dev reset 和角色权限仍待实现。接口与边界见 [DATABASE_LIFECYCLE.md](docs/DATABASE_LIFECYCLE.md)。

`NewSyncMigrator` / `Lifecycle.MigrateSync` 已逐例实现8条版本化 sync 迁移测试：在任务拥有的 shadow 内创建完整9表协议缓存、以repeatable-read复制允许数据并保留旧shadow/history、并发/失败重试及真实进程恢复。原版SQL三份资产完整保留并核对hash；Go整数参数改为十进制传输，修复PGlite对大int64的舍入。默认单链 runtime 已将协议缓存接入事件 CRUD，并验证冷重组和启停缓存；完整缓存查询/分页、trace 和多链仍缺，没有真实旧DB原地接管。见 [SYNC_MIGRATIONS.md](docs/SYNC_MIGRATIONS.md)。

## ABI 原生类型适配

`ponder codegen --abi-parameters params.json --decoder Transfer --package main --out transfer_generated.go` 从 ABI 参数生成有返回类型的解码器；位置 tuple 为 `Value0` 等字段，命名 tuple 保留 `abi` 字段标签，固定数组保留长度，较大整数为 `*big.Int`。Go 没有 TypeScript readonly tuple 或 Hex 模板字符串类型；这些差异在 `parity/runs/compile-fixtures.json` 注明。53 条编译 fixture 的预期类型独立来自原断言，未从 Go 签名反向生成。

动态地址的参数/offset 构建、发现持久化、失败重试、磁盘重开和重组回收已有双后端验证，见 [docs/FACTORY.md](docs/FACTORY.md)。完整 factory E2E、trace/account sources 与多链 runtime 仍未完成。

原子 rewind 与未完成 block 的恢复也已验证：业务 undo、进度及同步区间/factory 地址一起提交；handler 提交前先保留 block header，支持提交确认丢失后的重开/换链。两个后端另有 6 条进程退出恢复验证，见 `parity/runs/batch-6.json`。

当前 journal 存储格式为 `json-v3`，默认新建 `ponder_go_shadow_v3_<hash>`；staged Actions 默认 `ponder_go_actions_v3_<hash>`，模式不可混开。显式旧格式 schema 会要求新影子重建，当前没有原地迁移或接管旧数据库。JSON `U+0000` 与 raw SQL 大数字的 undo 保留原值；PGlite bytes 参数采用明确二进制位置标记。

生命周期/ORM then/SQL 的逐例边界见 [LIFECYCLE_COMPLETION.md](docs/LIFECYCLE_COMPLETION.md)，版本化缓存接入及未实现协议见 [PROTOCOL_CACHE.md](docs/PROTOCOL_CACHE.md)。`runtime.NewStaged` 已接入单链自动生命周期、setup、历史/实时交接、重组与通知，边界见 [STAGED_RUNTIME.md](docs/STAGED_RUNTIME.md)。多链、source 完整范围批处理/并发 pipeline、trace 和完整查询/工具链仍待实现；继续入口见 [CONTINUATION.md](docs/CONTINUATION.md)。

M20已逐例映射18条fragments和14条filter源记录（38个静态断言、75次运行时断言），保持全部输入、循环、快照和setup上下文。单链日志已接入predicate和超过50地址的请求放宽后本地再过滤；真实EVM验证factory同交易发现/调用、发现前排除与重组/cache开关。原版filter完整suite仍收集前阻塞，没有用native结果冒充oracle通过。见 [FILTER_FRAGMENTS.md](docs/FILTER_FRAGMENTS.md)。

M21新增静态/wildcard单链account transaction/transfer handler路径、完整规范化事件构建和五类RPC转换；真实EVM/PG/PGlite验证嵌套call-frame、zero/revert/delegate/callcode排除、receipt/checkpoint排序、handler回滚、重开切换cache和fork替换。现有Anvil仅支持transaction callTracer，Go显式回退不计为原ETH fixture通过；三个源ETH过滤例仍阻塞。contract trace解码/配置与多链仍未完成。见 [ACCOUNT_EVENTS.md](docs/ACCOUNT_EVENTS.md)。

## 云端接续检查点

Mac 本端在 M22 验证后暂停，下一执行端为用户创建的专用云环境。先读 [CONTINUATION.md](docs/CONTINUATION.md) 顶部与 [CLOUD_HANDOFF.md](docs/CLOUD_HANDOFF.md)。不传递凭据、node_modules、二进制或数据库卷；云端用固定源码与锁文件重新准备隔离测试环境。本次没有执行云端启动，也没有安装待批准的 Foundry。

M22 新增合约 ABI function/DecodeEvents 和单链 contract trace handler，实际 EVM 在两后端及缓存开关验证同交易 hash 的替代分支、回滚与错误统计。4条 RPC 源例保留8个原断言，另15条既有映射获得未改动声明的选择性 oracle。135个事件差分与真实EVM边界均属补充验证。完整 generated-ABI contract 原例、旧日志调度器接入统一 decoder、多链和完整查询/工具链尚缺，详见 [CONTRACT_TRACE.md](docs/CONTRACT_TRACE.md)。
