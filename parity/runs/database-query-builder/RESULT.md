# 数据库查询构造器与主键验证

PostgreSQL 上的 6 条原始查询构造器用例和 4 条 Drizzle 主键用例全部通过。在查询构造器中，独立查询可以重试，事务内的单条语句将错误交给事务处理。原版注入两次连接故障的用例经过 7 次数据库操作，Go 上的顺序同样是 `BEGIN → BEGIN → SELECT → ROLLBACK → BEGIN → SELECT → COMMIT`。事务内返回实际借用的连接。主键解析保留属性名、物理列名以及复合主键的声明顺序。

| Go 测试范围 | 普通模式 | 竞态模式 |
| --- | ---: | ---: |
| 完整 schema 包 | 29 | 29 |
| 查询构造器、索引 ORM 和写入回归 | 79 | 79 |

两种模式均无失败或跳过。查询构造器的事务和重试断言在真实 PostgreSQL 上运行。PGlite 模式的原版测试有 6 条提前返回，只计入 4 条实际执行的主键用例。额外的上游探针和 Go 测试分别验证了 PGlite 回调中的写入可见性，以及 SQL 列名中缩写、数字和撇号的处理。

`store.NewSourceQueryBuilder` 借用已有后端。`Wrap` 用于可重放查询，标签可用于观察每次尝试。`Transaction` 中的 PostgreSQL 嵌套回调使用保存点。若数据库已经提交但客户端丢失确认，Go 返回 `store.ErrIndeterminateCommit`，测试验证数据库只收到一行。PGlite 的来源回调直接在原客户端上运行，需要原子写入时使用 `Backend.Begin`。

独立重建核对了 905 条来源声明。现有 667 条映射，233 条待移植，5 条属于退役数据库迁移。整个模块通过编译和静态检查。[源码标识、命令与结果摘要](results.json) · [上游全量结果](../public-provider-archives/upstream.json)
