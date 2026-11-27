# Typed single-table queries in Go callbacks

The generated column descriptors accept native Go values and pass them through the column's insert encoder. Each bound table provides `Query`, with AND conditions, comparisons and NULL tests, multiple sort keys, limits and offsets. The query runs through the active indexing transaction on PostgreSQL and PGlite. The store checks the model's physical column names and binds comparison values as SQL parameters. Isolated queries add the event chain before ordering and applying the limit. [Usage](../../../docs/USAGE.md#在-go-回调中过滤和分页) · [Implementation](../../../store/indexing_select.go)

The native tests exercise buffered inserts, independent page definitions, tied sort keys, a zero limit, invalid bounds, empty pages, quoted text, SQL NULL and deletion by reorg. Callback tests cover read-only access, revoked leases, failing codecs and snapshotting a custom byte slice and nullable array. JSONB filtering preserves a number above the JavaScript safe integer range. The compile checks reject a mismatched value type and another row type's filters and sort keys. [Queries and codecs](../../../orm/query_test.go) · [Isolated query with duplicate keys across chains](../../../orm/join_test.go)

```sh
go test -count=1 ./orm
go test -race -count=1 ./orm
go test -run '^$' ./...
go vet ./...
```

## Verification results

The full ORM package passed in ordinary and race modes on PostgreSQL 14.24 and PGlite 0.2.13. This includes literal comparisons for enum arrays and custom codecs, enum ordering, and type checks for the generated API. Three control packages passed their configuration, callback-binding and Isolated storage cases in both modes. An earlier concurrent run lost the PGlite transport during store initialization, and a fixture comparison ran while models were being regenerated. The final runs used the regenerated fixtures and passed both cases.

The usage example ran in a generated ERC-721 project on both backends. Of 24 tokens, one belonged to a different holder. The page after the first 20 matches contained token IDs 22, 23 and 24.

All 50 packages compiled and passed vet. The verification-tool suites passed 503 Node checks and 43 Python checks. Six SQL and cache flush controls passed in pinned Ponder 0.18.2. The new native assertions are supplemental. The full-module ordinary and race regression runs separately.

```sh
go test -count=1 -run '^(TestSourceCallbackContext|TestDatabaseBindingCapabilities|TestDatabaseUnboundOperationsReject|TestDatabaseBindingRevocationJoins|TestORMIsolatedComposite)$' ./config ./indexing ./store
go test -race -count=1 -run '^(TestSourceCallbackContext|TestDatabaseBindingCapabilities|TestDatabaseUnboundOperationsReject|TestDatabaseBindingRevocationJoins|TestORMIsolatedComposite)$' ./config ./indexing ./store
# From packages/core at the pinned upstream revision
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/indexing-store/index.test.ts src/indexing-store/cache.test.ts -t '^sql$|^sql followed by find$|^sql with error$|^flush\(\) (insert|update|encoding)$'
```
