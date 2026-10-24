# 原生类型化来源配置

M29完整模块验收通过：Go/race各1865、0失败/skip，PG204/PGlite201；21包141顶层函数，每模式50个不重复串行批次。实际发布以M29 publication receipt为准；此前已核验远端M28为80bc222。本批仍不是完整Go移植目标完成。

`SourceTypedContract[S,E]`保留原始边界类型；`CreateTypedSourceConfiguration`、`BuildTypedSourceConfig`和`SafeBuildTypedSourceIndexing`分别约束结构与`SourceBlockBound`，并调用实际来源构建器/客户端。容器`SourceTypedConfiguration[C]`本身不加约束，使忽略构造错误的旧值仍可表示，从而能在两个消费入口分别证明原始错误。`Network`代替`Chain`触发实际结构约束，而非无关名义类型错误。数值（包括NaN）、`SourceLatestBound`和`SourceUndefinedBound`直接降低到来源值，不经过会丢失NaN/undefined的JSON往返。

`SourceOnChain`与`SourceOnChains`支持快捷链和有序覆盖。`SourceOption`区分省略与显式零值/null。Go零值链选择会返回错误；完整原生参数/上下文字面量DSL仍有更大范围。

`config.GenerateSourceContractBindings`与`cmd/source-codegen`从实际链声明和ABI生成闭合链/事件引用、带私有索引的类型和实际生产构造器。生成结果供真实builder/runtime使用，不是只用于负编译的玩具类型。未知Mainnetz/Event2既无法从实际声明中生成，也不能把任意字符串直接赋给生成的Chain/Event字段。当前生成器面向单ABI来源、快捷链、单事件filter；链覆盖、多个filter及完整TypeScript类型等价不由此证明。Go零值和底层动态适配器的差异继续保留。

四个原例的九项义务分别为network字段/两消费入口3项、未知链1项、未知ABI事件1项、字符串start/end属性及两消费入口4项。每项都有生产API的正向编译、同一原版存活Anvil上的实际正向运行，以及特定负向诊断。正向事件控制只将Event2改成Event0，原始args.arg="0x"不变；它是合法事件引用，但仍应触发剩余的bytes32长度错误。实际固定viem返回AbiEncodingBytesSizeMismatchError，Go控制验证对应长度失败后成功退出。不能通过填充参数来伪造正向成功。

原版config.test.ts始终不改文件、输入、setup、断言或skip；39active/92断言、69实际native调用、原skip1保持独立。九项附加正向控制不是新增source credit。M28既有Filter联合收窄1项加上本批9项，已验证全部10项原配置错误义务。

聚焦真实运行时12场景覆盖PostgreSQL/PGlite×缓存开关、数值/latest/NaN/undefined和生成引用的topics/实际handler/业务写入/重复同步。补充零链选择验证防止接口nil断言panic，零source credit。

`tools/config-typed-compile-fixtures.mjs`与`tools/config-literal-compile-fixtures.mjs`仅在显式--write准备阶段更新native fixture；Go测试期间只校验其内容，临时二进制/诊断源码写入.parity-build，证据写parity/runs。首轮新工具曾在聚焦Go测试中重写确定性fixture，已修正，不能用于冻结输入验收。修正后Go前后输入指纹相同。新增事件正向控制最初过窄匹配bytes32字样，导致1项控制失败；实际原始0x长度错误已触发，保留失败并改为对应的长度类别检查，重跑9项全部通过。早期输入解码器错误与失败日志也保留。

完整目标仍包括其他541条静态记录、完整类型/transport/context/include、历史planner、多链/实时调度、查询/API/CLI/dev/templates/SDK/simulation。静态905记录并非动态穷尽证明。原公共RPC skip保留，未替换、未执行、未计通过。

正式full01退出0，测试前977文件指纹`12b021ae80a8fc75e9c90a9860e082153b6ec7328b4b5199c86d42e135f0a5c0`贯穿normal/race开始、结束与最终汇总一致。累计364/905精确映射且所有映射原版oracle通过。
