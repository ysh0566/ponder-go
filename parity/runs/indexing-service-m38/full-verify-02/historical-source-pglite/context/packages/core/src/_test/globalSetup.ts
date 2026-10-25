import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createPool, startProxy } from "@viem/anvil";
import dotenv from "dotenv";
import { execa } from "execa";
import { Pool } from "pg";

const __dirname = dirname(fileURLToPath(import.meta.url));

async function globalSetup() {
  dotenv.config({ path: ".env.local" });

  const generatedFilePath = join(__dirname, "generated.ts");
  if (!existsSync(generatedFilePath)) {
    await execa("pnpm", ["wagmi", "generate"]);
  }

  // Environment-adapted startup: explicit loopback ports, no interface discovery.
  const shutdownProxy = await startProxy({
    host: "127.0.0.1",
    port: 8545,
    pool: createPool({ autoPort: false }),
    options: (id) => {
      if (!Number.isSafeInteger(id) || id < 1 || id > 64) {
        throw new Error("Invalid isolated Anvil worker ID: " + id);
      }
      return {
      host: "127.0.0.1",
      port: 18545 + id,
      chainId: 1,
      noMining: true,
      // Note: `@viem/anvil` does not clear the stop timeout, which keeps the process alive.
      stopTimeout: 100,
      };
    },
  });

  let cleanupDatabase: () => Promise<void>;
  if (process.env.DATABASE_URL) {
    cleanupDatabase = async () => {
      const pool = new Pool({ connectionString: process.env.DATABASE_URL });

      const databaseRows = await pool.query(`
        SELECT datname FROM pg_database WHERE datname LIKE 'vitest_%';
      `);
      const databases = databaseRows.rows.map((r) => r.datname) as string[];

      await Promise.all(
        databases.map((databaseName) =>
          pool.query(`DROP DATABASE "${databaseName}"`),
        ),
      );

      await pool.end();
    };
  }

  return async () => {
    await shutdownProxy();
    await cleanupDatabase?.();
  };
}

function resetPonderGlobals() {
  // @ts-expect-error
  globalThis.PONDER_DATABASE = undefined;
  // @ts-expect-error
  globalThis.PONDER_NAMESPACE_BUILD = undefined;
  // @ts-expect-error
  globalThis.PONDER_INDEXING_BUILD = undefined;
}

if ("bun" in process.versions) {
  // must be run outside of hook because missing the generated files causes test to fail with 'Cannot find module' error
  await globalSetup();
  const { beforeEach, afterEach } = require("bun:test");
  beforeEach(resetPonderGlobals);
  afterEach(resetPonderGlobals);
}

export default globalSetup;
