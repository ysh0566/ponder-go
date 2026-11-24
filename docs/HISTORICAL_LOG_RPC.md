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

The original test, Viem HTTP client, JSON-RPC method, parameters and assertion run against the local HTTP listener. Record these executions separately from tests against the original service. Provider range limits, response-size policies and error messages require the corresponding service.
