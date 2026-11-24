# Self-hosted historical log replay

Eighteen distinct historical data assertions passed on loopback using the original Ponder 0.18.2 test files, Viem HTTP transport and Vitest assertions. Nine declarations took a configurable RPC endpoint. Nine previously failed on a literal public URL and passed when the HTTP destination pointed at the local index. Every passed declaration is identified by source file, ledger ID, test name and source SHA-256 in [cases.json](cases.json).

| Chain | Range or application | Result |
| --- | --- | --- |
| Ethereum | Uniswap factory from 18,000,000 through 19,000,000 | 3,773 logs over the full range and 203 from 18,000,000 through 18,050,000 |
| Ethereum | WETH from 18,000,000 through 18,002,000 | 140,192 logs |
| Base | Original Coinbase address from 10,000,000 through 10,001,000 | Both original query widths returned 77 logs |
| Avalanche | WAVAX from 53,164,500 through 53,166,547 | 3,135 logs |
| Moonriver | All addresses from 12,000,000 through 12,001,024 | 590 logs and the original defined-result assertion passed |
| TAC | Original Ankr contract fixture | 185 logs |
| HyperEVM | Original Altitude contract fixture | 4,217 logs |

The new collector checked chain identity, header anchors, ordered page coverage and edge receipts on Base, Avalanche and Moonriver. The earlier checked Ethereum, TAC and HyperEVM pages were reindexed from the retained archive with SHA-256 `890d8ae6010f2f54ced5640f2c16de9a941a0e93149a26f9cc40fad89cda41b8`. All 143 files in the portable replay archive were compared byte-for-byte by SHA-256 after compression.

The Python suite passed 23 tests. The Node routing test verified the actual HTTP payload for both URL and Request inputs and rejection of an unconfigured remote target. Collection, serving and upstream test commands are documented in [HISTORICAL_LOG_RPC.md](../../../docs/HISTORICAL_LOG_RPC.md).

The four outstanding positive data queries from the earlier public-endpoint failures are Blast, Celo, Swell and Polygon zkEVM. The public sources tried for those ranges did not supply usable historical blocks or logs. Vendor-specific error assertions still require the service's own behavior. These supplemental replays leave the original-environment result at 849 passed, 44 failed, 10 skipped and 2 todo. Formal mapping remains 543 of 905.
