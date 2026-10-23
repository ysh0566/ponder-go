# Lifecycle completion and eager ORM operations

The fixed-source lifecycle cases `migrate() with crash recovery reverts rows`
and `camelCase` now have exact native mappings. Their original test bodies,
contexts, fixtures, input expressions and all eight assertions are retained in
`parity/lifecycle-orm-completion-source-cases.json`. PostgreSQL is the primary
mapping and PGlite executes both companion cases. The original full suite still
cannot collect without `_test/generated.js`; native success is not oracle success.

Historical writes use `Actions.HistoricalBatch`. They enter the same database
capture trigger and durable undo journal as live writes. Initial checkpoint
metadata and pruning of a confirmed historical journal prefix commit together.
Default ordering rejects a safe claim if it crosses an unknown/unsafe chain's
global operation suffix; isolated ordering seals each chain independently.
No eventless/raw write bypass was introduced. Fault injection after real journal
DELETE and real process exits before/after COMMIT exercise metadata/pruning
atomicity. Server/host power loss is not covered.

An optional `LifecycleOptions.ViewsSchema` must be a child of the task's owned
indexing namespace. A persistent ownership comment prevents adopting an unrelated
schema. Alias views preserve physical camelCase column names and expose the
source-shaped metadata keys without the native lease token. Checkpoint statement
triggers forward to the alias channel independently of base live-query triggers.
Changing the alias setting for an existing build requires a new shadow.

The source `insert then` maps to synchronous Go success continuations over the
actual buffered insert operations. Its no-physical-table/no-flush transaction,
zero address, ALICE, 10n, one-element array and all three result assertions are
preserved. These native continuations do not implement JS Promise scheduling.

The source `sql` retains the original buffered insert, eager select, explicit
SAVEPOINT/ROLLBACK TO sequence, NULL error and duplicate-key error. The source
returns before the PGlite fixture, so this mapping has only a PG primary leaf.
`IndexingStore.SQLInsert` uses the existing guarded SQL/savepoint boundary;
`orm.Table.SQL().Insert` takes the real generated required-field insert type.
The source undefined balance obligation has a positive fixture and a negative
fixture that must fail the Go compiler. Supplemental typed SQL tests execute
both backends, check cache visibility after eager DML, and undo through the
normal business-table trigger journal.

This completes the currently discovered lifecycle records, not the full project.
See `parity/test-ledger.json`, `parity/feature-ledger.json` and `CONTINUATION.md`
for unimplemented source/runtime/cache/query/tooling work.
