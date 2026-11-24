# Run a local historical log RPC

The Python standard-library server answers `eth_chainId` and bounded `eth_getLogs` requests from captured mined log pages. Use it to run historical data assertions offline. Anvil remains the local execution node for tests that deploy contracts, mine blocks or send transactions.

## Collect and serve

From the project root, collect the Base fixture against a historical RPC that supports the requested blocks.

```sh
python3 tools/historical_log_rpc.py collect \
  --rpc-url https://base.gateway.tenderly.co --chain 8453 \
  --first 10000000 --last 10001000 --page-width 500 \
  --address 0x2Ae3F1Ec7F1F5012CFEab0185bfc7aa3cf0DEc22 \
  parity/runs/local-base-index

python3 tools/historical_log_rpc.py serve --port 18545 parity/runs/local-base-index
```

Collection checks the chain ID, the range-end block hash before and after collection, the bounds and ordering of every log, and canonical receipts for the first and last returned logs. It publishes the completed directory with the page hashes. Keep the manifest and its pages together. The server binds to loopback and prints its URL. Omitting `--port` selects an available port.

Ranges are inclusive. Omitting `--address` during collection captures all logs for the range. An address-specific index accepts queries for that address. Queries outside coverage return an error. The server can load multiple indexes for the same chain. Each query must fit in a single index, and ambiguous overlaps are rejected.

```sh
curl -s http://127.0.0.1:18545 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}'
```

## Run original data assertions

With the pinned upstream dependencies installed, point an environment-configured test at the listener.

```sh
(cd ../ponder-upstream/packages/utils && \
  RPC_URL_COINBASE_8453=http://127.0.0.1:18545 \
  ./node_modules/.bin/vitest run --configLoader native -t success src/_test/coinbase.test.ts)
```

For upstream tests with a literal provider URL, create an exact URL mapping and load the HTTP routing adapter in the Vitest process.

```sh
project=$(pwd)
printf '%s\n' '{"https://mainnet.base.org":"http://127.0.0.1:18545"}' > parity/runs/base-routes.json
(cd ../ponder-upstream/packages/utils && \
  PONDER_LOG_RPC_ROUTES="$project/parity/runs/base-routes.json" \
  NODE_OPTIONS="--import=$project/tools/historical-log-routes.mjs" \
  ./node_modules/.bin/vitest run --configLoader native -t success src/_test/base.test.ts)
```

The original test, Viem HTTP client, JSON-RPC method, parameters and assertion run against the local HTTP listener. Record these executions separately from tests against the original service. A node can also exercise a specific implementation’s range policy. Record the implementation, configuration and chain used for those runs.


## Reproduce an observed RPC error offline

Capture a stable response for a specific historical filter. The collector checks the chain and reads the end block before and after two identical log requests. It records the error code, message, optional data, source hostname, and the end block and parent hashes. Inspect the error payload before sharing the capture.

```sh
python3 tools/historical_log_rpc.py capture-error \
  --rpc-url https://moonriver.api.onfinality.io/public --chain 1285 \
  --first 12000000 --last 12001025 parity/runs/moonriver-error.json

python3 tools/historical_log_rpc.py serve \
  --error-capture tools/rpcnode-fixtures/moonriver-error.json path/to/index-moon
```

Only the matching bounds and address return the observed error. Other requests use the verified index. When an index ends at the captured block or its parent, the server checks that the hashes agree. The committed Moonriver capture ends one block after the 590-log index. With the printed URL mapped to the upstream literal, both cases in `src/_test/moonriver.test.ts` can run offline.

## Run original range assertions against node software

The versions below were run on Linux amd64. Start each local chain in its own temporary environment with loopback networking. The expected limits apply to the particular software and configuration. The upstream success cases additionally require their named mainnet histories.

| Original test file | Node software | Local RPC |
| --- | --- | --- |
| `swell.test.ts` | OP Labs proxyd v4.37.0 in front of Anvil 1.8.5 | `http://127.0.0.1:18546` |
| `avalanche.test.ts` | AvalancheGo v1.13.3 with Coreth v0.15.3-rc.1 | `http://127.0.0.1:9650/ext/bc/C/rpc` |
| `ankr-tac.test.ts` | TAC v1.6.4 with its Cosmos EVM fork v0.6.0-tac.15 | `http://127.0.0.1:8545` |

For Swell, start the two processes in the same network namespace. The proxy rejects a difference of 1,001 blocks. The original query succeeds at the downstream Anvil listener.

```sh
anvil --host 127.0.0.1 --port 18545 --chain-id 1923 --number 6003001 --silent
# In another terminal
proxyd tools/rpcnode-fixtures/proxyd.toml
```

For Avalanche, use a fresh data directory with at least 2 GiB of disk space. The supplied C-chain configuration permits queries beyond the development chain head and caps requests at 2,048 blocks inclusive. Wait for the C chain to bootstrap.

```sh
avax_data=$(mktemp -d)
avalanchego --network-id=local --fd-limit=8192 --sybil-protection-enabled=false \
  --bootstrap-ips= --bootstrap-ids= --public-ip=127.0.0.1 --staking-host=127.0.0.1 \
  --http-host=127.0.0.1 --http-port=9650 --data-dir="$avax_data" \
  --chain-config-dir="$PWD/tools/rpcnode-fixtures/avalanche"
```

For TAC, initialize a fresh single-validator localnet using the tagged project's `contrib/localnet/init.sh` in a disposable environment. Set `ADD_DUMMY_VALIDATORS=false`, use its development chain ID `tacchain_2391337-1`, and keep `tacchaind` and `jq` available. Run the following before the node's first start. The helper signs the development validator transaction at its genesis account number and sets the initial height to 3,115,974. This puts the original filter below the local head on startup. The unmodified EVM backend enforces its default range difference of 10,000.

```sh
dev_root=/path/to/fresh/.tacchaind
tools/rpcnode-fixtures/tac-policy-genesis.sh "$dev_root" > "$dev_root/config/policy-genesis.json" &&
  mv "$dev_root/config/policy-genesis.json" "$dev_root/config/genesis.json"
tacchaind start --home "$dev_root"
```

Use the URL adapter above for the Swell and Avalanche literals, including Avalanche’s RPC path. For TAC, set `RPC_URL_ANKR_239=http://127.0.0.1:8545` directly in the upstream test process. The original Viem client receives the node's JSON-RPC response.

The tagged sources define each limit. [OP Labs proxyd](https://github.com/ethereum-optimism/infra/blob/b17cf26e2828340494cc43fbc8e2b2c36466a93b/proxyd/backend.go#L2142) · [Coreth](https://github.com/ava-labs/coreth/blob/v0.15.3-rc.1/eth/filters/filter.go#L201) · [TAC EVM](https://github.com/TacBuild/evm/blob/v0.6.0-tac.15/rpc/namespaces/ethereum/eth/filters/filters.go#L168) · [TAC localnet initialization](https://github.com/TacBuild/tacchain/blob/v1.6.4/contrib/localnet/init.sh)
