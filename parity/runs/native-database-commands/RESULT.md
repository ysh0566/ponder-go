# PostgreSQL deployment commands

Go 1.25.11 and PostgreSQL 14.24 exercised the public CLI. Two physical deployments held different balances. Publishing and republishing the first namespace produced stable views. A rolled-back write emitted no alias notification, and the next committed write delivered the changed table name. Publishing the second deployment changed the balance's SQL type. A dependent view and a manually added name collision each stopped the switch and preserved the old data. Removing the conflicts allowed the switch.

The prune command preserved a live atomic deployment, a staged writer with a current lease, a published deployment and an unregistered schema. A dependency in a third namespace and an unrelated table using a reserved-looking name each rolled back the complete attempted removal. After closing the writer and removing these dependencies, targeted prune succeeded and retry returned `absent`. Terminating the test store's dedicated session fenced further writes and cached RPC writes from its old handle. A fresh backend reopened the deployment and committed successfully.

A separate empty test database contained exactly two inactive deployments. Untargeted prune removed both in schema order. The test database was discarded after both modes. No other database was scanned by that test.

| Go commands | Outcome |
| --- | --- |
| `go test -count=1 -v ./cmd/ponder` and the same with `-race` | Passed, including the targeted command and the full sweep |
| `go test -count=1 -run '^(TestViewsSchemaBoundaries\|TestStoreFormatMarkers\|TestUpstreamDatabaseLifecycle\|TestLifecycleQueuedHeartbeat\|TestUpstreamRemainingLifecycle)$' ./store` and the same with `-race` | Passed on PostgreSQL and PGlite |
| `go test -count=1 -run 'Test(Isolated\|Application)' ./runtime` and the same with `-race` | Passed |
| `go vet ./...` and `go test -run '^$' ./...` | Passed across the module |

Set `PONDER_TEST_POSTGRES` to a task test database and `PONDER_TEST_PRUNE_DATABASE` to a different, empty test database named with the prefix `ponder_prune_test_` to reproduce the CLI checks. The source inventory retains its existing totals. [Usage](../../../docs/USAGE.md#发布查询视图与清理部署)
