// Execute the pinned application's handler and compare all of its account rows.
const fs = require("node:fs");
const path = require("node:path");
const zlib = require("node:zlib");
const vm = require("node:vm");
const { createRequire } = require("node:module");
const { execFileSync } = require("node:child_process");
const assert = require("node:assert/strict");

const upstream = path.resolve(process.env.PONDER_UPSTREAM);
const revision = "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334";
const app = "simulation-test/apps/feature-multichain/";
const req = createRequire(path.join(upstream, "simulation-test/package.json"));
const ts = req("typescript");
const { decodeEventLog } = req("viem");
const { Client } = createRequire(path.join(upstream, "packages/core/package.json"))("pg");
const seedrandom = req("seedrandom");
const seed = "75ae0bbb7a3614c3a5a6e2b889f6a03d805a68637cb18244394f8460ce297f5e";
const pick = (list, tag) => list[Math.floor(list.length * seedrandom(seed + tag)())];
const read = (name) => JSON.parse(fs.readFileSync(path.join(__dirname, name)));

// Read immutable Git objects so local edits in the reference checkout cannot affect results.
function load(name, dependencies, env = {}) {
  const contents = execFileSync("git", ["-C", upstream, "show", revision + ":" + app + name], { encoding: "utf8" });
  const code = ts.transpile(contents, { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 });
  const exports = {};
  vm.runInNewContext(code, { exports, process: { env }, require(id) {
    assert.ok(Object.hasOwn(dependencies, id), id);
    return dependencies[id];
  } }, { filename: name });
  return exports;
}

function accountStore(pg) {
  async function insertOrUpdate(row, update) {
    const address = row.address.toLowerCase();
    const result = await pg.query("SELECT balance FROM pg_temp.account WHERE address=$1", [address]);
    if (result.rowCount === 0) {
      await pg.query("INSERT INTO pg_temp.account VALUES ($1,$2)", [address, row.balance]);
      return;
    }
    const change = update({ address, balance: BigInt(result.rows[0].balance) });
    await pg.query("UPDATE pg_temp.account SET balance=$2 WHERE address=$1", [address, change.balance]);
  }

  return {
    insert(table) {
      assert.equal(table, "account");
      return {
        values(row) {
          return { onConflictDoUpdate: (update) => insertOrUpdate(row, update) };
        },
      };
    },
  };
}

async function main() {
  const config = read("config.json");
  const ordering = pick(["multichain", "omnichain"], "ordering");
  const unfinalized = pick([0, 0, 50, 100, 250, 300], "unfinalized-blocks");
  const cache = pick([true, false], "cache-rpc-requests");
  assert.equal(config.ordering, ordering);
  const { weth9Abi } = load("abis/weth9Abi.ts", {});
  const original = load("ponder.config.ts", { ponder: { createConfig: (input) => input }, "./abis/weth9Abi": { weth9Abi } }, { ORDERING: ordering }).default;
  assert.deepEqual(JSON.parse(JSON.stringify(original.contracts.weth9.chain)), config.contracts.weth9.chain);
  assert.deepEqual(Object.keys(original.chains).sort(), Object.keys(config.chains).sort());
  for (const [name, chain] of Object.entries(config.chains)) {
    assert.equal(chain.id, original.chains[name].id);
    assert.equal(chain.cacheRpcRequests, cache);
    assert.equal(chain.reorgWindow, unfinalized);
  }
  let handler;
  load("src/index.ts", {
    "ponder:registry": { ponder: { on(name, fn) { assert.equal(name, "weth9:Deposit"); handler = fn; } } },
    "ponder:schema": { account: "account" },
  });
  const archive = JSON.parse(zlib.gunzipSync(fs.readFileSync(path.join(__dirname, "archive.json.gz"))));
  assert.ok(process.env.PONDER_TEST_POSTGRES, "set the disposable test PostgreSQL URL");
  const pg = new Client({ connectionString: process.env.PONDER_TEST_POSTGRES });
  await pg.connect();
  try {
    await pg.query("BEGIN");
    await pg.query('CREATE TEMP TABLE account (address text PRIMARY KEY, balance numeric(78,0) NOT NULL)');
    const db = accountStore(pg);
    const events = [];
    for (const [name, { blocks, logs }] of Object.entries(archive)) {
      const timestamp = new Map(blocks.map((b) => [b.hash, Number(b.timestamp)]));
      for (const log of logs) {
        const time = timestamp.get(log.blockHash);
        assert.ok(Number.isSafeInteger(time), log.blockHash);
        events.push({ time, chain: config.chains[name].id, log });
      }
    }
    events.sort((a, b) => a.time - b.time || a.chain - b.chain ||
      Number(a.log.blockNumber) - Number(b.log.blockNumber) ||
      Number(a.log.transactionIndex) - Number(b.log.transactionIndex) ||
      Number(a.log.logIndex) - Number(b.log.logIndex));
    for (const { log } of events) {
      const event = decodeEventLog({ abi: weth9Abi, topics: log.topics, data: log.data, strict: true });
      assert.equal(event.eventName, "Deposit");
      await handler({ event, context: { db } });
    }
    const { rows } = await pg.query("SELECT address,balance FROM pg_temp.account ORDER BY address COLLATE \"C\"");
    if (process.argv.includes("--write")) {
      fs.writeFileSync(path.join(__dirname, "accounts.json"), JSON.stringify(rows, null, 2) + "\n");
    } else {
      assert.deepEqual(rows, read("accounts.json"));
    }
    console.log(ordering, "account rows", rows.length);
  } finally {
    await pg.end();
  }
}
main().catch((error) => { console.error(error); process.exitCode = 1; });
