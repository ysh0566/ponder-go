# 云端接续 M22

本次是执行端迁移，完整复刻目标没有取消。Mac 完成 M22 后暂停，不再并行写入。云端尚未由本线程启动或验收。先读取 AGENTS.md、CONTINUATION.md 顶部、parity/runs/batch-21.json、summary.json、test-ledger.json 和 feature-ledger.json。

## 代码与证据

私有项目 https://github.com/ysh0566/ponder-go，默认分支 main。最新已验证实现 SHA 和 tree 见 `parity/runs/cloud-handoff.json`；该文件作为后续纯文档交接提交保存，故不循环引用自己的 SHA。云端先用 Git 核对最新 main，再核对 receipt 指向的实现提交是其祖先且实现文件没有变化。Mac 最终 SHA/远端一致性由执行线程最终报告和本地忽略的 publication receipt 记录。

原版仓库必须另取完整固定基线，不能只使用本项目里的摘录：`https://github.com/ponder-sh/ponder`，commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`，核心0.18.2。测试工具默认两个目录并列：

```text
<cloud-task>/ponder-go
<cloud-task>/ponder-upstream
```

若上游不存在，在任务目录 clone/fetch 后 `git checkout --detach 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`，核对 HEAD 与工作区，保留 MIT 及所有原测试断言。不要把上游 Git 历史并入本项目。

Mac 冻结结果：292/905静态记录映射，Go288通过、保留SDK4通过；Go oracle210通过/78受fixture阻塞（DB65、profile13），613未实现。Go/race各1634叶子全通过，PG183/PGlite180分别通过。201活动core+2原skip、选择性声明4+15通过。53 ABI成功编译/53额外负向控制/1源错误、6 ORM源错误/6额外控制分别验证；原参数TS与SDK通过。差分2466基础/factory、867filter、135event（8预期异常）、20protocol均属补充。905只代表静态发现，不证明动态穷尽。跨平台重跑不能覆盖这些冻结证据，新增独立运行记录。

## 依赖准备

先检查云环境现有能力，不安装系统软件或修改权限。Mac 使用 Go1.27.1 darwin/arm64（go.mod最低1.25）、Node25.2.1、pnpm12.10.1、Vitest5.0.2、PostgreSQL16.15、PGlite0.2.13和Anvil0.2.0。Linux也需要可用C编译器：pg_query_go经CGO编译。不要搬运Mac二进制、Go/Node依赖缓存或绝对路径链接。

在固定上游目录，使用已可用的项目指定包管理器执行 `pnpm install --frozen-lockfile`；普通项目依赖解析在授权范围内，系统工具安装不在范围内。不要修改lockfile，不执行全局工具升级，不运行需要待批准Foundry下载的生成步骤。

然后在 ponder-go：

```sh
node tools/sdk-bootstrap.mjs
(cd sdk/typescript/client && ./node_modules/.bin/tsup)
```

这些命令只准备忽略的SDK依赖链接和client构建输出，原保留源码不变。Go依赖由go.mod/go.sum解析，verify使用项目 `.toolcache/gomod` 和 `.toolcache/gobuild`。`third_party/pg_query_go`有已说明的macOS符号重命名补丁，Linux兼容仍需实际编译确认。

## 测试服务

先检查已有任务服务。云端只创建新任务隔离数据，不能指向业务DB，不能复制Mac卷。若已具备Docker且没有本任务服务，可在确认名称/端口未占用后使用如下任务专用示例；这是供云端执行的说明，本线程没有执行：

```sh
docker volume create ponder_go_cloud_test
docker run -d --name ponder-go-cloud-test \
  -e POSTGRES_USER=ponder \
  -e POSTGRES_PASSWORD=ponder-local-test \
  -e POSTGRES_DB=ponder_test \
  -p 127.0.0.1:53398:5432 \
  -v ponder_go_cloud_test:/var/lib/postgresql/data \
  postgres:16
docker exec ponder-go-cloud-test pg_isready -U ponder -d ponder_test
export PONDER_TEST_POSTGRES='postgres://ponder:<redacted-local-fixture-password>@127.0.0.1:53398/ponder_test?sslmode=disable'
```

该账户仅是公开在测试文档中的隔离库fixture，不能用于生产。若没有Docker但已有PostgreSQL，可以创建同等隔离测试库并显式提供URL；不得把PGlite当PG验收替代。若两者均不存在，报告具体缺项，先执行独立可执行部分，不擅自安装服务。PGlite由 `bridge/pglite.mjs` 自动启动真实PostgreSQL WASM；默认从相邻上游解析依赖，也支持PONDER_UPSTREAM。每个测试自己的临时目录/连接会清理，不需要手动建立共享PGlite实例。

Anvil原生Go测试和选择性oracle会自动在127.0.0.1随机空闲端口启动、随后清理。不要预先创建长期Anvil或开放公网端口。云端先检查现有 `anvil --version` 和所需trace方法；本机旧0.2.0的每交易callTracer回退只是补充兼容，不能算原版block-hash trace用例通过。Foundry1.8.5项目级安装仍待用户明确许可，**没有安装授权**；不下载、不替换已有版本、不生成伪造ABI。完整原版fixture的forge artifacts/generated.js链路仍保留阻塞。云端如果已有合适版本，重新验证版本及原测试，不能假定结果。

准备齐备后在项目根目录运行：

```sh
./tools/verify.sh > parity/runs/cloud-first-verify.txt 2>&1
```

脚本实际执行Go/race/vet/build、原版纯函数/core、53 ABI runtime/race及编译正反例、选择性4+15、SDK与差分。失败时保留日志及部分执行状态，不把旧summary当新运行通过。manifest会先重建执行状态，只有最后record-results成功才是完整结果；若中断须区分已有冻结证据与当前未完成状态。全套生成ABI oracle不在成功子集内，不可删断言换通过。

## 接续实现与边界

M22实现文件和差异见CONTRACT_TRACE.md。先接旧contract-log调度器至统一Decoder，保留handler输入、错误计数/去重日志及与trace顺序；再取得确切原contract fixtures及逐例assertions。GetLogs顺序chunk50与source并发有明确差异，尚未接完整历史planner；rpc.actions16条已映射6条，余10条保留。随后factory/account/trace全配置、多链三种ordering、完整查询/GraphQL/SQL/SSE/API和CLI/模板/热重载、观测、类型/SDK/simulation继续在范围内。默认新shadow，不接管业务旧DB。sync/indexing分离、75位checkpoint、秒制reorgWindow、默认全局后缀撤销及原子业务/journal/progress必须保留。

只用现有授权身份ysh0566访问/发布本项目；项目级Git作者与提交者为 `ysh0566 <19503475+ysh0566@users.noreply.github.com>`，不改全局身份，不新建token。核对origin/owner/private/remote SHA后仅正常快进推送已验证阶段，不强推、开PR或部署。一次历史修正已经完成，Mac保留恢复refs和旧新SHA映射；这些本地备份不应推入远端或删除。云端所需映射已在 `parity/runs/author-repair.json`，无需传递凭据或本地恢复工作区。

Mac保留任务专用PG容器`ponder-go-task-20261023`/卷`ponder_go_task_20261023`，仅127.0.0.1:53398。交接不删除卷、不操作用户其他容器；是否仍运行见最终进程审计。云端全部重新建隔离fixture，无需这些数据。
