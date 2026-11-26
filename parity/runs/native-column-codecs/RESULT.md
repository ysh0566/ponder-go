# Go models with application-defined column codecs

Three CLI modes generate application-supplied Go types over existing database column types. The typed table and callback database modes encode keys, inserts and updates and decode returned rows. Arrays apply the codec to each non-NULL element. The application type controls the row's JSON representation.

Tests compile actual CLI output in plain, typed and callback modes and compare the callback output with the checked-in model. PostgreSQL 14.24 and PGlite integration tests cover a custom primary key, snapshots of mutable bytes and arrays, NULL elements, an empty array, nullable zero, update callbacks, revert, malformed stored values and a batch with an invalid second row. The invalid batch leaves the first row absent.

Go 1.25.11 passed all six focused leaves, all 30 ORM leaves and all five existing CLI codegen leaves in each of ordinary and race modes, with zero skips. The focused cases are included in the 30 ORM leaves. All 50 packages compiled and passed go vet. The repository verification tools passed 499 Node cases, four loopback cases and 43 Python cases.

```sh
selection='^(TestGeneratedCustomColumnModels|TestCustomColumnCodegen|TestCustomColumnCLIModes)$'
go test -p 1 -count=1 -timeout=35m -v -run "$selection" ./orm
go test -p 1 -count=1 -timeout=35m -v -race -run "$selection" ./orm
go test -p 1 -count=1 -timeout=20m -v ./orm
go test -p 1 -count=1 -timeout=20m -v -race ./orm
go test -p 1 -count=1 -timeout=20m -v -run '^TestCodegen' ./cmd/ponder
go test -p 1 -count=1 -timeout=20m -v -race -run '^TestCodegen' ./cmd/ponder
go test -p 1 -run '^$' ./...
go vet -p 1 ./...
```

Both cases in the pinned upstream Ponder 0.18.2 indexing-store utility test passed. Run in the upstream packages/core directory.

```sh
node_modules/.bin/vitest run --maxWorkers=1 src/indexing-store/utils.test.ts
```

These native tests supplement the upstream mappings. Full-module ordinary and race results for this revision are pending.
