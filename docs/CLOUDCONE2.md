# cloudcone2 接续状态（进行中）

完整复刻没有完成。主 checkout 是 `/root/ponder-go/work/ponder-go`，固定完整上游是相邻的 `/root/ponder-go/work/ponder-upstream`。项目入口 `/root/ponder-go` 原有内容及只读占位目录保留；Git 初始化和对象恢复通过当前执行器审批后在任务内部 checkout 完成，没有拒绝后改路径重试。

已验证的M23恢复阶段：验收已闭合。Go和race各1645通过、0失败/skip，PG185/PGlite182分别通过；源码指纹在两轮前后及最终记录时一致。vet与CLI/示例build、ABI/ORM编译正反例、参数tsc、SDK4、基础差分2466均通过。正式receipt为 `parity/runs/batch-22.json`，独立冻结证据为 `batch-22-evidence/`。

第三轮完整verify原命令退出1：Go/race与前置检查全部完成后，React的Vitest子进程因PATH找不到tsc而未执行测试。失败报告保留在 `sdk-missing-tsc/`。验证脚本已显式加入固定上游node_modules/.bin，原React类型例随后通过；差分与最终记录器退出0。尾部差分首次受到sandbox的Go子进程EPERM限制，保留失败；经现有执行器审批后成功运行。没有把原verify退出码改成0，也未重复已通过的重型测试。

第二轮完整Go为1637通过/2失败，失败均是原 `TestFactoryRuntime` 在新投影中先于预期handler异常退出；没有执行race，失败证据在 `candidate-02-evidence/`。原fixture和断言没有修改。最小红绿回归定位并修复缺省optional数量转换失败、JSON大整数舍入、decoder重编号改写handler索引三个生产问题。红灯3失败/3通过；修复后13个聚焦回归通过，最终全模块验收包含这些回归。

父会话要求先闭合当前候选验收，现已完成。独立RPC草稿保留在 `.cloud-bootstrap/rpc-next`：10个剩余action原例已在该草稿的纯mock/HTTP测试通过，但没有合入本批，不参与本批源码指纹、通过数或source credit。

最新M24完整verify退出0：Go/race各1723通过、0失败/skip，PG185/PGlite182分别通过，前后/最终原生源码指纹一致。剩余10个RPC actions原例/29断言全部映射执行，62补充向量与6个native边界分开；vet/build/type/SDK和各差分通过。累计302映射=298Go+4SDK，603未实现，所有已映射原版oracle通过。正式receipt与冻结证据为 `parity/runs/batch-23.json` / `batch-23-evidence/`。独立下一批草稿未合入本次验收。

## 已核验的恢复

