# 继续工作

任务没有完成。先读 `AGENTS.md`、本文件、`parity/runs/summary.json`、`parity/test-ledger.json` 和 `parity/feature-ledger.json`。不要把新增集成测试或差分向量算成上游逐例移植。



## 当前执行入口：M34已验证，完整目标继续

batch-33.json/batch-33-evidence 与 realtime-m34/full-verify-02 恢复验收实际退出0。Go/race各2463，PG343/PGlite340，0fail/skip；22包、170函数，每模式56个不重复串行批次，默认timeout保留。1065/f6a0342703f5fd7b5e7d8a3ce6ee36771281f3597c639d2656aae26ba36f825a准备后原生输入贯穿完整normal/race。精确映射486/905=482Go+4SDK，全部映射原版oracle通过；419未实现。

新增sync-realtime完整30原例/203断言/0类型义务，保留原hooks/Anvil/真实PG与PGlite setup/输入/断言/状态/返回cleanup。每backend/mode83个实际公开调用、46个原始provider请求，3个外部测试准备请求另记。Go独立无数据库内存API比较原始RPC消费、实际next/return步骤、结果/状态/输入块修改、Map/Set顺序及原始对象/frame身份和Error字段，额外警告比较仅排除时长；未用原版输出填充Go。完整原文件pristine各后端30通过。24源码向量为补充，旧复制trace helper同输入10红灯保留。见SOURCE_REALTIME.md。

首轮采集器误拒绝原测试准备RPC、normal03漏选trace名和race01因磁盘0余量在编译阶段SIGINT退出-2均完整保留。normal03的72项加trace-normal02的12项与race02的84项在1065/2b4ebe9a355ab6ad315d0548cf702a8896c6a6ec4530ae1e27482fb3854a223d同输入下通过。full01 的两组 PostgreSQL 写入因磁盘耗尽出现 SQLSTATE53100，全部原始失败保留；保持同一输入重跑受影响的完整批次，56组真实成功命令输出按字节汇总，58次含失败实际尝试另有清单。完整验证准备并冻结输入，两种模式之间仅按需清理任务编译缓存，保留源码/fixture/模块/类型程序/证据。暂未使用的工具采用无损压缩，有逐文件原始和压缩SHA，可恢复cast/chisel，Anvil保留可运行；Forge也已无损压缩并保留恢复信息。

上一实际发布M33 87555af3c8aba8f41154926238ceaa7bf1138649，tree 5d776cf3fa0265eb9e457e7c23468686f60bf55b，证明realtime-m34/m33-publication.json。M34只允许作为该SHA普通后继发布，ysh0566 author/committer并核验实际远端。已有默认repo/read:org/gist授权和GH_CONFIG_DIR持续有效，不重复登录/权限询问；不读token、不改全局helper、不force/改历史、不接触Mac/业务库、不建PR。

继续runtime/index15原例/30断言/6原生类型义务，再推进runtime/historical14原例/29断言及完整1732行API的omnichain/isolated/catchup/refetch/crashrecovery协调。M33的25个RequiredSourceIntervals向量和M34内存同步器不能替代这些原例或完整调度。外部history-next/m35/PLAN.md仅只读准备，未映射。其余查询/GraphQL/API/CLI/dev/templates/完整类型/SDK/simulation继续，整体目标未完成，不得停在里程碑。

## M33历史已验证基线

batch-32.json/batch-32-evidence 与 history-m33/full-verify-01 实际退出0。Go/race各2379，PG313/PGlite310，0fail/skip；22包、167函数，每模式56个不重复串行批次，默认timeout保留。原生输入1057/7b132b1f137d4da4bbd7ccab6aaab71367c565953e33f286e584455217588c61在准备后冻结，贯穿两种完整模式。精确映射456/905=452Go+4SDK，全部映射原版oracle通过；449未实现。

新增sync-historical/in-memory23及index17共40展开原例/102断言/12原生类型义务。原完整文件、hooks、Anvil、输入、断言、状态、RPC对象身份与返回cleanup保留。每后端每模式136个实际公开调用：Go独立消费原始provider输入并比较标准化结果/Map身份顺序/字段存在性与九张实际物理表。原版输出和预期SQL行未用于填充Go。pristine各后端40通过；四个只读TS程序各文件0-vs6诊断。12组具体负向Go编译与正向程序在四种backend/mode中实际执行，共48次；程序使用对应原例的Anvil，内存接口不建库，持久化接口用独立PG/PGlite schema。Go对照库先关闭，每项程序有独立afterEach，原例返回cleanup随后执行。

