# Historical RPC range policies on local nodes

Four original range-error assertions from Ponder 0.18.2 passed with the expected Viem errors and retry ranges. The runtime tests used their original source, parameters and assertions at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

| Original file | Execution | Range assertion | Historical success assertion in that run |
| --- | --- | --- | --- |
| Swell | OP Labs proxyd v4.37.0 with a 1,000-block difference limit, Anvil 1.8.5 downstream | Passed with code `-32602` | Failed, 0 of 58 logs on the dev chain |
| Avalanche | AvalancheGo v1.13.3, Coreth v0.15.3-rc.1, local C chain | Passed with code `-32000` and maximum 2,048 inclusive blocks | Failed, 0 of 3,135 logs on the dev chain |
| Ankr TAC | TAC v1.6.4, local validator starting at height 3,115,974 | Passed with code `-32000` and maximum block difference 10,000 | Failed, the requested history predates local genesis |
| Moonriver | Verified 590-log index plus an observed OnFinality RPC error | Passed with code `-32603` and maximum block difference 1,024 | Passed in the same offline run |

Both requests at and above the accepted width were also sent directly to the local Swell, Avalanche and TAC listeners. Shifting the starting height by one preserved the rejection above the limit. Direct Anvil accepted the upstream Swell filter that proxyd rejected. TAC returned a history error at the allowed width below local genesis and served an empty result at the local genesis height. The tested Avalanche and TAC development chain IDs were 43112 and 2391337, respectively.

The Moonriver endpoint returned the same JSON-RPC error on repeat requests. The captured end block's parent hash equals the archive's final block hash. A live run against OnFinality passed the range assertion and timed out on the historical success assertion. A second live invocation selected only the range assertion. The original named service endpoints have separate availability and configuration.

## Complete utils package

The following runs account for each of the 101 original declarations exactly once. The three development-node files above have separate results. The table keeps the outcomes of the complete package profile.

| Source execution | Passed | Failed | Original skip | Original todo |
| --- | ---: | ---: | ---: | ---: |
| 16 complete provider files against six local archive listeners, with the Moonriver capture | 19 | 17 | 0 | 0 |
| Remaining runtime files, original public URLs when specified | 50 | 7 | 4 | 1 |
| Original typecheck | 3 | 0 | 0 | 0 |
| **Total** | **72** | **24** | **4** | **1** |

All 18 cases configured through eight private-provider environment variables ran against local endpoints in the archive profile. The four failing historical success cases across the complete package request Blast, Celo, Swell and Polygon zkEVM data. The other 20 failures check service-specific error behavior.

The repository tooling passed 520 Node controls and 26 Python tests. All 1,338 native input files have the same aggregate SHA-256 as the stage base `e28c4b09`. These runs add execution evidence. Formal Go port coverage remains 543 of 905.

Reproduction commands are in [the RPC guide](../../../docs/HISTORICAL_LOG_RPC.md). [Release checksums, control responses, original declaration IDs and execution results](results.json)
