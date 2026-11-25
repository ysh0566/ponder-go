# Live provider policy assertions

The Go suite covers the last 17 active declarations in scope, including all 62 original assertions across 15 source files. The generator reads the pinned TypeScript tests and viem error dispatch. The native tests use the original HTTP requests, error classes, message fragments, retry plans and log counts.

| Execution | Passed | Failed | Skipped |
| --- | ---: | ---: | ---: |
| Go on self-hosted nodes | 0 | 17 | 0 |
| Go with the race detector on the same nodes | 0 | 17 | 0 |
| Original Vitest declarations on the same nodes | 0 | 17 | 0 |
| Go with original public URLs and available provider credentials | 0 | 9 | 8 |

Self-hosted nodes served the available archive indexes. Optimism returned 3,478 logs for the request whose source assertion expects a gateway rejection. The current 1RPC gateway returned a different range limit. The private endpoints for eight declarations were unavailable during the public run. On Swell, a local Anvil 1.8.5 chain at the required height returned zero of the 58 expected canonical logs. The additional public endpoints checked did not supply that history. The original assertions remain intact.

The separately run offline RPC package passed 237 leaf tests in ordinary and race mode. Each offline run skipped the 17 opt-in live children, which were exercised separately above. The entire module built and passed vet. Verification-tool checks passed 501 Node tests and 40 Python tests. The archive-dependent controls used 52 restored inputs verified against the archive inventory.

Across the fixed upstream revision, 888 declarations have target mappings, including 883 Go ports and five retained SDK cases. Upstream marks 10 others skipped and two as TODO. Five exercise retired database migrations. The full upstream inventory records 875 passed, 18 failed, 10 skipped and two TODO. The extra upstream failure concerns 1RPC history, which has a separate passing Go archive test. The checked-in target ledger currently carries results from multiple stages for 647 mappings, with 630 passing and 17 failing.

The instructions cover endpoint selection and the opt-in commands. The machine-readable report includes each source ID, assertion digest, result and observed response details. [Running the tests](../../../docs/HISTORICAL_LOG_RPC.md) · [Per-case results](results.json) · [Complete upstream inventory](../public-provider-archives/upstream.json)
