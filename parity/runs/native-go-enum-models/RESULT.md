# Database enums in Go indexer models

This scope covers plain Go structs, typed ORM code generation and transaction-bound callback tables. All three use the schema's declared label order. Each enum has a distinct comparable Go type, a constructor per label and a parser for external strings. Its zero value represents the first declared label. The generated types retain the exact SQL labels in JSON and database writes.

## Native checks

The enum fixture uses two enum types that share the label `BUY`. The compiler checks cover raw string literals, values from the wrong enum, explicit cross-enum conversion and attempts to set a private ordinal from another package. The runtime fixture covers an enum key, SQL NULL, an array with NULL and empty-label elements, a Unicode label, colliding constructor names, an update and JSON round trips. The CLI tests compile and execute the generated code in all three modes. [Tests](../../../orm/enum_models_test.go) · [Schema](../../../orm/testdata/enums/model.json)

Run with Go 1.25.11, a new task-owned PostgreSQL 14.24 instance and the PGlite bridge.

```sh
selection='^(TestGeneratedEnum.*|TestGeneratedContainerModels|TestGenerateModels)$'
go test -p 1 -count=1 -run "$selection" ./orm
go test -p 1 -race -count=1 -run "$selection" ./orm
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

Both focused executions passed all nine leaf cases without skips. Running the full ORM package separately passed all 24 leaf cases per mode, including generated caller diagnostics, typed conflicts, nullable values and eager SQL. Running the existing CLI codegen selection passed all five cases per mode. The actual Friendtech schema generated a callback database that compiled. All 50 Go packages compiled with the empty test selection, and vet passed. The module-wide ordinary and race suites are tracked separately and their results are pending for this revision.

```sh
go test -p 1 -count=1 ./orm
go test -p 1 -race -count=1 ./orm
go test -p 1 -count=1 -run '^TestCodegen' ./cmd/ponder
go test -p 1 -race -count=1 -run '^TestCodegen' ./cmd/ponder
```

The repository checks passed 499 Node tests in the tool and indexing-service groups, four additional loopback tests and 43 Python tests.

## Pinned upstream oracle

In `packages/core` at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, the enum selection passed all five matching cases across schema build, migration, the indexing store and GraphQL (including enum arrays and an enum primary key). Vitest filtered out 106 unrelated tests.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 \
  src/graphql/index.test.ts src/indexing-store/index.test.ts \
  src/build/schema.test.ts src/database/index.test.ts -t enum
```