106个实际源码向量与10项运行时回归零额外credit，覆盖异步next-before-yield、100块预取、factory共享分页与最终drain、真实EVM/五表持久化、provider分页学习及receipt fallback所有权。原recover对象身份11红灯、共享Actions两红灯、旧Proxy身份重置、初始case_id/cleanup连带失败和部分缓存正向程序误断言的失败输入与报告均保留，正式normal05/race02及完整验收通过后才映射。具体接口和边界见SOURCE_HISTORICAL.md。

上一实际发布M32 cb17241a1d2d5d8a337af025c6a9e6e0801db202，tree 8ad7dce5f84298c2b8935f6f5f87c0be7ddbc234，证明history-m33/m32-publication.json。M33只允许作为该SHA普通后继发布，指定ysh0566 author/committer并核验实际远端。已有默认repo/read:org/gist授权和GH_CONFIG_DIR持续有效，不重复登录或权限询问；不读token、不改全局helper、不force/改历史、不接触Mac/业务库、不建PR。

接着实现完整runtime历史/实时/多链调度。runtime/index15原例/30断言/6类型义务、runtime/historical14原例/29断言和sync-realtime30原例/203断言仍未映射；M33的RequiredSourceIntervals补充向量不能冒充这些原例。固定源码runtime/historical.ts还包括omnichain/isolated/catchup/refetch/crashrecovery等完整范围。保留已有页处理与事务引擎，继续其他查询/GraphQL/API/CLI/dev/templates/完整类型/SDK/simulation。整体目标未完成，不能停在此里程碑。

## M32历史已验证基线

batch-31.json/batch-31-evidence及sync-store-m32/full-verify-02实际退出0。Go/race各2183，PG272/PGlite269，0fail/skip；21包157函数，每模式53个不重复批次，默认timeout保留。原生输入1031/8dae6bb50112f6bf40b5c122fe969005006cb35330d03a7090a740d8d0e62a5d在准备后、native前冻结，贯穿两种模式。精确映射416/905=412Go+4SDK，全部映射原版oracle通过；489未实现。

新增同步存储38条原例/63断言/9类型错误义务。完整原文件保留原hooks/Anvil/输入/断言，每后端每模式95个实际Go调用，严格比较返回值、Map顺序与真实键身份、ownproperties/undefined/NULL、查询范围和九张物理表；pristine38在两个后端分别通过。原版TS pristine0诊断、内存去注释恰9诊断；九对Go具体参数编译控制的正向程序在四次backend/mode中执行真实RPC、存储、哈希和pruning。typed API覆盖eth_call，更广RPC schema仍需实现。

SourceSyncStore与ReadSourcePage/DispatchSourceEvents使用实际协议缓存、共享事件/ABI和业务事务。source MD5与native SHA256明确共存；block0NULL、100/101查询、pruning/冲突/失败原子性/生命周期有补充回归。旧区间rewind四红灯原样保留，修复source expanded端点且保留native point区间控制。真实EVM四backend/cache页消费证明失败回滚、重放、evm_revert分叉后重新索引。范围获取/来源/finality/完整历史调度、metrics/context和所有JS强制转换仍未完成。完整full01保留normal2183通过/race2145通过1失败及全部1031输入快照：独立类型程序启动第三个PGlite时被内核OOM kill，后续7原例连带中断。调度修复在全部38原例比较后释放native对照数据库，再用独立afterAll hook逐项执行9个正向程序；原Anvil仍运行，原输入/断言/超时保留。后端分类优先实际dialect分支，避免PGlite原例名称中的postgres被双计，实际M31日志回归不变。初始observer/映射预检失败及compile01被取代边界也保留。

上一实际发布为M31 5c4be485caa8155f3516c2f1a84693aa737059aa，tree f2851c5e0f00a41b99d43a69ab03d78645752c93，证明sync-store-m32/m31-publication.json。M32只允许作为该SHA普通后继发布，继续指定ysh0566 author/committer、核验实际远端；已有GH_CONFIG_DIR与每命令helper授权持续有效，不重复登录/权限问题，不读token/改全局helper/force/改历史/接触Mac或业务库。

