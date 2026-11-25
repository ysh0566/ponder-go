# Gateway errors and log pagination

The Go HTTP client preserves bounded JSON-RPC error details from unsuccessful HTTP responses. A matching provider hint can now split the log query even when a caller wraps the error. The historical runtime can retry a two-block interval as two single-block queries. If the provider still rejects a single block, the caller receives the original status.

| Original provider case | Observed response | Verified behavior |
| --- | --- | --- |
| Base block range | OP Labs proxyd returned HTTP 503 and RPC code `-32011` | Bisected the original 20,001-block request |
| Swell block range | OP Labs proxyd returned HTTP 400 and RPC code `-32602` | Split at an endpoint difference of 1,000 |
| Avalanche block range | Coreth returned RPC code `-32000` | Split into 2,048 blocks plus one final block |
| Ankr TAC request range | TAC returned RPC code `-32000` | Split at an endpoint difference of 10,000 |

All four native cases exercised live local listeners and captured responses. The current Go ledger maps 566 of 905 original declarations, with 334 pending implementation and five historical database migration cases outside the current contract. [Native results and source identities](results.json)

The complete source inventory records **876 passed, 17 failed, 10 skipped and two todo**. This phase reran each utils partition against the pinned sources and combined the reports with the verified core, client, React and scaffolding runs. Both original Avalanche and TAC test files now pass against local range-policy nodes and verified mainnet data. Swell’s historical success assertion still expects 58 real mainnet logs and the local chain returned zero. The inventory lists every outstanding failure. [Original results](upstream.json)

The range-policy checks used AvalancheGo v1.13.3 with Coreth v0.15.3-rc.1 and TAC v1.6.4. Both original oversized requests and shifted requests were sent directly to those nodes. The archived mainnet queries returned 3,135 and 185 logs respectively. [Commands and network identities](../../../docs/HISTORICAL_LOG_RPC.md#apply-live-coreth-and-tac-range-limits-to-archived-history)

For the complete native package run, `tools/verify.sh` prepares the historical type fixtures before starting Go tests. The 12 positive and negative type directives bind the executable fixtures to the current sources, including `rpc/client.go`. A focused run in a separate checkout also needs those generated files.

The complete RPC and runtime packages passed in both normal and race modes. Each mode executed 112 top-level tests and 861 leaf cases with zero failures or skips. Both runs used the same prepared native inputs and separate PostgreSQL clusters. The module build, go vet, 500 Node checks and 37 Python tests also passed.
