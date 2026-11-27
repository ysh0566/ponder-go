# Filtering and paging joined model rows from Go

`QueryInnerJoin` and `QueryLeftJoin` expose both inputs for `JoinColumn`. Bound columns retain their Go value type and insert encoder. Filters from either input can form one Boolean expression. SQL orders and pages the joined rows inside the callback transaction. In Isolated mode the right side must match the left chain in the join condition, and an outer WHERE enforces the event chain. [API example](../../../docs/USAGE.md#按发送方余额分页查询转账) · [Store implementation](../../../store/indexing_join.go)

The native cases cover SQL NULL on an unmatched right side, mixed-side OR predicates, byte and JSONB filters, reused pages, zero limit, negative offset and invalid side or column names. A self join uses a different ID on each side, including in a read-only callback. Additional checks cover failed custom encoding, revoked leases, an Isolated peer with the same key, an executor implementing only the original eager API and a caller passing the wrong model's column. [Two-backend and compile checks](../../../orm/join_test.go)

```sh
go test -count=1 ./orm
go test -race -count=1 ./orm
go test -run '^$' ./...
go vet ./...
```

## Verification results

The final ORM package passed in ordinary and race modes on PostgreSQL 14.24 and PGlite 0.2.13. A repeat ordinary run during full-module compilation lost two PGlite processes in the existing runtime and single-table cases. The final ORM runs were executed after the build and vet completed. The source configuration, callback bindings and Isolated storage controls passed in both modes. All 50 Go packages compiled and passed vet. The repository tooling passed 503 Node checks and 43 Python checks. The full-module ordinary and race regression runs separately.

The generated `api-functions` project ran the usage example on each backend. The fixture contains 12 accounts with addresses ending in integers 1 through 12 and balances of 100 times the integer. Thirteen transfers use the corresponding sender addresses, amounts of 19 times the integer, timestamps 1 through 13 and IDs `t01` through `t13`. The thirteenth sender has no account. The SELECT from the pinned upstream `/whale-transfers` example ran against temporary PostgreSQL tables with the same senders, balances and amounts. Both Go backends matched all ten ordered sender, balance and amount projections. With a minimum timestamp of 3 and an offset of 2, both returned eight transfers in order from `t10` through `t03`. [Upstream query](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/examples/feature-api-functions/src/api/index.ts#L34-L49)

Eight original SQL, cache, ordering and nested-filter controls also passed. The native join assertions are supplemental.

```sh
go test -count=1 -run '^(TestSourceCallbackContext|TestDatabaseBindingCapabilities|TestDatabaseUnboundOperationsReject|TestDatabaseBindingRevocationJoins|TestORMIsolatedComposite)$' ./config ./indexing ./store
go test -race -count=1 -run '^(TestSourceCallbackContext|TestDatabaseBindingCapabilities|TestDatabaseUnboundOperationsReject|TestDatabaseBindingRevocationJoins|TestORMIsolatedComposite)$' ./config ./indexing ./store
# From packages/core at the pinned upstream commit
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/indexing-store/index.test.ts src/indexing-store/cache.test.ts src/graphql/index.test.ts -t '^order by$|^filter and/or$|^sql$|^sql followed by find$|^sql with error$|^flush\(\) (insert|update|encoding)$'
```