后续先推进sync-historical/in-memory23及index17，共40原例/102断言/12类型错误义务；静态审计或外部草稿没有credit。外部history-next/m33已有12实际async merge向量、44实际block/一致性向量及格式化未编译Go草稿，须独立编译/原始完整调用/类型/真实EVM/fullnormal/race验收后才能应用并映射。保留generator next-before-yield、100块有界prefetch、factory依赖分页/共享/最终drain与原始RPC泛型override义务。然后继续多链/实时/查询/GraphQL/API/CLI/dev/templates/SDK/simulation等完整范围，任务没有完成。接口与证明见SOURCE_SYNC_STORE.md。

## M31历史已验证基线

batch-30.json/batch-30-evidence及topics-m31/full-verify-01退出0。Go/race各2030，PG222/PGlite219，0fail/skip；21包149函数，每模式50不重复批次，默认timeout保留。989/9b6fe9b1dc8b141af45e676383455830afa40121cf33b85915b413a00109100a在native前准备并贯穿两种模式前后。精确映射378/905=374Go+4SDK，全部映射原版oracle通过；527未实现。新增82=实际source50+真实runtime32，零原例credit。

修复abi.EncodeEventTopics显式undefined标量wildcard；string/bytes OR null编码null、undefined编码empty，静态OR nullish仍拒绝。未把OR nullish改成wildcard。实际源码50保留标记和hash，比较完整topics与error发生，不外推全部JS强制转换/错误类文案。真实typedSource->topics/bloom/RPC/receipt/handler/PG/PGlite/cache/replay通过。旧4EVM红灯与中间source40两失败保留；冻结M30 985输入加未改测试精确重建986旧输入指纹。

见INDEXED_TOPICS.md。完整模块验收先于普通提交/发布；上一实际发布M30为61f4da4e86a8b55ffc43583c39c7c016e0ee0d86。M31实际发布按外部m31-publication.json，不用本地commit或bundle推断。官方CLI默认scope/任务持久化已授权，不再问、不读token、不改全局helper、不force/历史改写、不开PR/部署、不操作Mac/业务repo/DB。

下一步继续527剩余静态记录；外部history-next仅静态78例/165断言/21typeerrors审计，RPC MD5与旧nativeSHA256契约须分开。完整原生类型、ABI、配置、transport/context/include、历史/多链/实时、query/GraphQL/API/CLI/dev/templates/SDK/simulation仍未完成。M30首轮租约失败根因未确定，保留原配置，不可伪称已修复。

## 当前执行入口：M30已验证，完整目标继续

正式batch-29.json/batch-29-evidence/、sql-m30/full-verify-02退出0。Go/race各1948，0fail/skip，PG206/PGlite203；21包146函数，每模式50不重复批次，默认timeout保留。985/82761e1cc11689adcba96e49c7e40dadac8bb88b42b076ca7c8dd000987d039a原生输入在Go前准备并贯穿normal/race前后及最终汇总一致。累计378/905=374Go+4SDK，全部映射原版oracle通过；527未实现，静态905不是动态穷尽证明。

新增SQL14原例/19断言/38调用，8个真实Drizzle SQL输入和并发调用保留；完整原文件pristine通过与call-through采集分开。两个完整SHA碰撞断言、源双LRU/缓存错误/有序可变集合身份/UTF16 cap/PG16.1和公开Error(object)语义已核实。真正source readonly遍历接入Writer.SQL，双后端同输入旧DB2/ORM1红灯->ORM2/后续更新3/journal3；原子业务/journal边界保持。

新native增量83=原例14+源码向量61+cache4+HTTP2+真实SQL缓存回归2，补充不加sourcecredit。最初HTTP自有测试误用Go客户端仅状态码错误观察正文，保留失败/旧测试快照；原SQL/期待不变，直接观察实际HTTP正文后通过。保留首60向量与生成器快照、实际旧代码978输入重建证明；M29冻结977快照仍可复核。Go只读fixture，准备阶段生成并在native前记录快照。

首轮full01 normal1948通过、race1947通过/1原生250ms租约失败，81份快照保留；同输入单例5次/原批次2次通过后，未改代码/参数/断言重新完整执行full02。根因未确定，不能把复查当修复证明，不能拼接轮次验收。

M30普通提交必须基于实际发布M29 6cca7170dcd6ff29d114b5af7b2d5d9aea1caf3e，作者/提交者ysh0566；实际发布按外部m30-publication.json，不用本地commit/bundle推断远端。官方CLI默认repo/read:org/gist及任务认证持久化已明确授权，不再问，不读/输出token、不改全局helper、不force/改写历史、不操作Mac/业务repo/DB、不开PR/部署。

