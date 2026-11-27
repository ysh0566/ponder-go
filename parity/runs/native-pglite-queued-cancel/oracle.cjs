const assert = require("node:assert/strict");
const { createRequire } = require("node:module");
const path = require("node:path");
const upstream = createRequire(path.resolve("package.json"));
const { createPglite } = upstream("./src/utils/pglite.ts");

async function verify() {
  assert.equal(upstream("./package.json").dependencies["@electric-sql/pglite"], "0.2.13");
  const db = createPglite({ dataDir: "memory://" });
  await db.waitReady;
  try {
    await db.exec("SET statement_timeout='5ms'");
    const start = performance.now();
    const { rows } = await db.query(
      "SELECT sum(n)::text AS total FROM generate_series(1,1500000) AS s(n)",
    );
    assert.equal(rows[0].total, "1125000750000");
    const elapsed = Math.round(performance.now() - start);
    console.log(`PGlite 0.2.13 returned the complete sum after ${elapsed} ms with a 5 ms statement timeout.`);
  } finally {
    await db.close();
  }
}

verify().catch((err) => { console.error(err); process.exitCode = 1; });
