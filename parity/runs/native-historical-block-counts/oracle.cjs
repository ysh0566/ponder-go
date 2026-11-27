const assert = require("node:assert/strict");
const { createRequire } = require("node:module");
const path = require("node:path");
const upstream = createRequire(path.resolve("package.json"));
const { MetricsService, getSyncProgress } = upstream("./src/internal/metrics.ts");

async function verify() {
  const metrics = new MetricsService();
  const label = { chain: "one" };
  metrics.ponder_rpc_request_duration.observe({ ...label, method: "eth_chainId" }, 1);
  const gauges = ["total", "cached", "completed"].map(
    (kind) => metrics[`ponder_historical_${kind}_blocks`],
  );
  for (const [i, value] of [5, 2, 1].entries()) {
    gauges[i].set(label, value);
    const { type, values } = await gauges[i].get();
    assert.equal(type, "gauge");
    assert.deepEqual(values[0].labels, label);
    assert.equal(values[0].value, value);
  }
  assert.equal((await getSyncProgress(metrics))[0].progress, 0.6);
  gauges[2].inc(label, 2);
  assert.equal((await getSyncProgress(metrics))[0].progress, 1);
  for (const gauge of gauges) gauge.set(label, 0);
  assert.equal((await getSyncProgress(metrics))[0].progress, 1);
  metrics.resetIndexingMetrics();
  for (const gauge of gauges) assert.deepEqual((await gauge.get()).values, []);
  console.log("Verified three chain gauges, additive progress, empty range and reset.");
}

verify().catch((error) => { console.error(error); process.exitCode = 1; });