继续完整原生类型/ABI/transport/context/include、负数和历史调度、多链/实时校验重连、查询/GraphQL/API/CLI/dev/templates/SDK/simulation及527静态记录。sourceNode静态联合、完整TS/JSSet语义不由这批证明。显式undefined indexed topic差异已在外部config-next/topic-audit实际复现，仍待修复/接真实运行时。任务未完成，继续推进。

## 当前执行入口：M29已验证，完整目标继续

正式batch-28.json/batch-28-evidence/、config-types-m29/full-verify-01退出0。Go/race各1865、0fail/skip，PG204/PGlite201；21包141函数，每模式50不重复批次，默认Go/source timeout保留。977/12b021ae80a8fc75e9c90a9860e082153b6ec7328b4b5199c86d42e135f0a5c0输入快照在native Go开始前记录，贯穿normal/race前后与汇总相同。累计364/905=360Go+4SDK，所有映射原版oracle通过；541未实现，静态905不是动态穷尽证明。

新增4配置原例/7断言/9项生产native类型义务；全部39active/92断言与10项配置类型义务已映射/验证，原RPC skip1/2断言保持未映射。typed容器保留被忽略错误的真实shape，构造/配置consumer/indexing consumer各自施加结构和bounds约束；实际链/ABI生成封闭引用进入真实builder/handler。每模式九个正向编译程序在同一原版Anvil执行，负编译诊断具体对应原错。Event2->Event0控制保持args.arg0x，仍验证剩余字节长度错误，不能填充值伪造成功。

真实typed8+generated4个两后端/cache场景与nilselection1回归补充，不加sourcecredit。保留新输入抽取器错误、确定性fixture重写的未接受聚焦轮次、过窄错误匹配造成1控制失败。默认Go测试只校验fixture，显式--write仅在准备阶段；后续指纹一致。当前生成API快捷链/单ABI/单eventfilter、底层动态接口和Go零值差异不宣称完整TS等价。

普通提交必须基于实际已发布M28 80bc22273a8bc740886dee9b8a46214c79f98106，ysh0566作者/提交者；实际发布按外部m29-publication.json，不用本地commit/bundle推断远端。官方CLI默认repo/read:org/gist和任务云端持久化已获明确授权，不要再问；不读/输出token，不改全局helper，不force/改写历史，不操作Mac/业务repo/DB，不开PR或部署。

后续仍完整原生typed/ABI/transport/context/include、负数调度、历史planner、多链/实时重连校验、查询/API/CLI/dev/templates/SDK/simulation及541静态记录。外部config-next/typed-obligations-next.md还保留已复现的显式undefined topic差异，以及尚待实际source对照的重复ABI事件/完整JS值语义审计线索；需真source差分，不能由既有通过外推。目标未完成，继续推进。

## 当前执行入口：M28验收完成，完整目标继续

正式证据batch-27.json/batch-27-evidence/、config-m28/full-verify-02退出0。Go/race各1852、PG198/PGlite195、0失败/skip；20包138顶层函数，每模式48不重复串行批次，默认timeout保留。所有实际执行均对应测试前952/ded57969d2f68ba7910400427e04ecae8ffc552be7f9f4400f9d776443a11a8e输入快照。源码360/905=356Go+4SDK均有oracle通过，545未实现；905静态记录不是动态穷尽证明。

配置完整原版39active/92断言通过、1原始skip/2断言保留；69实际Go调用与同一个原版Anvil比较完整结果/错误/输入修改。每模式60次原生规范链元数据身份检查，包括Hyperliquid配置/索引各1次。只映射35例/85断言；4例仍欠9项原生类型义务，严格保持未映射。区块Filter联合的1项错误义务已有真实正向编译/实际原始区块输入执行/负向编译。新来源配置/工厂8个真实后端cache场景和RPC并发1例为补充，零source credit。原生增量48=39运行时原例（其中4未完成类型）+8集成+1并发。

保留新增测试进度API编译错误、collector race，以及首轮发现Hyperliquid身份断言缺口后主动停止的1473完成/0fail/skip部分normal、实际退出143、未启动race。不得把部分结果代替正式full02。原版输入/setup/断言/skip不变。SourcePlan版本1隔离新配置/cache标识，旧native Config及legacy合约factory标识不改。601目录描述/17opaque函数不代表viem格式化行为已移植。见SOURCE_CONFIGURATION.md。

