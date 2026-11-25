# Continuous finality in native Go indexers

Go 1.25.11, two task-owned database backends (PostgreSQL 14.24 and PGlite 0.2.13), and Anvil 1.8.5 exercised the atomic runtime.

| Three-block comparison | Latest indexed | Finalized | Undo records |
| --- | ---: | ---: | ---: |
| Open-ended, PostgreSQL and PGlite | 3 | 3 | 0 |
| End block 3, PostgreSQL and PGlite | 3 | 3 | 0 |

The comparison reused the [earlier audit probe and commands](../native-go-ongoing-indexing/RESULT.md). With a zero-second window on the earlier revision, both open-ended workers retained three undo records and had no finalized row.

With a 12-second window, an Anvil chain mined blocks at offsets 10, 20 and 30. The worker finalized block 1 and retained the two newer undo records. A shorter fork removed block 3, then replaced it. When the chain forked again during a callback, the worker detected a parent mismatch, kept the old branch reversible, and acquired the canonical suffix. At height 5 the durable watermark was block 4 and the journal held only the write for block 5. On a cold reopen the same canonical rows were present and no callback ran again. A separate two-chain isolated test finalized one chain immediately while the other chain retained its own reversible writes.

| Checks | Commands |
| --- | --- |
| Atomic and staged workers, reorgs, factory, account and trace sources, isolated workers and outage recovery | `go test -count=1 -run '^(TestContinuousFinality\|TestIsolatedIndexer\|TestIsolatedBoundedCompletion\|TestApplicationRPCRecovery\|TestAnvilPostgresVertical\|TestAnvilPGliteVertical\|TestStagedAnvilVertical\|TestSourceConfigurationRuntime\|TestFactoryRuntime\|TestAccountFactoryRuntime\|TestContractTraceRuntime\|TestFilterWideRequestRuntime)$' ./runtime`, also with `-race` |
| Store, cancelled commits and staged lifecycle actions | `go test -count=1 -run '^(TestPostgresStore\|TestPGliteStore\|TestPGliteCancelledCommit\|TestUpstreamStagedActions\|TestUpstreamStagedDataActions)$' ./store`, also with `-race` |
| Module compilation and static checks | `go test -run '^$' ./...` and `go vet ./...` |

The new cases exercise current-format native Go behavior. The upstream inventory retains its existing counts.
