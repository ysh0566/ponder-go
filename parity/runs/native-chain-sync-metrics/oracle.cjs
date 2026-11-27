const assert = require("node:assert/strict");
const { createRequire } = require("node:module");
const path = require("node:path");
const upstream = createRequire(path.resolve("package.json"));
const { blockToCheckpoint, encodeCheckpoint } = upstream("./src/utils/checkpoint.ts");
const { MetricsService } = upstream("./src/internal/metrics.ts");
const { getRealtimeEventsIsolated, handleRealtimeSyncEvent } = upstream("./src/runtime/realtime.ts");

async function main() {
  const metrics = new MetricsService();
  const chain = { id: 31337, name: "local", cacheRpcRequests: false, pollingInterval: 100, reorgWindow: 600 };
  const logger = { info() {}, trace() {}, debug() {}, warn() {}, child() { return this; } };
  const common = { metrics, logger, shutdown: { isKilled: false } };
  const rpc = async (method, params) => {
    const response = await fetch(process.env.ANVIL_URL, {
      method: "POST", headers: { "content-type": "application/json" },
      body: JSON.stringify({ id: 1, jsonrpc: "2.0", method, params }),
    });
    const body = await response.json();
    assert.equal(body.error, undefined);
    return body.result;
  };
  const base = await rpc("eth_getBlockByNumber", ["latest", true]);
  await rpc("evm_mine", []);
  const tip = await rpc("eth_getBlockByNumber", ["latest", true]);
  const read = async () => {
    const values = {};
    for (const name of ["block", "block_timestamp", "is_realtime", "is_complete"]) {
      const series = (await metrics["ponder_sync_" + name].get()).values;
      if (series.length) {
        assert.deepEqual(series.map(s => s.labels.chain), ["local"]);
        values[name] = series[0].value;
      }
    }
    return values;
  };
  let deliver;
  const subscribed = Promise.withResolvers();
  const transport = {
    subscribe({ onBlock }) { deliver = onBlock; subscribed.resolve(); },
    unsubscribe() {},
  };
  const syncProgress = {
    current: base, finalized: base, end: tip,
    isEnd() { return this.current.hash === tip.hash; },
    getCheckpoint({ tag }) { return encodeCheckpoint(blockToCheckpoint(this[tag], chain.id, "up")); },
  };
  const args = { common, chain, syncProgress, database: {}, eventCallbacks: [], childAddresses: new Map(), unfinalizedBlocks: [],
    indexingBuild: { chains: [chain], rpcs: [transport], eventCallbacks: [[]], finalizedBlocks: [base] } };
  assert.deepEqual(await read(), {});
  const events = getRealtimeEventsIsolated(args);
  const next = events.next();
  await subscribed.promise;
  assert.deepEqual(await read(), { is_realtime: 1 });
  const ack = deliver(tip);
  const { value } = await next;
  assert.equal(value.type, "block");
  value.blockCallback?.(true);
  assert.equal(await ack, true);
  assert.equal((await events.next()).done, true);
  assert.deepEqual(await read(), { block: Number(tip.number), block_timestamp: Number(tip.timestamp), is_realtime: 0, is_complete: 1 });
  await handleRealtimeSyncEvent({ type: "reorg", block: base, reorgedBlocks: [tip] }, args);
  assert.equal((await read()).block, Number(base.number));
  assert.equal((await read()).block_timestamp, Number(base.timestamp));
  metrics.resetIndexingMetrics();
  assert.deepEqual(await read(), { block_timestamp: Number(base.timestamp) });
  syncProgress.current = tip;
  assert.equal((await getRealtimeEventsIsolated(args).next()).done, true);
  assert.deepEqual(await read(), { block_timestamp: Number(base.timestamp), is_complete: 1 });
  console.log("Verified original realtime, finite completion, rewind, reset and backfill-only gauges.");
}

main().then(() => process.exit(0), error => { console.error(error); process.exit(1); });
