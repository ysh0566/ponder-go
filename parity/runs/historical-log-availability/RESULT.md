# Historical log availability on three chains

The original Celo, Blast and Polygon zkEVM historical success assertions passed on local HTTP listeners using the two data files included in this revision. These were three of the four outstanding historical-data queries in the preceding utils run.

| Chain | Original block range (inclusive) | Logs | Evidence |
| --- | --- | ---: | --- |
| Celo | 10,000,000–10,001,000 | 3,649 | Tenderly returned the whole query and 11 smaller queries with identical ordered records. The original test also passed live against this source. |
| Blast | 1,000,000–1,010,000 | 109 | Pocket Network provided 11 pages, stable end-block headers and the edge transactions' receipts via `eth_getBlockReceipts`. |
| Polygon zkEVM | 950,000–951,000 | 2,979 | Pocket Network provided five pages, stable end-block headers and canonical edge receipts. |

The Celo gateway returned null for its historical block headers and transaction receipts. The local listener serves this one observed filter. Capturing it twice produced byte-identical gzip files. The Blast gateway returned null for the edge transactions' hash-based receipt lookups. Block-level receipt queries each supplied exactly one matching transaction, which passed the existing canonical block and log checks.

Go 1.25.11 read all three fixtures through `Client.Call`, `GetLogs` and `GetLogsWithPagination` in normal and race builds. Each path produced the full field-by-field result digest shown in the evidence. Adaptive pagination used 15 requests on Blast and two on Polygon zkEVM. Celo used the observed 1,001-block request. The Python suite passed 29 tests, including disagreement between paged and whole responses, wrong receipts, altered captures, and out-of-scope filters.

## Complete upstream utils run

Every case was matched by source file, original declaration name and source SHA-256 at Ponder revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Each row below used a separate Vitest invocation. Each declaration occurs once in the total.

| Execution | Passed | Failed | Original skip | Original todo |
| --- | ---: | ---: | ---: | ---: |
| 16 complete provider files on six archive listeners | 19 | 17 | 0 | 0 |
| Three complete files on the new local listeners | 3 | 1 | 1 | 0 |
| Remaining runtime files, using original public URLs where specified | 50 | 3 | 3 | 1 |
| Original typecheck | 3 | 0 | 0 | 0 |
| **All 101 declarations** | **75** | **21** | **4** | **1** |

Swell blocks 6,002,000–6,003,000 remain the sole failing historical success case. The other 20 failures check provider-specific errors. An earlier invocation of the remaining public files also failed the Tron response-size assertion. It passed in the final complete profile. Real development-node range results from the previous phase have their own evidence.

The formal port ledger remains at 543 of 905. [Fixture commands](../../../docs/HISTORICAL_LOG_RPC.md) · [Per-case results, source identities and data hashes](results.json)
