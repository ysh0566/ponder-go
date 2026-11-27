const assert = require("node:assert/strict");
const { createRequire } = require("node:module");
const path = require("node:path");
const upstream = createRequire(path.resolve("package.json"));
const { MetricsService } = upstream("./src/internal/metrics.ts");
const { createQB } = upstream("./src/database/queryBuilder.ts");
const { PGlite } = upstream("@electric-sql/pglite");
const { drizzle } = upstream("drizzle-orm/pglite");
const { sql } = upstream("drizzle-orm");

async function main() {
  const metrics = new MetricsService();
  const common = { metrics, logger: { trace() {}, warn() {} }, shutdown: { isKilled: false } };
  const client = new PGlite();
  try {
    const qb = createQB(drizzle(client), { common });
    let attempts = 0;
    await qb.wrap({ label: "probe" }, db => db.execute(++attempts === 1 ? sql`select 1/0` : sql`select 42`));
    await qb.wrap(db => db.execute(sql`select 1`));
    assert.equal(attempts, 2);
    const durations = await metrics.ponder_database_method_duration.get();
    const count = durations.values.find(s => s.metricName.endsWith("_count"));
    assert.deepEqual(count.labels, { method: "probe" });
    assert.equal(count.value, 2);
    const failures = (await metrics.ponder_database_method_error_total.get()).values;
    assert.deepEqual(failures.map(s => [s.labels, s.value]), [[{ method: "probe" }, 1]]);
    assert.deepEqual((await metrics.ponder_postgres_query_total.get()).values, []);
    metrics.resetIndexingMetrics();
    for (const name of ["ponder_database_method_duration", "ponder_database_method_error_total", "ponder_postgres_query_total"]) {
      assert.deepEqual((await metrics[name].get()).values, []);
    }
    console.log("Verified one labeled failure followed by one success, unlabeled SQL, effective labels and reset on pinned PGlite.");
  } finally {
    await client.close();
  }
}
main().then(() => process.exit(0), err => { console.error(err); process.exit(1); });
