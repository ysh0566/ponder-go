const assert = require("node:assert/strict");
const { createRequire } = require("node:module");
const path = require("node:path");

const upstream = createRequire(path.resolve("package.json"));
const { MetricsService } = upstream("./src/internal/metrics.ts");
const { recordAsyncGenerator } = upstream("./src/utils/generators.ts");

async function verify() {
  const metrics = new MetricsService();
  const gauge = metrics.ponder_historical_concurrency_group_duration;
  const samples = async () =>
    (await gauge.get()).values.map(({ labels, value }) => [labels, value]);
  assert.equal(gauge.aggregator, "sum");
  assert.deepEqual(await samples(), []);

  let clock = 0;
  Object.defineProperty(performance, "now", { configurable: true, value: () => clock });
  async function* source() {
    for (let n = 1; n <= 3; n++) {
      clock += 1.25;
      yield n;
    }
  }
  const record = (timing) => {
    gauge.inc({ group: "extract" }, timing.await);
    gauge.inc({ group: "transform" }, timing.yield);
  };
  for await (const n of recordAsyncGenerator(source(), record)) {
    clock += 3.5;
    if (n === 3) break;
  }
  const expected = [[{ group: "extract" }, 2.5], [{ group: "transform" }, 7]];
  assert.deepEqual(await samples(), expected);

  async function* failed() {
    clock += 10;
    throw new Error("acquisition failed");
  }
  await assert.rejects(
    recordAsyncGenerator(failed(), record).next(),
    /acquisition failed/,
  );
  metrics.resetIndexingMetrics();
  assert.deepEqual(await samples(), expected);
  console.log("Verified extract = 2.5 ms and transform = 7 ms, early exit, acquisition failure and indexing reset.");
}

verify().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
