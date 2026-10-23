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

当前 ledger 为 **87 条已映射、执行并通过，818 条未实现**。当前全部 Go 与 race 的确切叶子用例数量、实际版本和原始日志读取 `parity/runs/summary.json`，避免文档与新一轮结果计数漂移。

## 当前批次后的工作

继续 `docs/CONTINUATION.md` 中的 schema/ABI/finality 源测试逐例移植，再推进账户、trace、factory、多链调度、完整数据库与 GraphQL、CLI/模板/热重载、全部测试及 simulation。完整功能范围逐项登记在 `parity/feature-ledger.json`，其中 `verified_subset` 只表示已验证的子集。

没有接管旧 DB、修改其他业务仓库、创建远端、push、PR 或部署。计划 PDF 下载失败；没有声称读过该 PDF。

## M4：schema / event / finality 精确移植

新增 24 条 build/schema、19 条 decodeEventLog（21 条原断言）、6 条 finality，均分别在 Go 与未修改的原测试通过。事件 fixture 从源断言提取预期，保留完整 snapshot 字符串与错误匹配；没有使用被测实现生成预期。类型化 enum 和五种 view 在 PG/PGlite 执行、回滚、重开验证通过。当前 Go/race 各 118/118，所选 core oracle 83/83；参数解码器内联类型断言与完整 ORM/SQL journal 仍需继续。
