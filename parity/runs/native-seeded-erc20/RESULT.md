# Adventure Gold historical simulation

The pinned `reference-erc20` simulation uses Ethereum mainnet contract `0x32353A6C91143bfd6C7d363B546e62a9A2489A20` at blocks 13,145,000 through 13,147,000 inclusive. The archive contains the 2,001 contiguous headers, original blooms, and all 4,970 Transfer and Approval logs. An independent, single full-range address query without a topic filter matched the chunked capture from `mainnet.gateway.tenderly.co`. The archive projects the header and log fields this application consumes. Transaction bodies and receipts are omitted. The uncompressed archive SHA-256 is `a36a50732372b962f86e1518e74ff69854ac1a61d872ce4dc4c31a7c26a07103`. [Original simulation](https://github.com/ponder-sh/ponder/tree/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/simulation-test/apps/reference-erc20) · [Capture](../../simulation/reference-erc20/capture.py)

| PostgreSQL reference table | Rows | Compared values |
| --- | ---: | --- |
| account | 1,315 | Address, balance and ownership flag |
| allowance | 640 | Owner, spender and amount |
| transfer_event | 3,757 | Event ID, timestamp, sender, recipient and amount |
| approval_event | 1,213 | Event ID, timestamp, owner, spender and amount |

The reference run loads the original schema and handlers with Ponder's Vite plugin, decodes each log with viem, and uses the original checkpoint encoder, indexing store and indexing cache. It flushes the cache to a new PostgreSQL database, reads the four tables, then removes that private database. The Go application independently uses its compiled source configuration and native handlers. Both account legs insert zero for a new address and apply their signed amount on conflict. The complete uncompressed table fixture SHA-256 is `e682183ff35ee74dd7cd301ef9b6a72588b41dc8607a7de03a102fb34ebb5396`. [Original handler](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/simulation-test/apps/reference-erc20/src/index.ts) · [Reference run](../../simulation/reference-erc20/oracle.test.mjs) · [Go test](../../../runtime/simulation_erc20_test.go)

The upstream seed `75ae0bbb7a3614c3a5a6e2b889f6a03d805a68637cb18244394f8460ce297f5e` selects Omnichain ordering, zero unfinalized blocks, disabled RPC caching and a 2% RPC error rate. For each numeric full-block read, the reference evaluates the upstream per-request random rule from attempt zero until first success. For this archive, 39 heights fail once. Both native database runs require every scheduled failure to execute before comparing the four tables. Later reads after a success, log-request faults, database faults, realtime changes and process restarts are outside this result. [Upstream RPC simulation](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/simulation-test/src/rpc-sim.ts#L93-L112) · [Header failure schedule](../../simulation/reference-erc20/initial-block-errors.json)

## Results

Go 1.25.11 passed the seeded replay with PostgreSQL 14.24 and PGlite 0.2.13 in ordinary and race builds. All 39 scheduled failures executed and all four table comparisons passed in each backend and build combination. The earlier WETH simulation also passed ordinary regression on both backends. A shared PostgreSQL server ran out of disk space during its initial WETH run and a PGlite process exited under memory pressure. Repeats used a disposable tmpfs-backed PostgreSQL instance and ran the two PGlite workloads sequentially. All Go packages compiled and passed vet, including the simulation code in vet. Repository verification passed 499 Node cases, four loopback cases and 43 Python cases. The module-wide ordinary and race regressions are tracked separately.

## Reproduction

Configure the Go repository's PGlite bridge and set `PONDER_TEST_POSTGRES` to a disposable PostgreSQL service. The Go tests use the checked-in archive and table fixture.

```sh
go test -tags simulation -p 1 -count=1 -timeout=1h -v -run '^TestSimulationReferenceERC20$' ./runtime
go test -tags simulation -p 1 -race -count=1 -timeout=1h -v -run '^TestSimulationReferenceERC20$' ./runtime
```

To recompute the tables, set `PONDER_UPSTREAM` to the pinned 0.18.2 checkout with its core and simulation dependencies installed. Use Node 24 and a test PostgreSQL account that can create databases. The default run compares the original output with the checked-in fixtures. Set `PONDER_WRITE_REFERENCE=1` to regenerate the table and failure fixtures. To recapture from a node, set `PONDER_RPC_URL_1` to a mainnet archive endpoint.

```sh
"$PONDER_UPSTREAM/packages/core/node_modules/.bin/vitest" run --config parity/simulation/reference-erc20/oracle.config.mjs
python3 parity/simulation/reference-erc20/capture.py
```