- 既有 GitHub 连接的登录是 ysh0566 /19503475，目标是私有 `ysh0566/ponder-go`。
- 通过该连接恢复当前1916个文件和全部27个原始历史提交。每个blob/tree/commit按Git原格式核验SHA；HEAD是 `9180e4f915f3e3fdce27bcaf5a4e4a7c7be8da1a`，tree是 `674bac1b54679954512c89c6d867ee2777edd73b`。没有创建替代历史。暂态shallow状态在补齐历史后移除；`git fsck --full`通过，`git rev-list --count HEAD`是27，M22实现提交是HEAD祖先。
- 完整官方上游clone固定在 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`，工作区没有跟踪文件改动。原版Foundry合约fixtures生成成功。
- 项目级作者与提交者配置是 `ysh0566 <19503475+ysh0566@users.noreply.github.com>`。没有操作Mac，也没有新建或转移凭据。

机器证据在 `parity/runs/cloudcone2-20261024/recovery.json`。该文件记录创建时状态，实时执行结果以下方日志和后续receipt为准；不能把其中running状态当通过。

## 工具与隔离服务

Go1.25.0 linux/amd64、Node24.20.0、pnpm12.10.1、Foundry1.8.5。Go与Foundry从官方固定版下载并核验官方SHA256；普通工具安装已获cloudcone2授权。旧文档的Foundry待批准状态是Mac历史限制。

现有PostgreSQL14.24使用新任务集群 `/tmp/ponder-go-cloudcone2-pg-20261024/data`，仅监听127.0.0.1:53398。固定上游 `docs/pages/docs/requirements.mdx` 明确支持PG14/15/16/17；Mac历史16.15结果不算此次通过。PGlite0.2.13单独运行，没有用SQLite替代。

任务环境可通过 `. /root/ponder-go/.cloud-bootstrap/env.sh` 恢复。文件含公开测试fixture连接参数，不含业务凭据。只操作该任务集群。

## 实际执行与资源限制

原始基线worktree是 `/root/ponder-go/work/cloud-baseline/ponder-go`，保持原提交的代码和测试；它的生成报告与主checkout中的冻结Mac证据分开。

1. 第一次原始 `./tools/verify.sh` 在 `go run ./cmd/abi-codegen` 触发原45000ms超时，日志 `/root/ponder-go/.cloud-bootstrap/cloud-first-verify.txt`。预热命令 `go test -run '^$' ./...` 仅编译，没有测试通过credit；日志是 `cloud-cold-compile.txt`。
2. 缓存预热后原始verify重新运行，日志 `cloud-baseline-verify-warm.txt`。此轮GOFLAGS=-p=2，并行的额外PGlite验证造成内存耗尽。内核记录08:50至08:52多次MainThread被杀；原PGlite oracle退出137。Go运行中出现PGlite启动EOF和短租约到期，不能计为通过。不得用旧summary覆盖这次失败。
3. 后续重型测试必须串行。Go使用 `GOFLAGS=-p=1 GOMAXPROCS=2`；不得同时启动Go PG/PGlite测试与PGlite原oracle。若同批原oracle仍不能稳定容纳，按原文件分开进程运行，保留原setup和每条断言。

PG原oracle已完成：8文件、129通过、3个原skip、0失败。证据 `parity/runs/cloudcone2-20261024/original-pg.json`。完整原globalSetup、原setup/helper和原测试文件未修改；`tools/full-oracle.config.mjs`只限制worker数，`tools/loopback-only.mjs`将默认通配Node监听限制为loopback并记录binding。不替换trace helper，不添加fallback来换取原例通过。

同组PGlite原oracle的第一次运行退出137，没有完整JSON结果，不能计通过；失败日志与退出码保留。PG129通过不等于Go新增129条移植；静态映射仍是历史292/905，613未实现。

随后按原文件串行重新运行真实PGlite，同样129通过、3个原skip、0失败，见 `original-pglite-serial/`。两backend的原始报告和source/setup/生成fixture哈希分别保存在 `full-original-postgres-oracle.json`、`full-original-pglite-oracle.json`，最终记录器将核验这些上下文。它们是8文件子集，不是完整上游验收或新增Go source映射。

## WIP与续跑顺序

当前已验证代码包括：legacy contract log与trace统一批次Decoder；新增真实EVM的畸形日志去重/统计、有效事件继续、handler失败回滚及同交易hash重组回归；PGlite启动退出状态诊断；ABI代码生成harness分开冷编译和45秒生成执行，分别记录阶段时间和错误。

新回归 `runtime/contract_log_batch_test.go` 是补充测试，没有原source credit。最初两次Go构建与源码编辑重叠，不能作为产品红灯证据；固定原提交的独立red worktree随后实测重现原decoder传播错误。另有fixture异步挖矿、缺少初始共同祖先的问题，已经增加receipt等待并将补充fixture起点设为0；保留全部原失败日志。最终版本已在PG/PGlite、legacy/staged四个叶子通过，完整Go/race也各包含这些叶子。

`tools/compile-fixtures.mjs` 的新分阶段harness已执行53类型正例、53额外负控及1个原类型错误要求，均通过，见 `codegen-check/`；源原断言未删除。进程诊断回归曾通过，后来仅将名称改为 `TestBridgeStartupReportsProcessFailure`，避免被按测试名统计器误认为真实PGlite后端用例；最终运行已使用新名。

首轮候选另在ORM CLI的go-run冷编译触发45秒超时。共享 `tools/go-codegen.mjs` 现将ABI和ORM都分成单独build与有界生成阶段；实际ORM首次build为75954ms，随后strict/nullable生成成功，6个原类型错误要求和6个额外负控均通过。Go测试按完整枚举、互不重叠的4个顶层函数分批串行运行，保留Go默认超时；前后原生源码指纹必须一致，最终记录器拒绝旧报告或混合代码的Go/race结果。

本批完整模块验收及最终记录均已完成，当前没有运行中的重型命令。旧失败和Mac冻结证据保留，阶段提交与发布结果以Git及后续receipt为准。下一步在独立本批证据保留后推进RPC原例精确移植。

## 发布阻塞与后续完整范围

cloudcone2 HTTPS没有现成Git认证。父会话后续显式授权区分传输认证账号与commit身份，并要求只读核实已有路径。已禁用转发SSH agent、清除SSH_AUTH_SOCK，只使用云端既有 `/root/.ssh/id_rsa`，严格主机校验认证为whyareulaughing；对目标仓库的ls-remote返回Repository not found，路径已停止，未推送。未接触Mac或转发agent、未增权限/凭据。

既有ysh0566连接重新核验为本人/私有owner仓库，远端main仍9180e4f。已检查Git对象API：blob和tree可创建，ref支持force=false及expected_sha；create_commit只收message、tree及parents，不能上传raw commit或指定author/committer/时间，因此不能保留M23本地SHA。未用普通文件提交API替换已验证提交，未试写错误commit或移动ref。

完整核实receipt是 `parity/runs/rpc-actions-m24/publication-check.json`。已导出并verify M23增量bundle `/root/ponder-go/.cloud-bootstrap/ponder-go-M23.bundle`，只含已验证Git对象，prerequisite9180e4f、目标3e208c1，SHA256为61b651a998ef9b0d32bd964dce0c58a8069780e103c015ef0187cd2a3f19300b；不含凭据或M24未验证改动。最小用户步骤是在已有目标仓库Git认证的非Mac环境导入bundle，先核对远端main仍为预期基线，再普通快进推送原SHA并核验远端。远端变化先停止复核；不申请新权限、token或密钥。发布阻塞不影响代码与测试继续。

rpc.actions的16条原例已全部映射，但未测试helper/完整协议和planner接入仍缺；原generated-ABI逐例移植、factory/account/source/trace、多链三种ordering、完整关系/GraphQL/SQL/SSE/API、SDK/类型/simulation、CLI/dev/templates/观测等仍按完整台账推进。严格TS零源码修改与旧数据库原地接管仍是独立未完成轨。不得将本接续阶段称为全功能完成。
