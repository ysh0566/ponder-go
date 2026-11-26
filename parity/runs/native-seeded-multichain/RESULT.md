# Three-chain WETH historical simulation

The pinned Ponder 0.18.2 application registers one WETH Deposit handler. A recipient has one balance across Ethereum, Optimism and Base. The upstream seed `75ae0bbb7a3614c3a5a6e2b889f6a03d805a68637cb18244394f8460ce297f5e` selects Omnichain ordering, zero unfinalized blocks and disabled RPC caching. The reference script checks these choices using the upstream `seedrandom` dependency. The full simulator also varies transport limits, errors, restarts and realtime behavior. That fault schedule remains open. [Upstream application](https://github.com/ponder-sh/ponder/tree/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/simulation-test/apps/feature-multichain)

| Chain | Inclusive range | Blocks | Deposits | Capture endpoint |
| --- | --- | ---: | ---: | --- |
| Ethereum | 22,547,400–22,547,650 | 251 | 3,582 | `mainnet.gateway.tenderly.co` |
| Optimism | 136,213,300–136,213,550 | 251 | 234 | `mainnet.optimism.io` |
| Base | 30,618,000–30,618,250 | 251 | 7,940 | `base.gateway.tenderly.co` |

A fresh capture in eight-block intervals reproduced every archived header and log. A second check in four disjoint log intervals per chain also matched. The archive contains contiguous headers with their original bloom filters and every matching log with its block and transaction identity. The replay transport accepts the application's address and Deposit topic, including blocks with no matches. It supplies the block fields used by the handler. The fixture omits transaction bodies and receipts. The uncompressed JSON SHA-256 is `afa525135a62912923d381c3775310992775722c7f231047c60a29762f27e2a9`. [Capture script](../../simulation/feature-multichain/capture.py) · [Archive](../../simulation/feature-multichain/archive.json.gz)

The reference script loads the immutable Git objects for the original configuration, ABI and application handler. Viem decodes each log. The handler executes its insert or update through a small adapter backed by a PostgreSQL temporary table. The Go test independently decodes the archive through `NewIndexerFromSource`, executes the Go handler against its own database, and compares all 332 addresses and balances. Eight addresses occur on multiple chains. The reference table SHA-256 is `ccf3c39db9d5e423492dc3c4767153929710dbaf0a0f3b8b22890ed8336a017e`. [Reference script](../../simulation/feature-multichain/oracle.cjs) · [Account rows](../../simulation/feature-multichain/accounts.json) · [Go replay](../../../runtime/simulation_multichain_test.go)

## Results

Go 1.25.11 passed the complete 11,756-deposit comparison on PostgreSQL 14.24 and PGlite 0.2.13 in ordinary and race builds. Each run checked all 332 expected account rows. The PostgreSQL ordinary run was repeated after the ordering assertion was added. The upstream callback reproduced the account fixture in a separate PostgreSQL transaction. All Go packages compiled and passed vet with the simulation code included in vet. Verification tools passed 499 Node cases, four loopback cases and 43 Python cases. Module-wide ordinary and race regression results are tracked separately.

## Reproduction

Set `PONDER_TEST_POSTGRES` to a disposable PostgreSQL URL and configure the repository's PGlite bridge. Both Go runs replay the checked-in archive. For the optional reference check, set `PONDER_UPSTREAM` to the pinned checkout with its simulation dependencies installed.

```sh
go test -tags simulation -p 1 -count=1 -timeout=1h -v -run '^TestSimulationFeatureMultichain$' ./runtime
go test -tags simulation -p 1 -race -count=1 -timeout=1h -v -run '^TestSimulationFeatureMultichain$' ./runtime
node parity/simulation/feature-multichain/oracle.cjs
```

To check the captured inputs against live nodes, set `PONDER_RPC_URL_1`, `PONDER_RPC_URL_10` and `PONDER_RPC_URL_8453` to archive nodes serving the ranges above. The capture script checks every header and log against the existing file. Pass `--output PATH` to save a separate capture. Use `oracle.cjs --write` to regenerate the account table after reviewing a fixture update.

```sh
python3 parity/simulation/feature-multichain/capture.py
```
