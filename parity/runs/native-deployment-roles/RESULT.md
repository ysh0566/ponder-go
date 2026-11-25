# PostgreSQL permissions for a native Go deployment

The generated ERC-20 application supports `PONDER_DATABASE_MODE=existing` for indexing against a current, prepared namespace. Startup verifies the model and storage markers with `store.OpenExisting`. The default creation path still provisions a fresh deployment. Applications using a custom Go entry point can call the same Store API.

## Role exercise

`TestGeneratedDeploymentRoles` starts its own PostgreSQL 14.24 container, removes the PUBLIC database privileges and public-schema grants, and creates two independent login roles. The test runs the documented SQL from [the grants example](../../../docs/examples/postgres-writer.sql).

- The manager receives CONNECT and initially CREATE on the test database. It prepares two current deployments. It lists and publishes them through the normal CLI. After database CREATE is revoked, the manager switches an existing published schema to the prepared replacement, confirms its different data and prunes the retired deployment. Prune reports active and published deployments accurately. The role cannot drop a table in an unrelated schema.
- The writer receives schema USAGE, scoped row and sequence privileges, and a row-level policy for the multichain replay marker. A generated Go project uses this login to index an Anvil ERC-20 transfer. Both signed balances are checked through the stable views. After cold restart and a replacement block, both balances change from ±7 to ±9. The cross-chain store also creates and acknowledges its replay generation under this role.
- The writer cannot create a schema, read another deployment or an unrelated table, truncate or drop its own business table, publish views or prune the retired deployment. It cannot insert an arbitrary deployment metadata key, change the fingerprint or remove the deployment manifest. Publication and prune permission failures are checked by PostgreSQL SQLSTATE 42501.
- The grant script covers the current native store. Application tables have their own schema and DML requirements. The owner keeps its ownership of previously created objects when its database CREATE grant is revoked. A new deployment or a new published schema needs database CREATE again.

The store-engine crash tests also exercise the shared disposable PostgreSQL fixture. These are native acceptance cases and do not change the pinned upstream case inventory.

## Verification

Go 1.25.11 ordinary and race runs each passed all 12 leaf cases in the selection below. Each run had its own application and prune databases. The login exercise used an additional disposable PostgreSQL container with `PONDER_TEST_POSTGRES_ENGINE_IMAGE=postgres:14.24-bookworm`. The store selection also exercised PGlite 0.2.13; the role exercise used PostgreSQL with Anvil 1.8.5.

```sh
selection='^(TestGeneratedDeploymentRoles|TestGeneratedGoServe|TestGeneratedGoStartViews|TestDeploymentCommands|TestDeploymentSweep|TestInitGoProject|TestDatabaseEngineRecovery|TestOpenExistingDeployment|TestMultichainPartialPeerBlock)$'
go test -v -count=1 -timeout=15m -run "$selection" ./cmd/ponder ./store
go test -race -v -count=1 -timeout=15m -run "$selection" ./cmd/ponder ./store
go test -run '^$' ./...
go vet ./...
```

At upstream commit 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334, `vitest run --maxWorkers=1 src/database/actions.test.ts -t 'empty schema|live query notify trigger batches large payloads|createTriggers\('` passed five selected tests. The six unselected tests were filtered by name. Module-wide compile and vet passed, as did all 503 verification-tool checks. Whole-module ordinary and race runs for this tree follow the prior releases in the shared regression queue.
