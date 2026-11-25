# Verified public RPC archives

Fourteen additional Ponder 0.18.2 assertions passed through their original public endpoint, the self-hosted historical RPC listeners and the native Go HTTP client. Seven check returned logs and seven check provider errors and the resulting retry ranges. The mappings now cover 580 of 905 declarations, including 576 in Go and four in the SDK. Another 320 await implementation and five cover retired database migrations. [Mappings and evidence digests](results.json)

The archive contains eight block ranges and ten twice-observed RPC errors. It includes 13,861 Aurora logs with transaction-local log indexes, 5,616 logs on the Somnia Shannon testnet and 3,478 Optimism WETH logs from a query the pinned test expects to fail. SHA-256 for the 1,313,484-byte archive is `026f05727b8ffc09af0a80266a733d59eae6729931c8fa7cad76c07f62d43ca9`. [Collection commands and chain details](../../../docs/HISTORICAL_LOG_RPC.md)

The two original 1RPC requests exceeded its observed block-number difference limit of 50. The service returned code `-32602` for both, including the historical UNI query whose canonical Ethereum data contains nine logs. The self-hosted 1RPC listener reproduces both errors. The Go client recognizes the current limit and splits a difference of 51 into a difference of 50 and one final block. The independent Ethereum archive still verifies the pinned nine-log data assertion.

| Verification | Result |
| --- | --- |
| Original public RPC endpoints, nine original test files | 14 passed, three failed, one upstream skip. All 18 outcomes matched the isolated listeners. |
| Original utils runtime and types, 38 files | 78 passed, 18 failed, four upstream skips, one upstream todo. The local partition used isolated archive listeners, proxyd, Anvil, Coreth and TAC. |
| Full pinned inventory, 104 files | 875 passed, 18 failed, 10 upstream skips, two upstream todos. Six unchanged report artifacts from earlier runs were verified by SHA-256. |
| Go RPC package | All 25 top-level tests and 213 leaf cases passed independently in normal and race modes, with zero skips. |
| Repository checks | Go build and vet passed, as did 500 Node checks and 40 Python tests. |

The additional failure since the previous full inventory is the original 1RPC success case. Replaying the public endpoint’s current policy reproduces the direct failure even though the historical UNI logs still exist. [Full inventory and unresolved assertions](upstream.json)

From a clean checkout, run the package checks with Go 1.25.11, Python 3 and Node 24.

```sh
go test -count=1 -timeout=60m -json ./rpc
go test -race -count=1 -timeout=60m -json ./rpc
go vet ./...
go build ./...
python3 -m unittest discover -s tools -p '*_test.py'
```
