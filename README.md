# Ponder-Go

固定 `ponder-sh/ponder@17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`（核心 `0.18.2`）的本地 Go 复刻，**仍在实现，不是全功能完成版**。

完整 Git 基线保留于相邻的 `../ponder-upstream`，处于固定提交的 detached HEAD。既定私有仓库为 [ysh0566/ponder-go](https://github.com/ysh0566/ponder-go)。云端 M23–M33 已保留原 SHA 依次普通快进发布，上一已核验发布基线为 M35 `33761c84666733890c7176c75fe3756ded40998f`，后续发布以 Git 远端和验收回执为准；[M23–M25证明](parity/runs/account-factory-m26/publication-results.json) 、[M26证明](parity/runs/bloom-m27/m26-publication.json) 和 [M28证明](parity/runs/config-types-m29/m28-publication.json) 及 [M29证明](parity/runs/sql-m30/m29-publication.json) 及 [M30证明](parity/runs/topics-m31/m30-publication.json) 及 [M31证明](parity/runs/sync-store-m32/m31-publication.json) 及 [M32证明](parity/runs/history-m33/m32-publication.json) 及 [M33证明](parity/runs/realtime-m34/m33-publication.json) 保存 SHA/tree/身份。没有部署或 PR。

目前已有可运行的单链路径：本地 Ethereum RPC → 静态或 factory 动态合约日志/receipt → Go handler → PostgreSQL 事务写入、journal、checkpoint → SQL HTTP/SSE。Anvil 重组后可以撤销并重新索引。默认单链 runtime 已写入版本化规范化缓存的 block/transaction/log/receipt；冷重开禁用缓存的重组也清理已归属缓存。PostgreSQL 与真实 PGlite 分别验证；没有 SQLite 替代实现。

## 当前验证与计数

最新本地已验证基线为 M36（待发布复核）：[验收记录](parity/runs/batch-35.json)、[冻结证据](parity/runs/batch-35-evidence/) 和 [summary.json](parity/runs/summary.json)。配置全部39个活动原例/92断言及全部10项原生类型错误义务通过，原始skip保留未映射。发现、移植、执行、通过分别登记；补充验证不加source credit。

- 104 个原测试文件、897 个声明、905 条静态展开记录；包含73条专门类型测试、63个内联类型断言和84条类型错误指令。
- 已精确映射并通过508条：504条Go与4条保留SDK，当前原版oracle均通过；397条静态记录未实现。
- M36 Go/race各2634叶用例（2863节点）通过、0测试失败/skip；PG386/PGlite383分别通过。22包、187顶层函数、每模式61个不重复串行批次，Go默认timeout保留。完整28阶段使用明确启动适配，不是pristine；历史本地生成器新增11原例，完整多链调度仍未完成。
- ABI53条内联类型义务及53个额外负控制、ORM6条原错误义务及6个额外负控制、callback union1条及bloom2条原错误义务分别通过真实编译检查。更广的源码类型兼容仍在范围内。
- events11/11与filter23/23已映射。完整原文件保留真实生成ABI/helper和未改断言，通过call-through采集后由Go重放；采集与pristine原版验收分开标注。此前八文件PG/PGlite原版验收各129通过、3个原skip，未冒称全部上游验收。
- 补充差分2466基础/factory、867filter/fragments、135events、20协议编码、62RPC、497bloom分别计数。额外Go集成/故障/进程测试不加原例credit。
- 原版10个skip、2个todo状态保留，不计作移植通过。

静态发现不能证明任意 JavaScript 动态执行的全部实例均已收集。4 个 literal `test.each` 声明已展开；本基线未发现动态测试生成、别名测试遗漏、`describe.each` 或 `only`。Simulation 的 8 个应用和无限 seed 空间单列。详见 [discovery-audit.json](parity/discovery-audit.json)，完整测试源码和 247 个依赖/fixture 模块分别保存在 `parity/source-tests/`、`parity/source-support/`。早期 1,957 个断言表达式计数重复计算了链式 `expect().rejects`，已修正为 1,905；905 条测试记录不变。

## 验证

当前云端工具为Go1.25.0、Node24.20.0、pnpm12.10.1、Vitest5.0.2、Foundry/Anvil1.8.5、PG14.24和真实PGlite0.2.13，实际版本记录在 `parity/runs/tool-versions.txt`。当前专用PG仅监听127.0.0.1:55432，数据在任务云目录；此前Mac Docker/Anvil0.2环境为冻结历史。只复用任务数据库，不能连接业务库。

```sh
source /workspace/scratch/fac5abfa4c9e/ponder-cloud/env.sh
export PATH="$(realpath ../ponder-upstream)/node_modules/.bin:$PATH"
# 新目录名必需；服务与客户端在同一 exec，tsc 固定为 5.4.5。
bash /workspace/scratch/fac5abfa4c9e/ponder-cloud/with-postgres.sh \
  node tools/runtime-historical-full-verify.mjs \
  --output=parity/runs/runtime-historical-m36/full-verify-NEW
```

脚本先生成原例/差分fixture和ABI/ORM/callback编译证明，再列出全部Go包及顶层测试，按每组4函数串行执行normal/race，保存完整执行计划和前后源码指纹。此分组适应云端内存，原测试体及Go默认timeout不改。随后执行vet、CLI/示例构建、原版core201活动用例/2原skip、选择性4+15声明、完整events/filter采集、参数类型、SDK及差分。云端并行工作须用任务 `flock` 串行化重测试。缺PG变量时单独Go测试会标记相关集成未运行；完整脚本直接拒绝缺失变量。

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

当前486条精确映射均有源码 oracle 通过；此前数据库/profile所需 Foundry 与生成 fixture 已按授权准备，八文件PG/PGlite验收结果和完整events/filter采集分别保留。419条静态记录仍未实现，不能将 Go 通过外推为全部源码验收。

原生 `NewLifecycle` 已实现新影子启动、build ID 校验、heartbeat 租约、带 fencing 的业务/同步写入和事务恢复；新增8条生命周期源例及真实进程退出验证。恢复返回 safe checkpoint 并保留 latest，NewStaged 自动协调私有恢复游标及安装 capture。自动单链 staged runtime 已接入生命周期和实时事务；完整多链调度、真实旧版接管、dev reset 和角色权限仍待实现。接口与边界见 [DATABASE_LIFECYCLE.md](docs/DATABASE_LIFECYCLE.md)。

`NewSyncMigrator` / `Lifecycle.MigrateSync` 已逐例实现8条版本化 sync 迁移测试：在任务拥有的 shadow 内创建完整9表协议缓存、以repeatable-read复制允许数据并保留旧shadow/history、并发/失败重试及真实进程恢复。原版SQL三份资产完整保留并核对hash；Go整数参数改为十进制传输，修复PGlite对大int64的舍入。默认单链 runtime 已将协议缓存接入事件 CRUD，并验证冷重组和启停缓存；原版同步存储38例已补齐，包含缓存查询/分页；历史范围获取、完整trace调度和多链仍待实现，没有真实旧DB原地接管。见 [SYNC_MIGRATIONS.md](docs/SYNC_MIGRATIONS.md)。

## ABI 原生类型适配

`ponder codegen --abi-parameters params.json --decoder Transfer --package main --out transfer_generated.go` 从 ABI 参数生成有返回类型的解码器；位置 tuple 为 `Value0` 等字段，命名 tuple 保留 `abi` 字段标签，固定数组保留长度，较大整数为 `*big.Int`。Go 没有 TypeScript readonly tuple 或 Hex 模板字符串类型；这些差异在 `parity/runs/compile-fixtures.json` 注明。53 条编译 fixture 的预期类型独立来自原断言，未从 Go 签名反向生成。

动态地址的参数/offset 构建、发现持久化、失败重试、磁盘重开和重组回收已有双后端验证，见 [docs/FACTORY.md](docs/FACTORY.md)。M25单链contract trace已接入统一Decoder；M26 factory account已通过完整验收，边界见 [FACTORY_ACCOUNTS.md](docs/FACTORY_ACCOUNTS.md)。完整原版factory E2E、source配置/planner与多链runtime仍未完成。

原子 rewind 与未完成 block 的恢复也已验证：业务 undo、进度及同步区间/factory 地址一起提交；handler 提交前先保留 block header，支持提交确认丢失后的重开/换链。两个后端另有 6 条进程退出恢复验证，见 `parity/runs/batch-6.json`。

当前 journal 存储格式为 `json-v3`，默认新建 `ponder_go_shadow_v3_<hash>`；staged Actions 默认 `ponder_go_actions_v3_<hash>`，模式不可混开。显式旧格式 schema 会要求新影子重建，当前没有原地迁移或接管旧数据库。JSON `U+0000` 与 raw SQL 大数字的 undo 保留原值；PGlite bytes 参数采用明确二进制位置标记。

生命周期/ORM then/SQL 的逐例边界见 [LIFECYCLE_COMPLETION.md](docs/LIFECYCLE_COMPLETION.md)，版本化缓存接入及未实现协议见 [PROTOCOL_CACHE.md](docs/PROTOCOL_CACHE.md)。`runtime.NewStaged` 已接入单链自动生命周期、setup、历史/实时交接、重组与通知，边界见 [STAGED_RUNTIME.md](docs/STAGED_RUNTIME.md)。多链、source 完整范围批处理/并发 pipeline、trace 和完整查询/工具链仍待实现；继续入口见 [CONTINUATION.md](docs/CONTINUATION.md)。

M23统一合约日志/trace批量Decoder，并恢复原八文件PG/PGlite oracle；M24补齐rpc.actions16/16原例；M25完成events11/11、filter23/23原例和callback union类型义务，修复账户JSON整数精度。实现范围及限制见 [EVENT_SOURCE_COMPLETION.md](docs/EVENT_SOURCE_COMPLETION.md)、[RPC_ACTIONS.md](docs/RPC_ACTIONS.md) 与 [CONTINUATION.md](docs/CONTINUATION.md)。

## 云端继续入口

实际checkout为 `/root/ponder-go/work/ponder-go`，固定上游在相邻 `ponder-upstream`。上一已核验发布为M26提交 `253f4f903a534dcc4f7f745a9a8d0ea3bdcd095d`，原历史与MIT许可证保留。用户明确批准默认repo/read:org/gist后，官方CLI实际登录ysh0566，任务认证目录权限0700/文件0600；通过每命令helper依次普通快进发布M23–M25并核验远端。此前停止与失败探测作为历史保留；当前状态见 [认证证明](parity/runs/account-factory-m26/authentication-success.json) 和 [发布证明](parity/runs/account-factory-m26/publication-results.json)。未修改全局helper或操作Mac。M26及后续工作继续source配置/history planner、多链三种ordering、完整查询/API/CLI/dev/templates/类型/SDK/simulation。整体目标尚未完成。

M27对有效非零同步链bloom预计算并筛选日志请求，保持账户/trace/区块/发现进度。旧自有宽查询场景保留输入/setup并明确将RPC预期3改1；独立真实跨日志假阳性场景保持3次请求且正确本地过滤，固定上游函数独立验证两种场景。完整首轮普通测试1795/4失败未启动race，失败快照与修复后的第二轮完整1804通过均保留。cached缺少logsBloom保持原校验/无进度/补齐重试；uncached缺字段保守抓取，空0x/zero/short/malformed和真实chain143均验证。见[LOG_BLOOM.md](docs/LOG_BLOOM.md)。

M28将来源配置的实际客户端、重载事件、topics、收据、回调和工厂标识接入单链运行时，真实两后端与缓存开关8场景通过。601个链目录描述和17个不透明函数标记不代表移植viem formatter/serializer。Hyperliquid原例检查真实规范对象身份；首轮因该断言缺口主动停止并保留1473项部分结果，第二轮完整验收才是正式结果。见[来源配置边界](docs/SOURCE_CONFIGURATION.md)。

M29新增生产typed配置构造/消费边界及实际链/ABI生成的封闭引用，补齐4个原例/7断言/9类型义务；原配置39活动例现全部映射。九项正向程序在原版存活Anvil上执行；事件控制保留原0x短字节参数及其剩余长度错误。12个真实两后端/cache集成和1个零链回归不加source credit。完整类型DSL/调度及其他541静态记录仍继续。见[类型化配置](docs/TYPED_SOURCE_CONFIGURATION.md)。

M30补齐SQL工具函数14精确原例/19断言/38真实调用，含8条实际Drizzle关系查询SQL。源码双LRU、完整SHA256、UTF16限制、公开错误/只读分类及真实ORM缓存路径通过；双后端旧DB2/ORM1红灯在相同输入下修复为ORM2/更新3/journal3。61源码向量和其他新回归零sourcecredit。见[SQL原例](docs/SQL_SOURCE_HELPERS.md)。

M31修复indexed topic的显式undefined标量wildcard，以及string/bytes的OR数组中null/undefined编码；50个实际源码向量与32个真实EVM/PG/PGlite/cache回归通过，新增精确映射0。旧代码失败与同输入证明保留。见[Indexed topics](docs/INDEXED_TOPICS.md)。

原版同步存储的区间、factory子地址、协议CRUD、事件分页及MD5 RPC缓存新增38条精确映射、63个断言和9项原生类型义务；PG/PGlite与normal/race共四次完整原文件对照，每次95个实际Go调用。`ReadSourcePage` / `DispatchSourceEvents` 已通过真实EVM事务失败、重放和换分支回归；范围获取、区块来源和finality仍由调度器负责。接口与验收边界见 [SOURCE_SYNC_STORE.md](docs/SOURCE_SYNC_STORE.md)。

M33补齐内存与持久化历史范围获取的两个完整原文件：40条精确原例、102断言、12项原生类型错误义务，每后端每模式136个公开调用；实际48次类型正向程序使用各自原例的存活Anvil。100块预取、factory分页/最终drain、RPC状态所有权和原版标准化另有116项补充验证，不加source credit。完整历史/实时/多链调度仍需继续。见 [SOURCE_HISTORICAL.md](docs/SOURCE_HISTORICAL.md)。

M34补齐原版实时获取及内存协调 API 的完整30条原例/203断言。每后端每模式83个实际公开调用、46个原始 provider 请求，独立比较状态、输入修改、Map/Set 顺序及原始对象/frame 身份和警告；消费者实际迭代步骤驱动 Go，3个外部测试准备请求另记。24组源码补充向量不加原例计数。完整 runtime 历史/实时/多链调度继续。见 [SOURCE_REALTIME.md](docs/SOURCE_REALTIME.md)。
