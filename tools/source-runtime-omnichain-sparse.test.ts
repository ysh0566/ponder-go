import { createHash, randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import pg from "pg";
import { numberToHex, type Hex } from "viem";
import { expect, onTestFinished, test } from "vitest";
import { buildSchema } from "@/build/schema.js";
import {
	createDatabase,
	getPonderCheckpointTable,
	getPonderMetaTable,
} from "@/database/index.js";
import { onchainTable } from "@/drizzle/onchain.js";
import { createLogger } from "@/internal/logger.js";
import { MetricsService } from "@/internal/metrics.js";
import { buildOptions } from "@/internal/options.js";
import { createShutdown } from "@/internal/shutdown.js";
import type {
	Chain,
	DatabaseConfig,
	EventCallback,
	IndexingBuild,
	LightBlock,
	LogFilter,
	PreBuild,
	SyncBlock,
	SyncLog,
} from "@/internal/types.js";
import type { Rpc } from "@/rpc/index.js";
import { defaultLogFilterInclude } from "@/runtime/filter.js";
import { runOmnichain } from "@/runtime/omnichain.js";
import { createSyncStore } from "@/sync-store/index.js";
import { blockToCheckpoint, encodeCheckpoint } from "@/utils/checkpoint.js";

// Supplemental source probe. Seed a fully synced sparse cache and run the
// pinned source's real SQL paging, ABI decoding, callbacks and transactions.
const golden = JSON.parse(
	readFileSync(
		new URL(
			"../historical/testdata/runtime_omnichain_logonly_source_golden.json",
			import.meta.url,
		),
		"utf8",
	),
).cases[0].data;
const end = 13_000;
const distant = 12_020;
const marker = "controlled sparse callback failure";
const rows = onchainTable("sparse_log_rows", (t) => ({
	id: t.text().primaryKey(),
}));
const namespaceBuild = { schema: "public", viewsSchema: undefined };

function hash(id: number, number: number, type: string): Hex {
	return `0x${createHash("sha256").update(`${id}:${number}:${type}`).digest("hex")}`;
}

function header(id: number, block: number): LightBlock {
	return {
		number: numberToHex(block),
		timestamp: numberToHex(100 + block),
		hash: hash(id, block, "block"),
		parentHash: hash(id, block - 1, "block"),
	};
}

function callbackKey(id: number, block: number, index: number, name: string) {
	return `${id}:${block}:${index}:${name}`;
}

function expectedCalls(active: number[]) {
	return [1, distant].flatMap((number) =>
		active.flatMap((id) =>
			[0, 1, 2, 3].flatMap((index) =>
				["A", "B"].map((name) => callbackKey(id, number, index, name)),
			),
		),
	);
}

async function testDatabase(persistent: boolean): Promise<{
	config: DatabaseConfig;
	remove: () => Promise<void>;
}> {
	const connectionString = process.env.PONDER_SPARSE_POSTGRES;
	if (!connectionString) {
		const directory = persistent
			? await mkdtemp(path.join(tmpdir(), "ponder-sparse-source-"))
			: undefined;
		return {
			config: {
				kind: "pglite",
				options: { dataDir: directory ?? "memory://" },
			},
			remove: async () => {
				if (directory) await rm(directory, { recursive: true, force: true });
			},
		};
	}
	const url = new URL(connectionString);
	if (url.hostname !== "127.0.0.1" || url.pathname !== "/ponder_test") {
		throw new Error(
			"The sparse probe requires a dedicated loopback PostgreSQL fixture",
		);
	}
	const database = "sparse_" + randomUUID().replaceAll("-", "");
	const run = async (sql: string) => {
		const client = new pg.Client({ connectionString });
		await client.connect();
		try {
			await client.query(sql);
		} finally {
			await client.end();
		}
	};
	await run(`CREATE DATABASE "${database}"`);
	url.pathname = `/${database}`;
	return {
		config: {
			kind: "postgres",
			poolConfig: { connectionString: url.href, max: 8 },
		},
		remove: () => run(`DROP DATABASE "${database}"`),
	};
}

async function sparseRun(
	querySize: number,
	active: number[],
	fail: boolean,
	skipTail = false,
) {
	const endBlock = skipTail ? 3 : end;
	const laterBlock = skipTail ? 3 : distant;
	const cliOptions = {
		command: "start",
		config: "",
		root: "",
		logLevel: "silent",
		logFormat: "pretty",
		version: "0.0.0",
	} as const;
	const { config, remove } = await testDatabase(fail);
	let shutdown = createShutdown();
	onTestFinished(async () => {
		try {
			await shutdown.kill();
		} finally {
			await remove();
		}
	});
	const preBuild: PreBuild = { databaseConfig: config, ordering: "omnichain" };
	const schema = { rows };
	const schemaBuild = {
		schema,
		statements: buildSchema({ schema, preBuild }).statements,
	};
	const calls: string[] = [];
	const failAt = callbackKey(2, laterBlock, 1, "A");
	let injectFailure = fail;
	const indexingFunctions = ["A", "B"].map((name) => ({
		name: `Token${name}:Value`,
		fn: async ({
			event,
			context,
		}: {
			event: {
				block: { number: bigint };
				log: { logIndex: number };
				args: { value: bigint };
			};
			context: {
				chain: { id: number };
				db: {
					insert: (table: typeof rows) => {
						values: (values: { id: string }) => Promise<unknown>;
					};
				};
			};
		}) => {
			expect(typeof event.args.value).toBe("bigint");
			const key = callbackKey(
				context.chain.id,
				Number(event.block.number),
				event.log.logIndex,
				name,
			);
			calls.push(key);
			await context.db.insert(rows).values({ id: key });
			if (injectFailure && key === failAt) throw new Error(marker);
		},
	}));
	const chains = [1, 2].map(
		(id): Chain => ({
			id,
			name: `chain${id}`,
			rpc: "http://127.0.0.1",
			ws: undefined,
			pollingInterval: 1_000,
			reorgWindow: 0,
			cacheRpcRequests: true,
			ethGetLogsBlockRange: undefined,
			viemChain: undefined,
		}),
	);
	const eventCallbacks = chains.map((chain) =>
		indexingFunctions.map(
			({ name, fn }): EventCallback => ({
				chain,
				name,
				fn,
				type: "contract",
				abiItem: golden.abi_item,
				metadata: { safeName: "Value", abi: [golden.abi_item] },
				filter: {
					type: "log",
					chainId: chain.id,
					sourceId: name.split(":")[0]!,
					address: golden.address,
					topic0: golden.chains[0].logs[0].topics[0],
					topic1: null,
					topic2: null,
					topic3: null,
					fromBlock: 1,
					toBlock: endBlock,
					hasTransactionReceipt: false,
					include: defaultLogFilterInclude,
				},
			}),
		),
	);
	const rpcs = chains.map(
		({ id }): Rpc => ({
			hostnames: [],
			request: (async ({
				method,
				params,
			}: {
				method: string;
				params: [Hex, boolean];
			}) => {
				expect(method).toBe("eth_getBlockByNumber");
				expect(params[1]).toBe(false);
				if (skipTail) expect([1, 2, 3]).toContain(Number(params[0]));
				else expect([1, endBlock]).toContain(Number(params[0]));
				return {
					...golden.chains[id - 1].blocks[0],
					...header(id, Number(params[0])),
					transactions: [],
				};
			}) as Rpc["request"],
			subscribe: () => {
				throw new Error("bounded fixture unexpectedly subscribed");
			},
			unsubscribe: async () => {},
		}),
	);
	const indexingBuild: IndexingBuild = {
		buildId: "deadbeef01",
		chains,
		rpcs,
		finalizedBlocks: chains.map(({ id }) => header(id, endBlock + 1)),
		eventCallbacks,
		setupCallbacks: [[], []],
		indexingFunctions,
		contracts: [{}, {}],
	};
	const { buildId, finalizedBlocks } = indexingBuild;
	const open = async () => {
		const common = {
			options: {
				...buildOptions({ cliOptions }),
				syncEventsQuerySize: querySize,
			},
			logger: createLogger({ level: "silent" }),
			metrics: new MetricsService(),
			shutdown,
			apiShutdown: shutdown,
			buildShutdown: shutdown,
		};
		const database = createDatabase({
			common,
			preBuild,
			namespace: namespaceBuild,
			schemaBuild,
		});
		const crashRecoveryCheckpoint = await database.migrate({
			buildId,
			chains,
			finalizedBlocks,
		});
		await database.migrateSync();
		return {
			common,
			database,
			crashRecoveryCheckpoint,
			run: () =>
				runOmnichain({
					common,
					preBuild,
					namespaceBuild,
					schemaBuild,
					indexingBuild,
					crashRecoveryCheckpoint,
					database,
				}),
		};
	};
	const { common, database, run } = await open();
	const syncStore = createSyncStore({ common, qb: database.syncQB });
	for (const { id } of chains) {
		const filters = eventCallbacks[id - 1]!.map(
			({ filter }) => filter as LogFilter,
		);
		await syncStore.insertIntervals({
			chainId: id,
			intervals: filters.map((filter) => ({ interval: [1, endBlock], filter })),
			factoryIntervals: [],
		});
		if (!active.includes(id)) continue;
		const template = golden.chains[id - 1];
		for (const [original, number] of [
			[1, 1],
			[3, laterBlock],
		] as const) {
			const block = {
				...template.blocks[original],
				...header(id, number),
			} as SyncBlock;
			const transactionHash = hash(id, number, "transaction");
			const transactions = block.transactions.map((transaction) => ({
				...transaction,
				hash: transactionHash,
				blockHash: block.hash,
				blockNumber: block.number,
			}));
			const logs = (template.logs as SyncLog[])
				.filter((log) => Number(log.blockNumber) === original)
				.map((log) => ({
					...log,
					data: skipTail && original === 3 ? ("0x01" as Hex) : log.data,
					transactionHash,
					blockHash: block.hash,
					blockNumber: block.number,
				}));
			await syncStore.insertBlocks({ chainId: id, blocks: [block] });
			await syncStore.insertTransactions({ chainId: id, transactions });
			await syncStore.insertLogs({ chainId: id, logs });
		}
	}

	const checkpoints = getPonderCheckpointTable(namespaceBuild.schema);
	const meta = getPonderMetaTable(namespaceBuild.schema);
	const snapshot = async (db: typeof database, ready: boolean) => {
		const positions = await db.adminQB.raw
			.select()
			.from(checkpoints)
			.orderBy(checkpoints.chainId);
		const [app] = await db.adminQB.raw.select().from(meta);
		expect(app?.value.is_ready).toBe(ready ? 1 : 0);
		const stored = await db.adminQB.raw.select().from(rows);
		return {
			calls: [...calls],
			positions,
			stored: stored.map(({ id }) => id).sort(),
		};
	};
	if (!fail) {
		await run();
		return { ...(await snapshot(database, true)), failAt };
	}

	await expect(run()).rejects.toThrow(marker);
	const failed = await snapshot(database, false);
	await shutdown.kill();
	shutdown = createShutdown();
	injectFailure = false;
	calls.length = 0;
	const reopened = await open();
	expect(reopened.crashRecoveryCheckpoint).toEqual(
		failed.positions.map(({ chainId, safeCheckpoint }) => ({
			chainId,
			checkpoint: safeCheckpoint,
		})),
	);
	await reopened.run();
	return {
		...failed,
		failAt,
		recovered: await snapshot(reopened.database, true),
	};
}

for (const querySize of [12_000, 6]) {
	for (const active of [[1, 2], [1], []]) {
		test(`query ${querySize}: active chains [${active}]`, async () => {
			const { calls, positions, stored } = await sparseRun(
				querySize,
				active,
				false,
			);
			const expected = expectedCalls(active);
			expect(calls).toEqual(expected);
			expect(stored).toEqual(expected.toSorted());
			expect(positions).toHaveLength(2);
			for (const position of positions) {
				const hasEvents = active.includes(position.chainId);
				const checkpoint = encodeCheckpoint(
					blockToCheckpoint(
						header(position.chainId, hasEvents ? end : 1),
						position.chainId,
						hasEvents ? "up" : "down",
					),
				);
				expect([
					position.safeCheckpoint,
					position.latestCheckpoint,
					position.finalizedCheckpoint,
				]).toEqual([checkpoint, checkpoint, checkpoint]);
			}
		});
	}

	test(`query ${querySize}: later chain callback rolls back its merged group`, async () => {
		const { calls, failAt, positions, stored, recovered } = await sparseRun(
			querySize,
			[1, 2],
			true,
		);
		const expected = expectedCalls([1, 2]);
		expect(calls).toEqual(expected.slice(0, expected.indexOf(failAt) + 1));
		expect(stored).toEqual(
			(querySize === 6 ? expected.slice(0, 16) : []).toSorted(),
		);
		expect(recovered?.calls).toEqual(expected.slice(stored.length));
		expect(recovered?.stored).toEqual(expected.toSorted());
		expect(positions).toHaveLength(2);
		for (const position of positions) {
			const checkpoint = encodeCheckpoint(
				blockToCheckpoint(
					header(position.chainId, 1),
					position.chainId,
					querySize === 6 ? "up" : "down",
				),
			);
			expect([
				position.safeCheckpoint,
				position.latestCheckpoint,
				position.finalizedCheckpoint,
			]).toEqual([checkpoint, checkpoint, checkpoint]);
		}
		for (const position of recovered?.positions ?? []) {
			const checkpoint = encodeCheckpoint(
				blockToCheckpoint(
					header(position.chainId, end),
					position.chainId,
					"up",
				),
			);
			expect([
				position.safeCheckpoint,
				position.latestCheckpoint,
				position.finalizedCheckpoint,
			]).toEqual([checkpoint, checkpoint, checkpoint]);
		}
	});
}

test("ABI-skipped final SQL page keeps the preceding source checkpoint", async () => {
	const { calls, positions, stored } = await sparseRun(6, [1, 2], false, true);
	const expected = expectedCalls([1, 2]).slice(0, 16);
	expect(calls).toEqual(expected);
	expect(stored).toEqual(expected.toSorted());
	expect(positions).toHaveLength(2);
	for (const position of positions) {
		const cp = encodeCheckpoint(
			blockToCheckpoint(header(position.chainId, 1), position.chainId, "up"),
		);
		expect([
			position.safeCheckpoint,
			position.latestCheckpoint,
			position.finalizedCheckpoint,
		]).toEqual([cp, cp, cp]);
	}
});
