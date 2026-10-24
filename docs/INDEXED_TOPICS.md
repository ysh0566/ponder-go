# Indexed topics源码与运行时验证

M31完整验证退出0：Go/race各2030，PG222/PGlite219，0失败/skip；21包149顶层函数，每模式50个不重复批次，默认timeout保留。精确映射仍为378/905，527静态记录未实现；本轮82个补充检查新增source credit为0。

生成器实际通过CJS调用viem，fixture原记录相应ESM源码。完整验证后追加只读artifact审计：实际CJS/ESM两套helper各执行同样50个向量，topics及JS错误均与既有fixture一致，并记录实际入口与两套文件hash；没有改写native fixture，仍只计50个不同向量。

实际固定viem2.35.0编码器的50个调用覆盖bytes32/address/uint256/string/bytes，各自保留missing、null、undefined、位置参数null/undefined、空OR、value、OR value/null/undefined。undefined显式标记并还原，未在JSON传输中替换成null。源码文件、包、lock及生成器hash记录在fixture；完整Go执行前重新生成，Go测试只读。

标量null/undefined成为wildcard。OR元素经过原编码器：静态类型的nullish拒绝，string/bytes的null编码UTF8 `null`后hash，undefined编码空字节后hash。Go比较完整topic输出和错误是否发生；未宣称全部JS强制转换、错误类或错误文案等价。

旧M30生产代码在4个真实undefined场景失败；输入指纹986/ecccd53c5bf10d5df1854dc19ae6c82c317b39324b62ea772d9cf12f1cc27b12由冻结M30的985输入加未改新测试精确重建。最小标量修复后的runtime16通过；首40源码向量另暴露OR string null/undefined两失败，旧fixture/生成器/测试/标量修复快照保留。扩展bytes后50通过，32个完整runtime场景通过。

真实runtime包含PG/PGlite、cache开关、两种动态类型和scalar controls：原生typedSource构造与消费，实际编译topics、bloom、RPC、receipt、handler写表、client身份和再次Sync无重复。每个回归由真实Anvil EVM产生日志；匹配的null/undefined OR hash分别写42/7，scalar wildcard写两条总49。

正式完整轮次在Go之前准备989输入，指纹`9b6fe9b1dc8b141af45e676383455830afa40121cf33b85915b413a00109100a`贯穿normal/race前后与汇总一致。见batch-30.json、batch-30-evidence/、topics-m31/preflight-01.json和focused-results.json。M30曾出现的原生250ms租约失败证据和根因未确定记录仍保留；M31没有修改租约实现或测试配置。

完整ABI/typedDSL/transport/context/include/history/多链/实时/query/GraphQL/API/CLI/dev/templates/SDK/simulation仍在范围内。
