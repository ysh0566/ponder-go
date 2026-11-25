# Go multi-chain indexing

Two Anvil chains (IDs 101 and 202) used the same named Go block callback and an isolated schema with a composite primary key. One RPC started offline. The other chain indexed independently, and the application became ready after both chains caught up. The first chain then reverted to its snapshot before the first included block. Its two old rows were removed and one replacement row was inserted. The other chain retained its original hashes. A fresh store and application reopened the namespace with no duplicate callbacks. This ran on PostgreSQL 14.24 and PGlite 0.2.13.

A separate PGlite fixture mined past the configured range on two chains. Both workers finished at the selected finalized end height. The managed multichain adapter also processed six callbacks across two chains and reported readiness through the common application interface on each backend.

| Command | Coverage |
| --- | --- |
| `go test -count=1 -run 'Test(Isolated\|Application)' ./runtime` and the same command with `-race` | Isolated reorg, outage, restart, bounded workers and application lifecycle |
| `go test -count=1 -run 'Test(ManagedApplicationIndexer\|RuntimeBoundedLifecycle)$' ./historical` | Managed runner readiness and bounded recovery |
| `go test -race -count=1 -run '^TestManagedApplicationIndexer$' ./historical` | Common application interface on both backends |
| `go test -run '^$' ./...` | Compile module packages and tests |

These native tests use Go 1.25.11 and Anvil 1.8.5. They do not change the upstream test inventory.
