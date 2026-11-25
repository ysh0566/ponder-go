// Run with the pinned sibling upstream's dependencies installed and Anvil on PATH.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { createInterface } from "node:readline";

const require = createRequire(
	new URL(
		"../../../../ponder-upstream/packages/core/package.json",
		import.meta.url,
	),
);
const {
	createPublicClient,
	http,
	keccak256,
	toHex,
	parseAbi,
	encodeAbiParameters,
	encodeFunctionData,
	getAddress,
	namehash,
} = require("viem");
const { packetToBytes } = require(
	require.resolve("viem").replace(/index\.js$/, "utils/ens/packetToBytes.js"),
);

const resolver = "0x000000000000000000000000000000000000c011";
const account = getAddress("0x111122223333444455556666777788889999aaaa");
const next = getAddress("0x22223333444455556666777788889999aaaabbbb");
const gateways = ["https://example.invalid/{data}"];
const abi = parseAbi([
	"function addr(bytes32) view returns (address)",
	"function addr(bytes32,uint256) view returns (bytes)",
	"function text(bytes32,string) view returns (string)",
	"function resolveWithGateways(bytes,bytes,string[]) view returns (bytes,address)",
	"function set(bytes32,bytes)",
]);
const cases = [
	{ name: "alice.eth", value: account },
	{ name: "alice.eth", coin: 0, value: "0x001234" },
	{ name: "alice.eth", coin: 60, value: account.toLowerCase() },
	{
		name: "alice.eth",
		key: "url",
		gateways,
		value: "https://record.example/é",
	},
	{ name: "alice.eth", key: "empty", value: "", want: null },
	{ name: "alice.eth", key: "", gateways: [], value: "0x00" },
	{ name: "alice.eth", key: "unset", raw: "0x", want: null },
	{ name: "zero.eth", value: "0x" + "00".repeat(20), want: null },
	{ name: "alice.eth", coin: 1, value: "0x0000", want: null },
	{ name: "alice.eth", coin: 2, value: "0x", want: null },
	{
		name: "[" + "ab".repeat(32) + "].eth",
		key: "description",
		value: "labelhash",
	},
	{ name: "é.eth", key: "url", value: "unicode" },
];
const filename = (name) => new URL(name, import.meta.url);
function fixture(r) {
	const addr = r.key === undefined;
	const type = addr ? (r.coin === undefined ? "address" : "bytes") : "string";
	const args = [namehash(r.name)];
	if (!addr) args.push(r.key);
	else if (r.coin !== undefined) args.push(BigInt(r.coin));
	const data = encodeFunctionData({
		abi,
		functionName: addr ? "addr" : "text",
		args,
	});
	const call = encodeFunctionData({
		abi,
		functionName: "resolveWithGateways",
		args: [
			toHex(packetToBytes(r.name)),
			data,
			r.gateways ?? ["x-batch-gateway:true"],
		],
	});
	const bytes = r.raw ?? encodeAbiParameters([{ type }], [r.value]);
	const setter = encodeFunctionData({
		abi,
		functionName: "set",
		args: [keccak256(call), bytes],
	});
	return {
		name: r.name,
		coin: r.coin,
		key: r.key,
		gateways: r.gateways,
		want: r.want === null ? null : r.value,
		setter,
	};
}
const records = cases.map(fixture);
const replacement = fixture({ name: "alice.eth", value: next });
const anvil = spawn(
	"anvil",
	["--host", "127.0.0.1", "--port", "0", "--no-mining"],
	{ stdio: ["ignore", "pipe", "ignore"] },
);
const exit = new Promise((r) => anvil.once("exit", r));
try {
	let url;
	for await (const line of createInterface({ input: anvil.stdout })) {
		const match = line.match(/Listening on (127\.0\.0\.1:\d+)/);
		if (match) {
			url = "http://" + match[1];
			break;
		}
	}
	assert.ok(url);
	const c = createPublicClient({ transport: http(url), cacheTime: 0 });
	const rpc = (method, params = []) => c.request({ method, params });
	const [from] = await rpc("eth_accounts");
	const code = (await readFile(filename("RecordResolver.hex"), "utf8")).trim();
	await rpc("anvil_setCode", [resolver, code]);
	const set = (r) =>
		rpc("eth_sendTransaction", [
			{ from, to: resolver, data: r.setter, gas: "0x800000" },
		]);
	const read = (r, blockNumber) => {
		const opts = {
			name: r.name,
			universalResolverAddress: resolver,
			gatewayUrls: r.gateways,
			blockNumber,
		};
		return r.key === undefined
			? c.getEnsAddress({
					...opts,
					coinType: r.coin === undefined ? undefined : BigInt(r.coin),
				})
			: c.getEnsText({ ...opts, key: r.key });
	};
	for (const r of records) await set(r);
	await rpc("evm_mine");
	const snap = await rpc("evm_snapshot");
	await set(replacement);
	await rpc("evm_mine");
	for (const r of records) assert.equal(await read(r, 1n), r.want);
	assert.equal(await read(replacement, 2n), next);
	assert.equal(await read({ name: "missing.eth" }, 1n), null);
	await assert.rejects(() =>
		c.getEnsAddress({
			name: "missing.eth",
			universalResolverAddress: resolver,
			strict: true,
			blockNumber: 1n,
		}),
	);
	assert.equal(await rpc("evm_revert", [snap]), true);
	await rpc("anvil_dropAllTransactions");
	await rpc("evm_mine");
	assert.equal(await read(records[0], 2n), account);
	const contents =
		JSON.stringify({ resolver, records, replacement }, null, 2) + "\n";
	if (process.argv.includes("--write"))
		await writeFile(filename("records.json"), contents);
	else assert.equal(await readFile(filename("records.json"), "utf8"), contents);
	console.log(
		"viem 2.35.0 resolved all 12 records on Anvil, plus strict failure, two historical heights and a reorg",
	);
} finally {
	anvil.kill();
	await exit;
}
