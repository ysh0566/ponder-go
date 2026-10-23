# Ponder-Go

固定 `ponder-sh/ponder@17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`（核心 `0.18.2`）的本地 Go 复刻，**仍在实现，不是全功能完成版**。

项目：`/Users/ysh0566/Documents/Codex/2026-10-23/task/ponder-go`。完整 Git 基线保留于相邻的 `../ponder-upstream`，处于固定提交的 detached HEAD。此项目没有远端、推送、PR 或部署。

目前已有可运行的单链路径：本地 Ethereum RPC → 静态或 factory 动态合约日志/receipt → Go handler → PostgreSQL 事务写入、journal、checkpoint → SQL HTTP/SSE。Anvil 重组后可以撤销并重新索引。PostgreSQL 与真实 PGlite 分别验证；没有 SQLite 替代实现。

## 当前验证与计数

权威结果是 [parity/runs/summary.json](parity/runs/summary.json)。用例发现、移植、执行、通过分别登记，不合并计数。

- 104 个原测试文件、897 个测试声明，905 条静态展开记录；包含 73 条专门类型测试、63 个运行时文件内联类型断言、84 条类型错误指令，以及客户端/React/E2E。
- 已逐例映射 176 条：172 条 Go + 原样保留的 4 条 SDK。Go 映射 172 条均通过，其中 165 条原版 oracle 也通过、7 条数据库 oracle 阻塞；SDK 4 条通过。另有 **729 条未实现**。
- 全套原生 Go/race 各 238/238；53 条生成 ABI 解码器的伴随 runtime/race 另计。53 条源内联类型要求的成功 fixture 全通过，53 条额外负向控制及 1 条源 null-topic 错误要求均按预期编译失败；两份原 ABI 文件 tsc 通过。
- 新增 Go 存储、SQL/SSE、RPC、Anvil 和崩溃测试独立计数；不冒充原版测试移植。
- 2,466 个确定性差分向量（基础 2,400 + factory 66）独立计数。
- 原版 10 个 skip、2 个 todo 的状态保留，不计作本移植通过。

静态发现不能证明任意 JavaScript 动态执行的全部实例均已收集。4 个 literal `test.each` 声明已展开；本基线未发现动态测试生成、别名测试遗漏、`describe.each` 或 `only`。Simulation 的 8 个应用和无限 seed 空间单列。详见 [discovery-audit.json](parity/discovery-audit.json)，完整测试源码和 247 个依赖/fixture 模块分别保存在 `parity/source-tests/`、`parity/source-support/`。早期 1,957 个断言表达式计数重复计算了链式 `expect().rejects`，已修正为 1,905；905 条测试记录不变。

## 验证

当前工具：Go 1.27.1、Node 25.2.1、pnpm 12.10.1（工作区 corepack）、Vitest 5.0.2、PGlite 0.2.13、PostgreSQL 16。已安装的 Anvil 为 0.2.0，不符合上游全套测试要求的 Foundry 1.8.5，不能据此声称整套 oracle 已通过。

本任务 Docker 容器 `ponder-go-task-20261023`，卷 `ponder_go_task_20261023`，监听 `127.0.0.1:53398`，数据库 `ponder_test`。账户/密码 `ponder` / `ponder-local-test` 仅用于此隔离测试库。未操作已有业务容器或业务库。后续先 inspect 现有任务容器，避免重复创建。

```sh
export PONDER_TEST_POSTGRES='postgres://ponder:ponder-local-test@127.0.0.1:53398/ponder_test?sslmode=disable'
./tools/verify.sh
```

该脚本执行 `go test -count=1 -json ./...`、`go test -count=1 -race -json ./...`、`go vet ./...`、CLI/示例编译、原版 83 条所选 core 测试及差分 harness。本环境的沙箱限制本地 TCP/IPC，执行数据库、HTTP 和 Anvil 测试需要已授权的工具网络能力。缺少 PostgreSQL 环境变量的单独 `go test` 会明确报告相关集成测试未运行；完整验收脚本直接要求该变量。

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

- 默认创建新的 `ponder_go_shadow_<schema hash>`，索引 schema 与 sync schema 分离；拒绝接管没有本项目 ownership marker 的 schema。
- 业务 DML 与 undo journal 在同一数据库语句的 trigger 内完成；事件去重与 checkpoint 进度在同一事务内提交。默认 multichain 回滚从目标链边界后的最小 operation_id 撤销全局后缀；isolated 只回滚目标链。
- 四个应用进程边界（业务 SQL 后、确认 journal 后、进度后、commit 后）在两个后端分别测试；前两个现已位于同一业务/trigger SQL 语句完成之后。额外 journal 约束失败验证语句内回滚。没有测试数据库服务端断电或磁盘损坏，不外推这些保证。
- `reorgWindow` 按时间戳秒计算。调度器目前明确限制单链；多链调度尚未实现，存储层的多链回滚已经有真实验证。
- 当前原生 schema/ABI 已补齐 build/schema、event-log 和 finality 所选源测试，并在两个后端执行枚举及视图；业务 raw SQL 已统一经过数据库 trigger journal 并验证 savepoint 错误恢复；完整参数/类型测试、关系、factory、trace、GraphQL 等尚待实现。

Go 功能等价与任意 TS/Hono/Drizzle 源码零修改执行、旧 DB 原地接管，是不同工作轨道。后两项均未实现，也未进行数据迁移。当前模型 hash 不包含 handler/config 内容，修改这些内容时必须选择新的影子 schema；自动开发重建尚待实现。

## 来源与许可

保留原 MIT [LICENSE](LICENSE)、SDK 各自许可证、实际 Go 依赖许可证清单 [licenses/manifest.json](licenses/manifest.json)。`third_party/pg_query_go` 固定为 v5.1.0，保留原许可证；仅将本地 `strchrnul` fallback 重命名，解决 macOS SDK 15.4+ 同名声明冲突，未改变 SQL 解析规则。`sqlquery/rules.json` 从固定源码的 83 个允许节点、62 个函数与 264 个系统表限制生成。

未取得计划 PDF 的本地可读副本：Library 已解析给定文件，但所提供下载经指定 helper 两次失败；未伪造本地文件。实现以完整委托范围和固定源码为依据。

当前 89 条映射中，87 条也取得原版 oracle 通过，2 条 SQL 用例已在 Go 双后端执行，其完整原版 setup 因待批准的 Foundry 1.8.5 安装仍阻塞。见台账 `oracle_passed` / `oracle_blocked`，不将 Go 通过冒充 oracle 通过。

## ABI 原生类型适配

`ponder codegen --abi-parameters params.json --decoder Transfer --package main --out transfer_generated.go` 从 ABI 参数生成有返回类型的解码器；位置 tuple 为 `Value0` 等字段，命名 tuple 保留 `abi` 字段标签，固定数组保留长度，较大整数为 `*big.Int`。Go 没有 TypeScript readonly tuple 或 Hex 模板字符串类型；这些差异在 `parity/runs/compile-fixtures.json` 注明。53 条编译 fixture 的预期类型独立来自原断言，未从 Go 签名反向生成。

动态地址的参数/offset 构建、发现持久化、失败重试、磁盘重开和重组回收已有双后端验证，见 [docs/FACTORY.md](docs/FACTORY.md)。完整 factory E2E、trace/account sources 与多链 runtime 仍未完成。