M28必须基于已核验远端M27 219a76e11dca7fa3804324c72401405d48c129d7普通后继提交，作者/提交者ysh0566；实际发布以外部m28-publication.json成功证明为准。用户已明确允许官方CLI默认repo/read:org/gist及任务云端认证持久化，不要重复询问。禁止读/输出token、改全局helper、force/历史改写、Mac/业务仓库/业务数据库操作。

下一步先补4例9项真正生产typed API编译义务：network迁移3、未知链1、未知ABI事件1、字符串bounds属性/调用4。外部config-next/typed-obligations-next.md是设计笔记，零source credit；不要用语言玩具/任意名义错类型/缺失map key替代真实约束。更完整的typed DSL、transport函数、完整RPC/context/include、负数区块调度、历史planner、多链排序/调度、实时重连/校验、查询/API/CLI/dev/templates/SDK/simulation仍全部在用户完整范围内。

## 当前执行入口：M27已验证，完整目标继续

最新验收batch-26.json/batch-26-evidence/，full-verify-02退出0：Go/race各1804通过、0失败/skip，PG194/PGlite191。20包134顶层函数48不重复串行批次，默认Go timeout及执行体保留；前后935/c18845a3bd6421af83240dd76033c2647b09f5ccc5ebb5db60f86c8d1bf6225d一致。源码325/905=321Go+4SDK均有oracle通过，580未实现。8个新bloom原例/12断言/19调用及两项Topic0错误义务经真实正向/运行/负向编译通过。Go省略值/string构造和其他dynamic字段仍宽于TS，不能称完整类型等价。497补充源码向量、新假阳性和native回退测试均无source credit。见docs/LOG_BLOOM.md。

首轮完整normal1795通过/4失败/0skip，没有启动race，快照保留bloom-m27/full-01-evidence/。失败是自有补充宽查询场景（原版映射0条）的旧RPC计数3；旧输入/setup不变且预期明确更新1。独立4场景真实跨日志Bloom假阳性保持3次RPC并正确本地筛选；actual pinned source bridge对实际头判断旧[true,false,false]/新[true,true,true]。恢复race24通过，包含empty0x/zero/short/malformed、cached缺字段原校验/无进度/重试、uncached缺字段同数据及真实chain143。原版源码/输入/setup/断言均未修改。

M26已核验发布253f4f903a534dcc4f7f745a9a8d0ea3bdcd095d，tree fb1fd3f2e4d44f8d3408f319b1f896271f1e9cd6；证明bloom-m27/m26-publication.json。M27提交应是该SHA的普通后继，保留ysh0566指定author/committer，发布须核验实际远端，不能由本地提交/bundle推断成功。已有明确授权的HTTPS gh任务认证目录及每命令helper可复用，禁止输出token、全局helper变更、force/改写历史或Mac操作。

之后继续完整配置构建器/历史planner/多链排序/实时完整校验与重连/查询/API/CLI/dev/templates/类型/SDK/simulation。外部config-next只完成40静态记录审计（39active+1原skip、94断言、10类型错误分布5例）及未编译ABI有序事件草稿；没有接入或source credit。PLAN.md保留真实原版TS与native Go HTTP桥同时使用同一Anvil的验收设计、真实public类型义务限制及ABI元数据保留要求。所有功能仍在用户原始完整范围内，任务未完成。

## M26：上一已验证云端基线

M26基于M25本地8954911接入account.Factory、共享发现/child birth筛选、早于业务起点的factory历史和新account bounds/parent校验。只接API的实际EVM红灯保留prebirth99错误派发。聚焦race最终29/29通过，含19配置/2默认值与身份/8真实PG/PGlite×legacy/staged×cache场景，覆盖from/to四种callback、事务失败、cold reopen/cache翻转、safe后live重放、same-parent-tx-hash换child、晚business start和canonical vs orphan cache。两次聚焦fixture失败保留并明确不是产品修复。完整verify退出0：Go/race各1769通过、0失败/skip，PG189/PGlite186。19包、129顶层函数、46不重复串行批次，源码前后922/3066e64710d39e96b10634f0468c7f842505a93a401dede5b43ed42d70eef1ca一致。vet/build/oracle/capture/type/SDK/差分均通过。source317映射/588未实现不变。最新证据batch-25.json与batch-25-evidence/；提交须为8954911的普通后继、原SHA与指定身份，验收后方可发布。见docs/FACTORY_ACCOUNTS.md及parity/runs/account-factory-m26/。

