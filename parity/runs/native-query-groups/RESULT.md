# Boolean groups in native Go model queries

`orm.Or` and `orm.And` accept typed predicates for the same model. A single predicate keeps its meaning. Each group captures its operands, preserves the first invalid operand and keeps its SQL parentheses when nested. Repeated `Where` calls add outer AND conditions. Each comparison uses the existing column encoder and bound SQL parameters. Isolated applies its chain condition outside the groups and before pagination. [Wallet transfers in Go](../../../docs/USAGE.md#查询地址转入或转出的记录) · [SQL implementation](../../../store/indexing_select.go)

The Go checks exercise nested precedence, a reused group, nullable comparisons, quoted strings, sorting and offset pagination. They also cover a custom codec error inside a group, rejected mixed model types, malformed and cyclic low-level groups and a query where the second OR operand matches a row on another chain. [Predicate tests](../../../orm/query_test.go) · [Isolated two-chain case](../../../orm/join_test.go)

```sh
go test -count=1 ./orm
go test -race -count=1 ./orm
go test -run '^$' ./...
go vet ./...
```

## Verification results

The ORM package passed in ordinary and race modes on PostgreSQL 14.24 and PGlite 0.2.13. Earlier overlapping ORM runs recorded PGlite process exits during enum and codec tests. Both full ORM runs passed when executed serially. Selected callback binding, source configuration and Isolated database controls also passed in both modes. The documentation example ran on both engines in a generated ERC-721 project. With six transfer events, it returned the outgoing, incoming and self-transfer rows once each, in order. It excluded a transfer between two other addresses and both events outside the requested interval.

All 50 packages compiled and passed vet. The repository tooling passed 503 Node checks and 43 Python checks. At upstream commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, seven SQL, cache and nested-filter controls passed. The new Go callback assertions are supplemental. The module-wide ordinary and race regression runs separately.

```sh
go test -count=1 -run '^(TestSourceCallbackContext|TestDatabaseBindingCapabilities|TestDatabaseUnboundOperationsReject|TestDatabaseBindingRevocationJoins|TestORMIsolatedComposite)$' ./config ./indexing ./store
go test -race -count=1 -run '^(TestSourceCallbackContext|TestDatabaseBindingCapabilities|TestDatabaseUnboundOperationsReject|TestDatabaseBindingRevocationJoins|TestORMIsolatedComposite)$' ./config ./indexing ./store
# In packages/core of the pinned upstream checkout
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/indexing-store/index.test.ts src/indexing-store/cache.test.ts src/graphql/index.test.ts -t '^filter and/or$|^sql$|^sql followed by find$|^sql with error$|^flush\(\) (insert|update|encoding)$'
```
