# Complete upstream run on the self-hosted archive profile

Ponder 0.18.2 produced **872 passed, 21 failed, 10 skipped and 2 todo** across 905 original declarations. The checker verified the SHA-256 of all 104 source files at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334` and matched the suite path, expanded title, declaration multiplicity and source state.

| Partition | Files | Passed | Failed | Skipped | Todo |
| --- | ---: | ---: | ---: | ---: | ---: |
| Core runtime on PostgreSQL 14.24 | 57 | 723 | 0 | 6 | 1 |
| Same core runtime on PGlite | 57 | 723 | 0 | 6 | 1 |
| Core types | 6 | 69 | 0 | 0 | 0 |
| Client, React types and create-ponder | 3 | 5 | 0 | 0 | 0 |
| Utils on nine loopback listeners | 19 | 22 | 18 | 1 | 0 |
| Other utils runtime files, including original public URLs | 17 | 50 | 3 | 3 | 1 |
| Utils types | 2 | 3 | 0 | 0 | 0 |

Every core runtime declaration had the same outcome on both backends and counts once in the total. All 18 declarations configured through the eight private-provider environment variables ran on the local listeners. Twenty unresolved declarations check service-specific errors and one requires 58 historical Swell mainnet logs. The profile exercises local archive behavior for the redirected tests. The supplemental node-software runs have their own results in the previous RPC milestones.

The first complete pass produced 870 passed, 23 failed, 10 skipped and 2 todo. The PostgreSQL crash-recovery lock assertion failed once and passed when all 57 files reran against a fresh server. The public Tron response-size assertion failed once and passed in a second complete utils run. Both attempts are identified in the evidence. The CLI also failed in an initial direct Vitest launch with “Undetectable package manager”. It passed using the upstream `pnpm test` script.

During code review, a further run with an inherited HTTP proxy and an empty proxy bypass list reproduced every outcome in the 19 loopback files after explicitly disabling proxy use for those files.

The repository’s Node controls passed 500 checks after restoring their verified historical inputs and supplying the three fixture-specific entry points. The Python suite passed 34 tests. The 1,338 native verification inputs match the preceding phase and formal port coverage remains 543 of 905.

[Final report and unresolved declarations](results.json) · [First attempt evidence](first-attempt.json) · [Reproduction](../../../docs/HISTORICAL_LOG_RPC.md#check-the-complete-upstream-inventory)
