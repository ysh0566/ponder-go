import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { once } from "node:events";

export async function verifyAvatars({
	c,
	rpc,
	viem,
	resolver,
	from,
	fixture,
	filename,
}) {
	const token = "0x000000000000000000000000000000000000c012";
	const image = "data:image/png;base64,aWNvbg==";
	const other = "data:image/png;base64,bmV3";
	const svg = "<svg>é</svg>";
	const cid = "Qm" + "a".repeat(44);
	const padded = "0".repeat(62) + "2a";
	const assets = {
		"HEAD /ipfs/photo": { type: "image/png" },
		"HEAD /ipns/site/avatar": { type: "image/webp" },
		"HEAD /ar-photo": { type: "image/gif" },
		"HEAD /plain": { type: "image/png" },
		["HEAD /ipfs/" + cid]: { type: "image/png" },
		"HEAD /noimage": { type: "text/plain" },
		"HEAD /missing": { status: 404, type: "image/png" },
		["GET /ipfs/meta/" + padded]: {
			type: "application/json",
			body: JSON.stringify({ image_url: "ipns://site/avatar" }),
		},
		"GET /ipfs/meta/7": {
			type: "application/json",
			body: JSON.stringify({ image_data: svg }),
		},
		"GET /badjson": { type: "application/json", body: "bad json" },
	};
	const server = createServer((req, res) => {
		const item = assets[req.method + " " + req.url];
		res.writeHead(item?.status ?? (item ? 200 : 404), {
			"Content-Type": item?.type ?? "text/plain",
		});
		res.end(item?.body ?? "");
	});
	server.listen(0, "127.0.0.1");
	await once(server, "listening");
	try {
		const gateway = "http://127.0.0.1:" + server.address().port;
		const tokenABI = viem.parseAbi([
			"function set(bytes4 method,uint256 id,string value)",
		]);
		const tokenSet = (method, id, value) =>
			viem.encodeFunctionData({
				abi: tokenABI,
				functionName: "set",
				args: [
					viem.toFunctionSelector(method + "(uint256)"),
					BigInt(id),
					value,
				],
			});
		const nft = (kind, id) => "eip155:1/" + kind + ":" + token + "/" + id;
		const json64 = (v) =>
			"data:application/json;base64," +
			Buffer.from(JSON.stringify(v)).toString("base64");
		const svg64 =
			"data:image/svg+xml;base64," +
			Buffer.from(svg, "latin1").toString("base64");
		const tokenSetters = [
			tokenSet("tokenURI", 1, json64({ image: "unused-before-update" })),
			tokenSet("uri", 42, "ipfs://meta/0x{id}"),
			tokenSet("tokenURI", 7, "ipfs://meta/{id}"),
			tokenSet("tokenURI", 8, '{"image":"","image_data":"' + image + '"}'),
			tokenSet("tokenURI", 9, "ar://badjson"),
			tokenSet("tokenURI", 10, json64({ image_data: svg })),
			tokenSet(
				"tokenURI",
				11,
				json64({ image_data: svg }).replace("application/json", "text/plain"),
			),
		];
		const records = [
			["inline", image, image],
			["svg", "data:image/svg+xml," + svg, svg64],
			["ipfs", "ipfs://photo", "@/ipfs/photo"],
			["ipns", "ipns://site/avatar", "@/ipns/site/avatar"],
			["ar", "ar://ar-photo", "@/ar-photo"],
			["plain", "https://arweave.net/plain", "@/plain"],
			["cid", cid, "@/ipfs/" + cid],
			["httpctype", "ar://noimage", null],
			["httpstatus", "ar://missing", null],
			["garbage", "not-an-avatar", null],
			["erc721", nft("erc721", "1"), other],
			["erc1155", nft("erc1155", "0x2a"), "@/ipns/site/avatar"],
			["did", "did:nft:" + nft("erc721", "7").replaceAll("/", "_"), svg64],
			["onchain", nft("erc721", "8"), image],
			["badmeta", nft("erc721", "9"), null],
			[
				"encodedjson",
				nft("erc721", "10"),
				"data:image/svg+xml;base64," +
					Buffer.from(svg, "utf8").toString("base64"),
			],
			["encodedplain", nft("erc721", "11"), svg64],
			["badkind", nft("erc20", "1"), null],
			["badtoken", nft("erc721", "abc"), null],
			["empty", "", null],
		].map(([prefix, value, want]) => {
			const name = prefix + ".eth";
			return {
				name,
				want,
				setter: fixture({ name, key: "avatar", value }).setter,
			};
		});
		const replaceAvatar = fixture({
			name: "inline.eth",
			key: "avatar",
			value: other,
		}).setter;
		const replaceNFT = tokenSet("tokenURI", 1, json64({ image: other }));
		await rpc("anvil_reset", []);
		for (const [target, file] of [
			[resolver, "RecordResolver.hex"],
			[token, "AvatarToken.hex"],
		])
			await rpc("anvil_setCode", [
				target,
				(await readFile(filename(file), "utf8")).trim(),
			]);
		const put = (to, data) =>
			rpc("eth_sendTransaction", [{ from, to, data, gas: "0x800000" }]);
		for (const r of records) await put(resolver, r.setter);
		for (const data of tokenSetters) await put(token, data);
		await rpc("evm_mine");
		const snapshot = await rpc("evm_snapshot");
		await put(resolver, replaceAvatar);
		await put(token, replaceNFT);
		await rpc("evm_mine");
		const lookup = (name, blockNumber, strict = false) =>
			c.getEnsAvatar({
				name,
				blockNumber,
				strict,
				universalResolverAddress: resolver,
				assetGatewayUrls: { ipfs: gateway, arweave: gateway },
			});
		for (const r of records)
			assert.equal(
				await lookup(r.name, 1n),
				r.want?.replace(/^@/, gateway) ?? null,
				r.name,
			);
		assert.equal(await lookup("inline.eth", 2n), other);
		assert.equal(await lookup("absent.eth", 1n), null);
		await assert.rejects(() => lookup("absent.eth", 1n, true));
		assert.equal(await rpc("evm_revert", [snapshot]), true);
		await rpc("anvil_dropAllTransactions");
		await rpc("evm_mine");
		assert.equal(await lookup("inline.eth", 2n), image);
		const output =
			JSON.stringify(
				{
					resolver,
					token,
					assets,
					records,
					tokenSetters,
					replaceAvatar,
					replaceNFT,
					newAvatar: other,
				},
				null,
				2,
			) + "\n";
		if (process.argv.includes("--write"))
			await writeFile(filename("avatars.json"), output);
		else assert.equal(await readFile(filename("avatars.json"), "utf8"), output);
		console.log(
			"viem 2.35.0 resolved 20 avatar records, ERC-721/1155 metadata, strict failures, historical ENS and latest NFT reads, and a same-height reorg",
		);
	} finally {
		server.closeAllConnections();
		await new Promise((resolve) => server.close(resolve));
	}
}
