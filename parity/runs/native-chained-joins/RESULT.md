# Chained joins in Go indexing callbacks

`ThenInnerJoin` and `ThenLeftJoin` add a model to a typed join. Each equality can refer to a different preceding table or self-join alias. Each step produces `Joined[Previous, Next]`, with `*Next` for an optional match. The writer executes all joins, filters, ordering and pagination together inside its event transaction. Isolated queries limit the root to the event's chain and add a chain comparison to every join condition, preserving unmatched rows. [Usage example](../../../docs/USAGE.md#查询授权事件的所有者和当前额度) · [Go builder](../../../orm/join.go) · [SQL and row decoding](../../../store/indexing_join.go)

The native tests cover four appearances of the same model, composite equality predicates using different aliases, an inner join through a missing optional row, and an inner join through the original row even when two optional rows are missing. They also cover different model decoders, custom value codecs, read-only database access, callback binding revocation, buffered writes, rollback and the chain restriction when an identical key exists on another chain. The original two-table entry points retain custom executor overrides for both eager and paginated reads. [Native and compile-time cases](../../../orm/join_test.go)

## Approval query comparison

The documentation example uses the generated Go models from the `api-functions` template. Its supplemental reference query uses the pinned upstream example's actual table declarations and Drizzle on PostgreSQL. It joins an approval event to its owner account, then left joins the current allowance using both the account address and the event's spender. It selects events at or after timestamp 3, orders by descending timestamp and ascending event ID, and reads pages of three. Addresses in this table are abbreviated to their numeric suffix. The fixture uses full 20-byte hex addresses. [Pinned upstream schema](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/examples/feature-api-functions/ponder.schema.ts)

| Offset | Approval IDs | Owner suffixes | Current allowances |
| ---: | --- | --- | --- |
| 0 | e6, e5a, e5b | 1, 2, 1 | 110, 211, 111 |
| 3 | e4, e3 | 2, 3 | NULL, NULL |

The fixture includes a fourth owner without an account and an older event before the timestamp cutoff. The three accounts have current allowances for the owner/spender pairs (1,10), (1,11), (2,11) and (3,11). The complete page projections include the event ID, owner address and nullable allowance amount.

## Verification

```sh
go test -p 1 -count=1 -v ./orm
go test -race -p 1 -count=1 -v ./orm
go test -p 1 -count=1 -run '^(TestSourceCallbackContext|TestDatabaseBindingCapabilities|TestDatabaseUnboundOperationsReject|TestDatabaseBindingRevocationJoins|TestORMIsolatedComposite)$' ./config ./indexing ./store
go test -race -p 1 -count=1 -run '^(TestSourceCallbackContext|TestDatabaseBindingCapabilities|TestDatabaseUnboundOperationsReject|TestDatabaseBindingRevocationJoins|TestORMIsolatedComposite)$' ./config ./indexing ./store
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

The original upstream controls use this command from `packages/core` at revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/indexing-store/index.test.ts src/indexing-store/cache.test.ts src/graphql/index.test.ts -t '^order by$|^filter and/or$|^sql$|^sql followed by find$|^sql with error$|^flush\(\) (insert|update|encoding)$'
```

The full ORM package and the selected configuration, callback-binding and store controls passed in ordinary and race modes on PostgreSQL 14.24 and PGlite 0.2.13. The generated Go project matched the five Drizzle projections across both pages on each database. Eight original upstream controls passed on PostgreSQL. The comparison and chained-join tests are supplemental. All 50 Go packages compiled and vet passed. Repository tooling passed 503 Node and 43 Python checks. Full-module ordinary and race regressions run in the separate exact-commit queue.
