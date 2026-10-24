# SQL 原例与生产缓存

M30完整模块验收通过：Go/race各1948、0失败/skip，PG206/PGlite203；21包146顶层函数，每模式50个不重复串行批次，默认timeout保留。实际发布以M30 publication receipt为准；此前已核验发布M29为6cca717。完整目标尚未完成。

本批补充SQL工具函数14个原始声明、19个原断言。完整未修改原文件独立通过14例，另以call-through记录38次真实调用，包括原Drizzle schema生成的8个关系查询SQL和三项并发校验。Go逐例执行相同SQL并比较完整关系集合/结果/错误；两个故意构造的40位哈希前缀碰撞也保留完整SHA256断言。原文件、setup、输入、断言和状态不变。原文件没有类型错误指令；这不代表原Node静态类型联合已完整移植。

`sqlquery.NewValidator`提供两个独立的LRU，默认容量一百万，使用完整SHA256键。首次校验失败保留具体错误，缓存命中后返回原版通用错误。失败插入与成功插入的淘汰边界保持源码行为。关系缓存返回具有真实身份和修改语义的有序`RelationSet`；`Values()`提供Go调用方的快照。现有`Relations()`便捷接口返回该快照。缓存和共享集合支持Go并发访问。

公开`Parse`/关系解析按JavaScript UTF-16单元限制5000长度，允许字节数更大的有效Unicode SQL。实际固定WASM和原生C解析器版本均为160001。原版将解析错误对象传给`Error`，消息为`[object Object]`；`SourceParseError`保留该公开消息，并通过`Unwrap`保留原生PG原因。受信任的`ParseStatement`保留详细诊断和现有无公开长度限制入口。

只读判断使用源码AST允许列表，跳过公开查询的函数/schema/递归附加校验。`Writer.SQL`现在使用该遍历决定ORM缓存flush/invalidate/clear。旧代码的DML/locking启发式将含SubLink的SELECT当作只读：真实双后端红灯显示数据库余额从1变为2，随后ORM仍返回1。相同回归在修复后返回2，后续ORM更新为3；insert、SQL更新、ORM更新的journal恰为3条。所有写入保持同一个事件事务，测试函数仅创建在任务新schema。

聚焦SQL79+HTTP2共81项race通过；另有9项SQL/ORM实际后端race通过。61个实际源码向量、缓存检查、双后端HTTP与上述回归均为补充，零source credit。HTTP首轮自有测试误用仅返回状态码的Go客户端观察错误正文；保留失败和旧测试快照，改为检查实际HTTP状态/正文后通过，原SQL/预期不变。985个原生输入在第二轮聚焦前后指纹相同。

准备工具显式生成原例采集、源码向量和SQL规则，再记录原生输入快照；Go测试只读取这些fixture。`sql-source-results.mjs`核对完整原版、采集、实际解析器和工具上下文，正式native通过仍依赖完整normal/race。已验证映射378/905，527未实现；全功能ORM/SQL/GraphQL/API/CLI/类型、历史与多链调度等仍在原始完整目标内。905条静态记录不构成动态穷尽证明。

首轮full01 normal1948通过、race1947通过/1租约失败，81份快照保留。原生250ms租约场景在准备helper中失败，根因尚未确定；相同985输入、25ms/250ms配置的单例5次和原批次2次race均通过。未改代码、超时或断言后重新执行完整full02，只有本完整轮次作为验收，不能拼接聚焦结果。

正式full02退出0；native Go开始前记录985个输入，指纹`82761e1cc11689adcba96e49c7e40dadac8bb88b42b076ca7c8dd000987d039a`贯穿normal/race前后与最终汇总一致。原版14例pristine/capture均重新执行通过。
