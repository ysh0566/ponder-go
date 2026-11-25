# 独立 Go 项目

CLI 和生成的模型使用公开模块路径。Go 模块包含实际使用的 SQL 解析器和 GraphQL 解析器。独立索引器依赖同一版本的 SDK，PGlite 的 Node 桥接脚本编入 Go 程序。

- 在独立临时目录用 Go 1.25.11 执行 `go install github.com/ysh0566/ponder-go/cmd/ponder@<revision>`。使用本地 Git 镜像提供待发布提交，生成项目后关闭 Go workspace，执行 `go mod tidy`、`go build -trimpath`、`ponder codegen --typed` 和 `go test .`。依赖清单包含该提交的 SDK 伪版本。
- 独立可执行文件对接 PostgreSQL 14.24 和 Anvil 1.8.5，索引部署交易中的 Transfer 日志；SQL 查询返回部署地址 25、零地址 -25。关闭上游环境变量，从项目安装的 PGlite 0.2.13 打开内存库，执行 SELECT 得到 42。
- `go test ./cmd/ponder ./graphql ./third_party/pg_query_go/...` 及相同包的 `go test -race` 通过。`go test -run '^$' ./...` 编译各包与测试；`go vet ./...` 通过。ORM 和 callback 的外部调用方编译测试通过。
- 验证工具的 503 项 Node 测试，以及 Python 的 40 项 unittest 和 3 项 observer 测试通过。历史断言使用校验过的归档输入。此阶段仅记录独立项目与相关模块的验证。
