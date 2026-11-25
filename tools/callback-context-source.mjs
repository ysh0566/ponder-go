// Verify compiled callback contexts, the original registry accesses and viem ENS wire values.
import fs from "node:fs";
import path from "node:path";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
const root = path.resolve(import.meta.dirname, ".."),
	peer = path.resolve(
		process.env.PONDER_UPSTREAM ?? path.join(root, "../ponder-upstream"),
	);
const core = path.join(peer, "packages/core"),
	req = createRequire(path.join(core, "package.json")),
	ts = req("typescript"),
	viem = req("viem");
const file = "packages/core/src/types/virtual.test-d.ts",
	full = path.join(peer, file);
const ledger = JSON.parse(
	fs.readFileSync(path.join(root, "parity/test-ledger.json")),
);
assert.equal(
	execFileSync("git", ["rev-parse", "HEAD"], {
		cwd: peer,
		encoding: "utf8",
	}).trim(),
	ledger.baseline.revision,
);
const opts = {
	...ts.parseJsonConfigFileContent(
		ts.readConfigFile(path.join(core, "tsconfig.json"), ts.sys.readFile).config,
		ts.sys,
		core,
	).options,
	noEmit: true,
	skipLibCheck: true,
};
const p = ts.createProgram([full], opts),
	tree = p.getSourceFile(full),
	checker = p.getTypeChecker();
const ds = [
	...p.getSemanticDiagnostics(tree),
	...p.getSyntacticDiagnostics(tree),
];
assert.equal(
	ds.length,
	0,
	ts.formatDiagnostics(ds, {
		getCurrentDirectory: () => core,
		getCanonicalFileName: (x) => x,
		getNewLine: () => "\n",
	}),
);
const records = ledger.tests.filter((t) => t.source_file === file),
	hash = createHash("sha256").update(tree.text).digest("hex");
assert(records.length === 22 && records.every((t) => t.source_sha256 === hash));

const wanted = [
	"Context db",
	"Context client",
	"Context contracts",
	"Registry",
];
const cases = [];
for (const st of tree.statements) {
	if (!ts.isExpressionStatement(st) || !ts.isCallExpression(st.expression))
		continue;
	const [title, cb] = st.expression.arguments;
	if (!wanted.includes(title.text)) continue;
	const rec = records.find((t) => t.title === title.text);
	const strict = [],
		accesses = [],
		members = {};
	for (const statement of cb.body.statements) {
		if (
			ts.isTypeAliasDeclaration(statement) &&
			statement.name.text.startsWith("expected")
		) {
			members[statement.name.text] = statement.type.getText(tree);
		}
		if (!ts.isExpressionStatement(statement)) continue;
		const call = statement.expression;
		if (call.expression.getText(tree) === "assertType") {
			assert(
				checker.isTypeAssignableTo(
					checker.getTypeAtLocation(call.arguments[0]),
					checker.getTypeFromTypeNode(call.typeArguments[0]),
				),
			);
			strict.push(call.getText(tree));
		} else if (call.expression.getText(tree) === "ponder.on") {
			assert.equal(call.arguments[0].text, "c1:Event0");
			for (const expr of call.arguments[1].body.statements) {
				assert(ts.isPropertyAccessExpression(expr.expression));
				assert(
					!(
						checker.getTypeAtLocation(expr.expression).flags & ts.TypeFlags.Any
					),
				);
				accesses.push(expr.expression.getText(tree));
			}
		} else throw Error(call.getText(tree));
	}
	cases.push({
		id: rec.id,
		file,
		line: rec.line,
		title: title.text,
		strict,
		accesses,
		members,
	});
}
assert.equal(cases.length, 4);
assert.equal(cases.flatMap((c) => c.strict).length, 11);
assert.equal(cases.flatMap((c) => c.accesses).length, 10);
const account = tree.statements
	.filter(ts.isVariableStatement)
	.flatMap((s) => [...s.declarationList.declarations])
	.find((d) => d.name.text === "account").initializer;
assert.equal(account.expression.getText(tree), "onchainTable");
const columns = account.arguments[1].body.expression.properties.map((p) => {
	const init = p.initializer.getText(tree);
	assert(["p.hex().primaryKey()", "p.bigint().notNull()"].includes(init));
	return {
		name: p.name.text,
		type: init.includes("hex") ? "hex" : "bigint",
		...(init.includes("primaryKey") ? { primary: true } : { notNull: true }),
	};
});
const schema = { tables: [{ name: account.arguments[0].text, columns }] };
const abiModule = req(
	path.join(path.dirname(req.resolve("viem")), "constants/abis.js"),
);
const resolverABI = abiModule.universalResolverReverseAbi;
assert.deepEqual(
	resolverABI,
	JSON.parse(fs.readFileSync(path.join(root, "indexing/ens_reverse.json"))),
);
const mainnet = req("viem/chains").mainnet;
const address = "0x" + "12".repeat(20),
	blockNumber = BigInt(mainnet.contracts.ensUniversalResolver.blockCreated + 1);
const requests = [];
const run = async (name, strict, errorData) => {
	const response = viem.encodeFunctionResult({
		abi: resolverABI,
		functionName: "reverseWithGateways",
		result: [name, address, address],
	});
	const client = viem.createPublicClient({
		chain: mainnet,
		transport: viem.custom(
			{
				request: async (req) => {
					requests.push(req);
					if (errorData)
						throw Object.assign(new Error("execution reverted"), {
							code: 3,
							data: errorData,
						});
					return response;
				},
			},
			{ retryCount: 0 },
		),
	});
	try {
		return {
			result: await client.getEnsName({ address, blockNumber, strict }),
			response,
		};
	} catch (error) {
		if (!strict) throw error;
		return { error: true };
	}
};
const found = await run("sample.eth", false),
	empty = await run("", false);
const missingData = viem.encodeErrorResult({
	abi: resolverABI,
	errorName: "ResolverNotFound",
	args: ["0x"],
});
const missing = await run("", false, missingData),
	reverted = await run("", true, missingData);
assert.equal(found.result, "sample.eth");
assert.equal(empty.result, null);
assert.equal(missing.result, null);
assert(reverted.error);
assert(
	requests.length >= 4 &&
		requests.every(
			(req) =>
				req.method === "eth_call" &&
				JSON.stringify(req.params) === JSON.stringify(requests[0].params),
		),
);
const ens = {
	address,
	resolver: mainnet.contracts.ensUniversalResolver,
	block: Number(blockNumber),
	request: requests[0],
	found,
	empty,
	missing_data: missingData,
};
const fixture = { files: { [file]: hash }, schema, cases, ens };
const out = path.join(root, "config/testdata/callback-context.json"),
	data = JSON.stringify(fixture, null, 2) + "\n";
if (process.argv.includes("--write")) fs.writeFileSync(out, data);
else assert.equal(fs.readFileSync(out, "utf8"), data);
console.log(
	JSON.stringify({
		typescript: ts.version,
		cases: 4,
		strict_assertions: 11,
		registry_accesses: 10,
		files: fixture.files,
		fixture_sha256: createHash("sha256").update(data).digest("hex"),
	}),
);
