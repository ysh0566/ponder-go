const assert = require("node:assert/strict");
const { createRequire } = require("node:module");
const path = require("node:path");
const upstream = createRequire(path.resolve("package.json"));
const { MetricsService } = upstream("./src/internal/metrics.ts");
(async () => {
  const metrics = new MetricsService();
  const start = metrics.ponder_historical_start_timestamp_seconds;
  const end = metrics.ponder_historical_end_timestamp_seconds;
  const stamp = 1700000000;
  assert.equal(start.aggregator, "min");
  assert.equal(end.aggregator, "max");
  for (const gauge of [start, end]) {
    assert.deepEqual((await gauge.get()).values, []);
    for (const chain of ["a", "b"]) gauge.set({chain}, stamp);
    assert.deepEqual((await gauge.get()).values.map(x => [x.labels, x.value]),
      [[{chain:"a"},stamp],[{chain:"b"},stamp]]);
  }
  metrics.resetIndexingMetrics();
  for (const gauge of [start,end]) assert.deepEqual((await gauge.get()).values,[]);
  console.log("Pinned writer registry: chain labels, gauge values, aggregation and indexing reset verified.");
})().catch(error => { console.error(error); process.exitCode = 1; });
