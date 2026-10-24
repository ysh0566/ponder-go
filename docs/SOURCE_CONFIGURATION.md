# 来源配置构建器

M28 的完整模块验收通过：Go/race各1852、0失败/skip，PG198/PGlite195。20包138顶层函数、每模式48个不重复串行批次。普通提交/推送以实际M28 publication receipt为准；此前已核验远端M27为219a76e。本批不是整个Go移植目标的完成声明。

`config.ParseSourceConfiguration` 读取来源形状的动态 JSON，保留缺失值、null 与对象顺序。`BuildSourceConfig` 处理链默认值和校验，创建真实 `rpc.Client`，并按来源规则修改缺失/空字符串 RPC 的输入。`BuildSourceIndexing` 从实际节点取得 latest/finalized 头，合并链覆盖，构造 setup、合约日志/调用、账户交易/转账和区块回调。完整 ABI 原始元数据随回调保存，包括 internalType 和显式 false。

`EventCallbacks()` 返回带公共 `SourceFilter` 联合的回调。读取区块 interval 前须收窄到 `SourceBlockFilter`。`NativeConfig(chainName)` 为现有单链调度器选择一条链；`runtime.NewFromSource` 绑定真实 `runtime.Handler`，复用构建器的客户端、筛选条件和 ABI。多链配置构建不等于多链调度已经实现。

原生来源计划采用独立的 `SourceRuntimePlan.Version=1` 配置/缓存标识。旧原生 Config 在 SourcePlan=nil 时仍产生原来的 JSON。来源工厂保留 undefined 边界对应的完整标识；旧原生合约工厂默认值未改。来源工厂的 shared discovery、出生前排除和同块子日志/转账在真实 Anvil、PostgreSQL 和 PGlite 上验证。

链目录来自固定版本源码与实际 ESM viem2.35.0：601 个不同 id，按原 namespace 第一个匹配项选取，并保留 Hyperliquid999 显式覆盖。17 个函数值只是目录中的不透明描述，不代表已移植 viem formatter/serializer。`node tools/config-chain-catalog.mjs` 校验目录，显式 `--write` 才更新资产。

完整原版 config.test.ts 有40条展开记录、94个断言表达式：39 active/92断言，加1个原始skip/2断言。live桥保留原版文件、globalSetup、setup、常量、真实节点和断言，只观察导出的函数；返回给原版断言的始终是原版结果。Go 在同一节点仍存活时执行69次实际构建，比较完整结果、错误和输入修改，并检查实际客户端、处理器和链对象身份。Hyperliquid的原始toBe断言还必须检查配置和索引构建结果中的viemChain确实引用原生目录的规范对象；完整值相等不足以替代对象身份。Go运行期间仅写 parity/runs 与编译输出，不修改 native/testdata/生成输入。

已验证精确映射35条/85断言；其中区块 Filter 联合的一项错误义务已通过公共 API 正向编译、同一原版实际区块输入的正向执行、负向编译。其余4条虽然已有运行时对照，但9项类型义务尚未满足，所以继续未映射：旧 network 迁移3项、未知链名字面量1项、未知 ABI 事件名字面量1项、字符串 start/end 边界属性及后续调用4项。原始公共 RPC URL skip 保留，不提供假节点、不取消skip、不计通过。

真实运行时补充验证为两种数据库×缓存开关的8个case，零source credit。涵盖筛选后日志、重载事件顺序和解码、调用结果、收据、账户方向、区块interval/offset、setup、实际业务写入、进度和重复同步；另验证工厂出生前99被排除、同块出生转账5和后续7被索引、undefined工厂标识持久化。

已保留新增测试的错误记录：native-runtime-race-01误用旧进度API，编译失败；live-race-01在结果收集器无锁读取时触发race，原版39断言和实际值比较虽通过，整轮仍判失败。修复收集同步后的live-race-02通过。没有更改原版输入、setup、断言或skip。

当前边界仍包括9项原版类型义务、原版skip、完整静态DSL/字面量约束、transport函数适配、完整RPC订阅与自适应策略、全部事件/context/include公开形状、负数区块/interval到现有无符号调度器的支持、完整历史planner和多链调度。JSON动态适配器不是完整TypeScript类型等价证明。其余查询/API/CLI/dev/templates/SDK/simulation范围继续保留。

完整首轮在发现Hyperliquid对象身份断言缺口后主动停止，退出143；已完成1473个native叶用例、0失败/skip，normal未完整结束且race未启动。快照full-01-evidence保留原日志、原native测试和输入指纹；补上身份断言后须完整重跑，不能把该部分执行当作验收通过。

身份校验补齐后的live-race-03通过：39原版活动例、69实际构建调用、0差异，60次规范元数据身份检查，其中Hyperliquid配置/索引各1次。第二轮完整验收会在任何native Go测试前写入prepared-native-inputs.json，原版采集完成后的完整输入指纹必须贯穿normal/race与最终验收一致。

正式full02退出0。测试前记录的952文件指纹`ded57969d2f68ba7910400427e04ecae8ffc552be7f9f4400f9d776443a11a8e`贯穿normal/race开始、结束与最终汇总一致。35个映射原例的独立完整原版oracle也通过。桥将Go的(value,error)返回规范化为来源safe状态/BuildError JSON表示；这不宣称导出的来源safe-status API或完整原生错误类层次。