用户已明确批准官方CLI默认repo/read:org/gist并重新发起登录；第二次CLI流程41773实际退出0，登录ysh0566/id19503475。仓库private/owner/推送权限及远端main9180e4f先核验，再以每命令HTTPS helper依次普通快进推送M23 3e208c1、M24 4d654d5、M25 8954911；每步退出0且远端SHA/tree/parent/author/committer一致，最终main89549111c03f57fa3a35d9917bbab786d46177d7。没有全局helper改动、token内容输出或Mac操作；任务认证目录0700、文件0600。最新证明account-factory-m26/authentication-success.json、publication-preflight.json与publication-results.json；旧停止/失败探测保留为历史。M26当时为WIP，未推送。

## M25：上一已验证云端基线

当前 checkout `/root/ponder-go/work/ponder-go`；固定上游相邻 `ponder-upstream`，HEAD `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`。M25 是 M24 `4d654d598375ce75c4e448051443672326bba8c8` 的普通本地后继，提交89549111c03f57fa3a35d9917bbab786d46177d7已发布。该里程碑验收为 `parity/runs/batch-24.json`、`batch-24-evidence/` 和 `summary.json`。

新增15个精确原例（events6/filter9）、38个原断言表达式和1个 callback union 编译错误义务。现在 events11/11、filter23/23 均映射；完整原文件用真实生成ABI和部署/转账helper、未改断言实际通过11/23。call-through采集与此前八文件的 pristine PG/PGlite 原版验收分开标注；Go逐项重放完整输入、结果、调用后状态及原显式断言。public callback union 的正向构建/运行和具体负向编译诊断均通过，不宣称动态filter/metadata/Fn和旧Object API完全具备TS静态约束。账户JSON整数舍入缺口经固定版本红灯、修复后race验证；只共享UseNumber，不变更两个路径缺省行为。见 `docs/EVENT_SOURCE_COMPLETION.md`。

完整 verify 第二次退出0：Go/race各1740通过、0失败/skip，PG185/PGlite182分别通过；19包、126顶层函数、46不重复串行批次，原Go默认timeout保留。前后指纹919文件/3ce5807f7885723be039c5b555ad9329aa5fe057f59cee57fb187c2ab85244ea一致。类型/ABI/ORM、SDK、vet/build、适用oracle与2466+867+135+20+62补充差分通过。首次verify在旧选择性oracle错误纳入新增filter映射时退出1，尚未执行Go/race；已恢复旧14filter+split选择范围，新增9例走完整文件采集，失败日志保留。

累计317/905映射并执行通过=313Go+4SDK，588静态记录未实现。native编译验证8条源码错误义务，retained SDK另1条；剩余types、905动态穷尽性和完整功能仍未完成。此前八个原版文件在PG/PGlite各129通过/3原skip的上下文和报告hash复核通过，本轮未把旧报告称为新跑的完整上游验收。

M23–M25传输阻塞已在用户明确授权网页登录和默认权限后解决，原SHA均已通过HTTPS发布；三份已校验bundle继续保留。最新发布证明在上方入口；rpc-actions-m24/publication-check.json仅记载当时的历史阻塞。不操作Mac、不强推/改写历史。

上述factory account目标现已接入M26并完成29项聚焦race；完整验收状态见最新入口。旧contract factory默认ToBlock/ID保持，完整config/identity升级设计仍待做。继续完整source配置/history planner、多链三种ordering、查询/API/CLI/dev/templates/类型/SDK/simulation，任务未完成。

## M24：上一已验证云端基线


实际checkout是 `/root/ponder-go/work/ponder-go`，完整固定上游在相邻 `ponder-upstream`。27个原始Git提交与1916文件已校验恢复。M23本地提交 `3e208c1b35beaa87f936a958391ca431df80c757`，M24是其普通本地后继，以Git HEAD为准。没有改历史或操作Mac。最新正式证据为 `parity/runs/batch-23.json`、`batch-23-evidence/` 与 `parity/runs/summary.json`；下方M22是冻结Mac历史。

M24补齐剩余10个rpc.actions原例、29个原断言，包含分页惰性/提前停止、每RPC的范围学习、失败重试/错误身份、收据fallback/provenance/排序及日志一致性。该原文件16/16现在都有精确Go映射。62个实际source函数差分向量和6个native边界用例分开，不加source credit。实现和限制见 `docs/RPC_ACTIONS.md`。

