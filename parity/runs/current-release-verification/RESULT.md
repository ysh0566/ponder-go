# Complete upstream and mapped target verification

The upstream run covers the 905 original declarations at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Every active declaration executed. The 57 runtime core files ran on fresh PostgreSQL 14.24 and PGlite databases. The reconciliation counts each declaration once across backends.

| Upstream result | Declarations |
| --- | ---: |
| Passed | 876 |
| Failed | 17 |
| Skipped by upstream | 10 |
| Upstream TODO | 2 |

Both database backends passed 723 core runtime declarations, skipped six and reported one TODO. All 69 core type declarations, three client cases, the React type case and the project creation case passed. Utils contributed 79 passes, 17 failures, four skips and one TODO. All 17 failures match the provider-policy declarations in the separate native live run.

The complete 1RPC file ran against the Ethereum archive in this profile. It received the expected nine UNI logs and failed the original gateway-limit assertion. The direct vendor run and its 50-block policy observation remain available. Swell, Base, Avalanche and TAC used local nodes or gateways for their respective policy checks. The Swell development chain had no canonical logs to serve for the requested historical range.

[Per-file partitions and source IDs](upstream.json) · [Native live provider observations](../provider-policy-assertions/results.json)

## Mapped targets

At `de89be1b8ac57b013a406a5591da2838922bcb7a`, ordinary and race execution covered the 94 top-level Go tests containing all 883 Go mappings across 23 packages. In each mode, 866 source-mapped cases passed offline. All 17 opt-in cases had separate failing executions on self-hosted nodes. The five retained SDK cases passed against the browser, client and React packages. The ledger records 871 passing targets and 17 failing targets. Every active declaration within current database scope has an observed target outcome.

Each focused Go mode passed 1,197 leaf tests and skipped the 17 separately verified live children. The two modes produced identical leaf results. Both tests associated with a backend-specific mapping were checked where the mapping identifies a companion. Go build and vet cover the complete module. The verification tools passed 502 Node tests and 40 Python tests. Separate module-wide regression runs include the supplemental recovery and historical synchronization tests.

## Static checks

The pinned TypeScript compiler accepted the original GraphQL, copy and database declaration files. Removing their 29 expected-error comments produced diagnostics at all 29 original locations. For GraphQL, an independent Go program compiled after checking the dynamic response maps. The Go compiler rejected direct field access at all 11 corresponding positions across seven cases. The mapped row-copy and typed database tests exercised their own compile and generation controls in ordinary and race modes.

The verification inputs contained 1,480 native files. The compiler report and both Go modes used the same SHA-256 fingerprint. The source IDs, original report hashes and per-mode results are recorded below. [Mapped test results](mapped-tests.json) · [Type diagnostics](query-storage-types.json)

The compiler checks run with `node tools/query-storage-types.mjs` after the native inputs are prepared by `sh tools/verify.sh`. After collecting these results, the phase removed 3,371,810,816 bytes of temporary dependencies, node binaries, restored archive inputs and test workspaces.
