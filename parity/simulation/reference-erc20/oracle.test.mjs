import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { gzipSync, gunzipSync } from "node:zlib";
import { context, setupCommon } from "@/_test/setup.js";
import { buildSchema } from "@/build/schema.js";
import { createDatabase } from "@/database/index.js";
import { createIndexingCache } from "@/indexing-store/cache.js";
import { createIndexingStore } from "@/indexing-store/index.js";
import { encodeCheckpoint, EVENT_TYPES } from "@/utils/checkpoint.js";
import { ponder } from "ponder:registry";
import * as schemaExports from "@app/ponder.schema.ts";
import "@app/src/index.ts";

const here = import.meta.dirname;
const upstream = process.env.PONDER_UPSTREAM;
const req = createRequire(path.join(upstream, "simulation-test/package.json"));
const { Client } = createRequire(path.join(upstream, "packages/core/package.json"))("pg");
const { decodeEventLog } = req("viem");
const seedrandom = req("seedrandom");
const seed = "75ae0bbb7a3614c3a5a6e2b889f6a03d805a68637cb18244394f8460ce297f5e";
const pick = (choices, tag) => choices[Math.floor(choices.length * seedrandom(seed + tag)())];

test("compare four reference-erc20 tables with the original indexing store", async () => {
  const cfg = JSON.parse(fs.readFileSync(path.join(here, "config.json")));
  process.env.ORDERING = pick(["multichain", "omnichain"], "ordering");
  const { default: appConfig } = await import("@app/ponder.config.ts");
  const source = appConfig.contracts.ERC20;
  const { chain, address, startBlock, endBlock } = cfg.contracts.ERC20;
  const range = { chain, address, startBlock, endBlock };
  assert.deepEqual(range, { chain: source.chain, address: source.address,
    startBlock: source.startBlock, endBlock: source.endBlock });
  assert.equal(cfg.chains.mainnet.id, appConfig.chains.mainnet.id);
  assert.equal(cfg.ordering, appConfig.ordering);
  assert.equal(cfg.chains.mainnet.reorgWindow, pick([0, 0, 50, 100, 250, 300], "unfinalized-blocks"));
  assert.equal(cfg.chains.mainnet.cacheRpcRequests, pick([true, false], "cache-rpc-requests"));
  const { mainnet } = JSON.parse(gunzipSync(fs.readFileSync(path.join(here, "archive.json.gz"))));
  // rpc-sim.ts uses the serialized request and a per-request nonce. Record
  // each numeric header's initial consecutive failures before its first success.
  const rate = pick([0, 0.02, 0.05], "rpc-error-rate");
  const failures = {};
  for (const header of mainnet.blocks) {
    const id = JSON.stringify({ method: "eth_getBlockByNumber", params: [header.number, true] });
    let nonce = 0;
    while (seedrandom(seed + id + nonce)() < rate) nonce++;
    if (nonce > 0) failures[header.number] = nonce;
  }
  const failureFile = path.join(here, "initial-block-errors.json");
  const times = new Map(mainnet.blocks.map((b) => [b.hash, BigInt(b.timestamp)]));
  const handlers = new Map(ponder.fns.map(({ name, fn }) => [name, fn]));
  const dbName = "ponder_go_erc20_oracle_" + randomUUID().replaceAll("-", "");
  assert.ok(process.env.PONDER_TEST_POSTGRES, "set PONDER_TEST_POSTGRES to a test server with CREATEDB");
  const admin = new Client({ connectionString: process.env.PONDER_TEST_POSTGRES });
  const url = new URL(process.env.PONDER_TEST_POSTGRES);
  url.pathname = "/" + dbName;
  setupCommon();
  await admin.connect();
  let created = false;
  try {
    await admin.query('CREATE DATABASE "' + dbName + '"');
    created = true;
    const databaseConfig = { kind: "postgres", poolConfig: { connectionString: url.toString(), max: 10 } };
    const preBuild = { ordering: appConfig.ordering, databaseConfig };
    const { statements } = buildSchema({ schema: schemaExports, preBuild });
    const schemaBuild = { schema: schemaExports, statements };
    const database = createDatabase({ common: context.common, namespace: { schema: "public" }, preBuild, schemaBuild });
    await database.migrate({ buildId: "erc20-reference", chains: [], finalizedBlocks: [] });
    const cache = createIndexingCache({ common: context.common, schemaBuild, eventCount: {}, crashRecoveryCheckpoint: undefined });
    let retryError;
    const store = createIndexingStore({ common: context.common, schemaBuild, indexingCache: cache,
      indexingErrorHandler: {
        getRetryableError: () => retryError,
        setRetryableError: (error) => { retryError = error; },
        clearRetryableError: () => { retryError = undefined; },
      } });
    await database.userQB.transaction(async (tx) => {
      cache.qb = tx;
      store.qb = tx;
      for (const log of mainnet.logs) {
        const { eventName, args } = decodeEventLog({ abi: source.abi, topics: log.topics, data: log.data, strict: true });
        const blockTimestamp = times.get(log.blockHash);
        const id = encodeCheckpoint({ blockTimestamp, chainId: cfg.chains.mainnet.id, blockNumber: BigInt(log.blockNumber),
          transactionIndex: BigInt(log.transactionIndex), eventType: EVENT_TYPES.logs, eventIndex: BigInt(log.logIndex) });
        await handlers.get("ERC20:" + eventName)({ event: { args, id, block: { timestamp: blockTimestamp } }, context: { db: store.db } });
      }
      await cache.flush();
    });
    const client = new Client({ connectionString: url.toString() });
    await client.connect();
    const tables = {};
    try {
      const primaryKeys = { account: ["address"], allowance: ["owner", "spender"], transfer_event: ["id"], approval_event: ["id"] };
      for (const [name, keys] of Object.entries(primaryKeys)) {
        const order = keys.map((key) => key + ' COLLATE "C"').join(", ");
        tables[name] = (await client.query("SELECT * FROM public." + name + " ORDER BY " + order)).rows;
      }
    } finally { await client.end(); }
    const file = path.join(here, "tables.json.gz");
    if (process.env.PONDER_WRITE_REFERENCE === "1") {
      fs.writeFileSync(file, gzipSync(JSON.stringify(tables), { level: 9 }));
      fs.writeFileSync(failureFile, JSON.stringify(failures, null, 2) + "\n");
    } else {
      assert.deepEqual(tables, JSON.parse(gunzipSync(fs.readFileSync(file))));
      assert.deepEqual(failures, JSON.parse(fs.readFileSync(failureFile)));
    }
    console.log(Object.fromEntries(Object.entries(tables).map(([name, rows]) => [name, rows.length])));
  } finally {
    try {
      try { await context.common.shutdown.kill(); } finally {
        if (created) await admin.query('DROP DATABASE "' + dbName + '" WITH (FORCE)');
      }
    } finally { await admin.end(); }
  }
});