本轮完整verify退出0：Go/race各1723通过、0失败/skip；PG185/PGlite182分别通过。全部19包、123顶层函数、44个不重复串行批次，Go默认timeout保留，前后/最终源码指纹911文件/a1906a266e294eeac9ce4705b7c5c05acdaf2ca06546da3b51896c19299a2eb3一致。vet、CLI/示例构建、ABI/ORM正反编译、参数tsc、SDK4与基础差分2466全部通过。原core201活动/2原skip、选择性4+15、filter867/events135/protocol20及RPC62分别保留。

累计302/905映射=298Go+4SDK，全部执行通过，已映射原版oracle也通过；603静态记录未实现。八个原版文件在PG14.24与PGlite0.2.13各129通过/3原skip，原setup/helper/生成ABI和断言未改，Foundry1.8.5已核验安装。905不是动态穷尽证明，八文件也不是完整上游验收。M23及更早冻结证据保留，不混用跨平台通过数。

本M24历史验收时发布仍阻塞（现已由上方M23–M25发布证明取代）。父会话显式授权区分传输账号与commit身份后，禁用转发agent、仅用既有云端SSH key做只读目标检查，返回Repository not found。ysh0566连接有blob/tree和普通ref API，但无raw commit或author/committer/时间输入，无法保留本地原SHA；远端main核实时仍9180e4f。具体证据及已有认证的非Mac环境导入Git bundle后普通快进推送的最小步骤见 `parity/runs/rpc-actions-m24/publication-check.json`。不创建凭据、增权限、改写提交或强推；本地提交不能称已发布。

M25完整验收已完成，最新计数和后续工作以上方M25入口及batch-24为准；本节保留M24历史，不覆盖最新结果。

随后继续factory account目标、完整配置与历史planner、多链三种ordering、完整查询/API/CLI/dev/templates/类型/SDK/simulation；完整目标未完成。

## 当前交接入口：M22，Mac 暂停以切换云端

用户已创建专用云环境；本端完成当前回归与发布后暂停，不开启下一批。已验证且已发布实现提交 `5a3886dfcf30d2b2debd61090fe24adf8513497a`，tree `718d6a61cf0d26d45afacf26c78e88e45ee5fdca`。之后仅有本节 SHA 与 `parity/runs/cloud-handoff.json` 的纯文档交接提交，最终 main 以 Git/远端核验和执行线程报告为准；上一发布基线为 `8965d61031f85c4a9d3e43fa541a0f662b5106ca`。当前正式证据为 `parity/runs/batch-21.json` 和 `batch-21-evidence/`，此前各批只作历史证据，下面早期“下一批”或计数不得覆盖本节。

