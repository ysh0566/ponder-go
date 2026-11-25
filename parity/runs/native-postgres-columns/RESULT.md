# PostgreSQL column types in Go and GraphQL

The Go schema and model generator accept `cidr`, `inet`, `interval`, `macaddr`, `macaddr8` and `time`. Each scalar maps to a Go string. The database validates values and supplies its canonical output format. SQL arrays use the ordinary nullable-element model. The native GraphQL API exposes the canonical text of a point or line. Geometry comparisons, including cursor sorting, use that text. Generated Go point and line fields retain their coordinate tuples.

The following checks ran on Go 1.25.11 with PostgreSQL 14.24 and PGlite 0.2.13. Each affected package passed its ordinary and race-enabled tests.

```sh
go test -count=1 ./graphql ./schema ./orm
go test -race -count=1 ./graphql ./schema ./orm
go test -count=1 ./store
go test -race -count=1 ./store
go test -run '^$' ./...
go vet ./...
```

- `TestNativePostgresColumnTypes` generates string fields for all six scalars and checks direct inserts, canonical reads, an interval update and rollback. Its GraphQL query filters on all six types and checks membership and output of an `inet[]` value on both engines.
- `TestGeometricColumns` uses direct Go writer inserts and updates with coordinate slices, a fixed array and text inputs. On both engines the rollback restores the original point, line and point array. The test reads canonical strings, exercises equality, inclusion and array membership, then follows a cursor to a row with a null line.
- A separate consumer ran `go run ./cmd/ponder codegen --schema "$fixture/schema.json" --typed --package machines --out "$fixture/model.go"` for the six new types and their arrays, and compiled the output with `go test "$fixture/model.go"`.

These native cases supplement the pinned GraphQL tests. Upstream test mapping counts are unchanged. [Test source](../../../graphql/native_column_types_test.go)
