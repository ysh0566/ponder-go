# Native HTTP historical log verification

A request for WETH logs over Ethereum blocks 18,000,000 through 18,002,000 reproduced a transport failure on revision `972ab73e`. The local archive returned 84,721,928 bytes. The Go client stopped reading after 32 MiB and reported `unexpected end of JSON input`.

With the new client, a direct request with a 128 MiB limit read all 140,192 logs. An 8 MiB limit and automatic pagination returned the identical ordered records in 15 pages. Three oversized requests caused the client to reduce its range. The default 32 MiB limit returned the typed size error on a single request. The persistent synchronizer also recovered a two-block interval as two single blocks in order and surfaced a size error for an oversized single block.

| Verification | Result |
| --- | --- |
| Native RPC package | 22 top-level tests, 158 leaf cases passed in each of normal and race modes |
| Native historical runtime checks | Six top-level tests, 11 leaf cases passed in each mode, including live Anvil, PostgreSQL and PGlite |
| Historical data | Direct and paginated requests matched all rows in seven datasets on six chains in both modes |
| Pinned upstream core RPC tests | All 25 tests across actions, HTTP and RPC passed with a local Anvil node |
| Build | `go vet ./...` passed. The CLI recorded clean revision `c76e9906295f66235919c7c27da2c3dbeaf3876a` |

Each archive comparison covered the address, block and transaction numbers or hashes, log index, topics, data and removal flag in response order. The independent reference digest came from checksum-verified archive pages.

## Complete upstream utils inventory

All 101 declarations from the pinned utils package were accounted for. Six local archive listeners served the complete 16 provider test files whose positive queries had verified historical inputs. Eight providers normally take environment variables. Their nine data assertions passed and their nine provider error assertions failed against the local nodes. The other eight files normally specify literal URLs. Nine data assertions passed and nine service-specific error assertions failed on loopback.

| Execution | Passed | Failed | Upstream skip | Upstream todo |
| --- | ---: | ---: | ---: | ---: |
| Six self-hosted endpoints, complete provider files | 18 | 18 | 0 | 0 |
| Remaining runtime files, original public URLs where specified | 50 | 7 | 4 | 1 |
| Original typecheck | 3 | 0 | 0 | 0 |
| **Total** | **71** | **25** | **4** | **1** |

The seven other failures were the Blast, Celo, Swell and Polygon zkEVM historical data queries and the Blast, Optimism and Swell range errors. The public Tron response-size assertion passed on this run. A local archive can check the payload of a historical query. Claims about a named provider's response limits still depend on that provider. This execution profile and the earlier 905-case original-environment baseline have separate results. The formal port ledger remains at 543 of 905.

The native command-log hashes, archive comparison counts, upstream ledger IDs and all 101 utils outcomes are in [results.json](results.json).