- 固定源 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` /0.18.2；905条静态记录并非动态穷尽。292映射=288 Go通过+4 SDK；Go oracle210通过/78阻塞（DB65、profile13），SDK4通过，613未实现。
- 最终 `PONDER_TEST_POSTGRES=<task-only> ./tools/verify.sh` 退出0；Go/race各1634/1634，PG183/183、PGlite180/180，无skip/fail；vet、CLI/示例build通过。core201活动通过/2源skip；选择性原声明4+15通过；ABI53成功/53额外负控制/1源错误、ORM6源错误/6额外负控制、原参数tsc与SDK均通过。
- 补充差分：基础/factory2466，filter/fragments867，事件135（8预期异常），协议20。与源记录分开，不加源credit。M22只新增4条rpc.actions源记录/8断言；15条oracle是此前已映射记录，不能再加映射数。
- 新实现：`abi/function.go`、`events/contract.go`、contract trace接入`runtime/accounts.go`、`rpc/logs.go`。ABI selector严格大小写；viem tuple空名和bytes小写适配仅在function边界，原参数helper不变。ABI顺序/重复项/selector碰撞保留。真实EVM同交易hash替代分支、缓存开关、handler失败回滚在两后端通过。
- 原始失败与修正日志均保留：旧诊断Go源码曾被测试误收集导致构建失败，已改`.go.txt`并全套重跑；selector/空tuple名/bytes大小写/零参数附带数据差异先红后绿，不修改上游断言。最终只计最终成功运行。
- `docs/CONTRACT_TRACE.md` 明确接入边界：旧contract log调度仍走ABI.DecodeEvent并传播错误，尚未统一batch decoder；新GetLogs未替换完整历史planner，chunks为顺序调用（source Promise.all）。原generated ABI contract测试未冒充通过。
- Foundry1.8.5项目级安装仍待明确批准，未安装。旧Anvil0.2缺block-hash trace；3条原ETH fixture仍在断言前失败，native每tx回退仅额外验证。云端已有不同版本时先探测，不沿用Mac能力结论；安装/替换仍须遵守许可。

云端依赖、隔离PG/PGlite/Anvil启动、权限边界和禁止迁移内容见 `docs/CLOUD_HANDOFF.md`。不传凭据或本机卷。优先核对main/tree/完整性，再新建测试库重跑；跨平台结果单独登记，保留Mac冻结证据。下一实现批次：统一contract log/trace decoder接入与原事件fixtures/断言，剩余10/16 rpc.actions原例、factory account targets/source/trace，随后多链排序与完整查询/CLI。关系、GraphQL、完整SQL/SSE、开发重载、模板/观测和类型/SDK/simulation剩余仍全部在范围内。

## 已保留的状态

- 工作目录 `/Users/ysh0566/Documents/Codex/2026-10-23/task/ponder-go`。
- 完整上游 `/Users/ysh0566/Documents/Codex/2026-10-23/task/ponder-upstream`，固定 `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`。
- 已创建独立 Go Git 仓库并发布到私有 `https://github.com/ysh0566/ponder-go`。用户后续明确授权推送经验证阶段；每次确认账号、远端与 SHA，不开 PR、不部署。Foundry 安装仍待批准。首次发布凭据不落盘、不切全局账号；receipt 在 `parity/runs/publication-receipt.json`。
- 用户已明确批准一次历史身份修正并已执行：13个错误author/committer全部改为ysh0566，旧e9747c7对应新a7a1f30；原M14提交6c9b2f1安全重接为8dc922b。14个提交的tree逐一相同，日期/消息不变。旧新SHA映射在 `parity/runs/author-repair.json`；保留 `refs/backup/author-before-ysh0566`、`refs/backup/before-author-repair-20261023` 和忽略的 `.local/author-repair-20261023/` 工作区备份。修正后恢复普通阶段推送，不再强推。Foundry许可仍未收到。
- 已启动现有 Docker Desktop，并创建 `ponder-go-task-20261023` / `ponder_go_task_20261023`。先 `docker inspect` 核对存在和 `127.0.0.1:53398`，直接复用；不要操作其他容器。
- Go 缓存位于 `.toolcache/`，基线 npm 依赖已按冻结 lockfile 安装。所有 SDK 源码原样保留在 `sdk/typescript/`。
- 计划 PDF `libfile_90c2e75070c481918ecace631e841e0f` 下载失败，没有本地副本；完整需求已经在初始委托中给出，无需等待它。

## 下一可执行批次

1. 从最新提交和 summary.json 恢复。M21 后累计 288 条映射（284 Go + 4 SDK），Go oracle 191 通过（含4条选择性未改动声明）、93 fixture oracle 阻塞，617 静态记录未实现；最新deposit补充修正及执行结果看 batch-20.json，M21完整冻结结果保留batch-19.json。不要重做已通过源例，也不要把静态发现当成动态穷尽证明。
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


## M21补充：deposit optional fee语义修正

复核发现SyncTransactionToInternal(type0x7e)把maxFee/maxPriority为null或空串时保留输入，而源码返回undefined。先增加4个实际source-generated控制（undefined/null/空串/0x0），真实红灯2leaf，再修正转换为filter.Undefined；0x0仍是*big.Int零。source credit不变，288映射/617未实现，Go284（oracle191含4选择性，93完整suite受限）+SDK4。events差分119（8预期错误）分别全部通过。最后完整verify退出0：Go/race1606/1606、PG181/PGlite178，vet/build/type/core201活动/2skip/选择性4/SDK及各差分都通过；33份M21旧冻结证据未改，补充证据batch-20.json。当前无在途命令。

下一步除前节contract decoder/trace/rpc.actions剩余14源例，还可扩展选择性oracle至已移植的14个filter静态/Genesis例及splitEvents：它们原full suite缺generated.js，但不是都需要生成ABI。明确保留whole-suite收集状态与selective source结果，不能把所有93受限项笼统称不可执行。ETH transferEth三例则已经实际确认旧Anvil缺blockHash trace方法，native每tx回退不加这些source credit。Foundry安装仍pending未执行。

M26之后继续完整source配置/planner、多链三个ordering、完整查询/API/CLI/dev/templates/类型/SDK/simulation。独立bloom-next草稿在任务资料目录，8个确切源例/12断言/2个topic0=undefined编译义务；需真实public型接口、完整原文件采集与原断言、真实编译证明、source差分和运行时连接，不可只以map键缺失计类型通过。zero bloom按原码视为missing，async链绕过，不得跳过account交易/trace。草稿未接入/编译/计数。
