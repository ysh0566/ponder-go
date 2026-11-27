const assert = require("node:assert/strict");
const { createRequire } = require("node:module");
const path = require("node:path");

const upstream = createRequire(path.resolve("package.json"));
const { MetricsService, getAppProgress } = upstream("./src/internal/metrics.ts");

async function verify() {
  for (const ordering of ["multichain", "experimental_isolated", "omnichain"]) {
    const metrics = new MetricsService();
    const gauge = metrics.ponder_indexing_timestamp;
    const samples = async () =>
      (await gauge.get()).values.map(({ labels, value }) => [labels.chain, value]);
    assert.equal(gauge.aggregator, "first");
    assert.deepEqual(await samples(), []);
    metrics.ponder_settings_info.set({ command: "start", database: "postgres", ordering }, 1);
    for (const chain of ["a", "b"]) {
      for (const name of ["total", "cached", "completed"]) {
        metrics[`ponder_historical_${name}_indexing_seconds`].set({ chain }, 0);
      }
      gauge.set({ chain }, 0);
    }
    assert.equal((await getAppProgress(metrics)).progress, 0);
    gauge.set({ chain: "a" }, 30);
    assert.equal((await getAppProgress(metrics)).progress, ordering === "omnichain" ? 1 : 0);
    gauge.set({ chain: "b" }, ordering === "omnichain" ? 30 : 20);
    assert.deepEqual(await samples(), [["a", 30], ["b", ordering === "omnichain" ? 30 : 20]]);
    assert.equal((await getAppProgress(metrics)).progress, 1);
    gauge.set({ chain: "a" }, 10);
    assert.equal((await samples())[0][1], 10);
    metrics.resetIndexingMetrics();
    assert.deepEqual(await samples(), []);
  }
  console.log("Verified labeled timestamps, ordering-specific progress, rewind and reset.");
}

verify().catch((err) => { console.error(err); process.exitCode = 1; });
