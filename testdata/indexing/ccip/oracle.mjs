// Execute the pinned viem client against the same Solidity fixture as the Go callbacks.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
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
	encodeFunctionData,
	pad,
	toHex,
	getAddress,
} = require("viem");
const { abi, code } = JSON.parse(
	await readFile(new URL("GatewayResolver.json", import.meta.url)),
);
const target = getAddress("0x000000000000000000000000000000000000cc1a");
const result = pad(toHex(42), { size: 32 });
const routes = [];
const server = createServer(async (req, res) => {
	routes.push([req.method, req.url]);
	if (req.url === "/bad") {
		res.writeHead(503);
		res.end("unavailable");
		return;
	}
	if (req.url === "/malformed") {
		res.setHeader("Content-Type", "application/json");
		res.end('{"data":"bad"}');
		return;
	}
	if (
		req.url === "/get/" + target.toLowerCase() + "/0x12" &&
		req.method === "GET"
	) {
		res.end(result);
		return;
	}
	if (req.url === "/post" && req.method === "POST") {
		let body = "";
		for await (const chunk of req) body += chunk;
		assert.deepEqual(JSON.parse(body), { data: "0x12", sender: target });
		res.setHeader("Content-Type", "application/json");
		res.end(JSON.stringify({ data: result }));
		return;
	}
	res.writeHead(404);
	res.end();
});
server.listen(0, "127.0.0.1");
await once(server, "listening");
const gateway = "http://127.0.0.1:" + server.address().port;
const anvil = spawn(
	"anvil",
	["--host", "127.0.0.1", "--port", "0", "--no-mining"],
	{ stdio: ["ignore", "pipe", "ignore"] },
);
const exit = new Promise((r) => anvil.once("exit", r));
try {
	let url;
	for await (const line of createInterface({ input: anvil.stdout })) {
		const m = line.match(/Listening on (127\.0\.0\.1:\d+)/);
		if (m) {
			url = "http://" + m[1];
			break;
		}
	}
	assert.ok(url);
	const c = createPublicClient({ transport: http(url), cacheTime: 0 });
	const rpc = (method, params = []) => c.request({ method, params });
	const [from] = await rpc("eth_accounts");
	await rpc("anvil_setCode", [target, code]);
	const tx = async (functionName, args) => {
		const data = encodeFunctionData({ abi, functionName, args });
		await rpc("eth_sendTransaction", [
			{ to: target, from, data, gas: "0x800000" },
		]);
		await rpc("evm_mine");
	};
	await tx("setup", [gateway]);
	await tx("setOffset", [5n]);
	const opts = { abi, address: target, blockNumber: 1n, functionName: "value" };
	for (const i of [0, 1, 7])
		assert.equal(await c.readContract({ ...opts, args: [i] }), 47n);
	for (const i of [2, 3, 5, 6])
		await assert.rejects(() => c.readContract({ ...opts, args: [i] }));
	const ens = {
		name: "alice.eth",
		blockNumber: 1n,
		universalResolverAddress: target,
	};
	assert.equal(
		await c.getEnsAddress(ens),
		getAddress("0x0000000000000000000000000000000000000abc"),
	);
	assert.equal(
		await c.getEnsText({ ...ens, key: "avatar" }),
		"data:image/png;base64,aWNvbg==",
	);
	assert.equal(await c.getEnsAvatar(ens), "data:image/png;base64,aWNvbg==");
	assert.equal(
		await c.getEnsName({
			address: target,
			universalResolverAddress: target,
			blockNumber: 1n,
		}),
		"alice.eth",
	);
	assert.equal(await c.getEnsResolver(ens), target);
	assert.deepEqual(
		await c.multicall({
			contracts: [{ abi, address: target, functionName: "value", args: [1] }],
			multicallAddress: target,
			account: target,
			allowFailure: false,
			blockNumber: 1n,
		}),
		[42n],
	);
	assert.ok(routes.some(([m, path]) => m === "GET" && path.endsWith("/0x12")));
	console.log(
		"viem 2.35.0 and Anvil verified EIP-3668 GET/POST, fallback, latest verification at an older event block, nested contract lookups, batch successes and encoded failures, ENS address/text/avatar",
	);
} finally {
	anvil.kill();
	await exit;
	server.closeAllConnections();
	await new Promise((r) => server.close(r));
}
