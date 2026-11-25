// Check the original bidirectional event assignments and produce independent ABI vectors.
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

const appFixture = JSON.parse(
	fs.readFileSync(path.join(root, "config/testdata/virtual-names.json")),
);
assert.equal(appFixture.files[file], hash);
const input = appFixture.application;
const cases = [];
const id = "0x" + "11".repeat(32);
const other = "0x" + "22".repeat(32);
const address = "0x" + "ab".repeat(20);
const serial = (_key, value) =>
	typeof value === "bigint" ? value.toString() : value;
for (const st of tree.statements) {
	if (!ts.isExpressionStatement(st) || !ts.isCallExpression(st.expression))
		continue;
	const [title, cb] = st.expression.arguments;
	if (!title.text.startsWith("Event")) continue;
	const rec = records.find((t) => t.title === title.text);
	const [actual, expected] = cb.body.statements;
	const event = actual.type.typeArguments[1].literal.text;
	const strict = cb.body.statements
		.filter(ts.isExpressionStatement)
		.map((st) => {
			const call = st.expression;
			assert.equal(call.expression.getText(tree), "assertType");
			assert(
				checker.isTypeAssignableTo(
					checker.getTypeAtLocation(call.arguments[0]),
					checker.getTypeFromTypeNode(call.typeArguments[0]),
				),
			);
			return call.getText(tree);
		});
	assert.equal(strict.length, 2);
	const fields = expected.type.members.map((m) => ({
		name: m.name.text,
		optional: !!m.questionToken,
		type: m.type.getText(tree),
	}));
	const receipt = checker
		.getTypeFromTypeNode(actual.type)
		.getProperty("transactionReceipt");
	let availability = "absent";
	if (receipt) {
		const prop = checker.getTypeOfSymbolAtLocation(receipt, actual);
		const alternatives = prop.isUnion() ? prop.types : [prop];
		if (alternatives.some((t) => t.getProperty("status")))
			availability = alternatives.some((t) => t.flags & ts.TypeFlags.Undefined)
				? "optional"
				: "required";
	}
	const c = {
		id: rec.id,
		file,
		line: rec.line,
		title: title.text,
		event,
		fields,
		strict,
		receipt: availability,
	};
	if (event.startsWith("c")) {
		const [contract, label] = event.split(/[:.]/);
		const name = label.split("(")[0];
		const candidates = input.contracts[contract].abi.filter(
			(x) => x.name === name,
		);
		const item =
			candidates.length === 1
				? candidates[0]
				: candidates.find(
						(x) =>
							name + "(" + x.inputs.map((p) => p.type).join(",") + ")" ===
							label,
					);
		assert(item);
		c.item = item;
		if (item.type === "event") {
			const values = item.inputs.map((p, i) => (i === 0 ? id : other));
			const topics = viem.encodeEventTopics({
				abi: [item],
				eventName: item.name,
				args: Object.fromEntries(
					item.inputs.flatMap((p, i) =>
						p.indexed ? [[p.name, values[i]]] : [],
					),
				),
			});
			const non = item.inputs
				.map((p, i) => ({ p, i }))
				.filter((v) => !v.p.indexed);
			const data = viem.encodeAbiParameters(
				non.map((v) => v.p),
				non.map((v) => values[v.i]),
			);
			const decoded = viem.decodeEventLog({ abi: [item], topics, data });
			c.wire = { topics, data };
			c.args = decoded.args ?? [];
		} else {
			const args = item.inputs.map(() => address);
			const input = viem.encodeFunctionData({
				abi: [item],
				functionName: item.name,
				args,
			});
			const output = item.outputs.length
				? viem.encodeFunctionResult({
						abi: [item],
						functionName: item.name,
						result: 131n,
					})
				: "0x";
			const decoded = viem.decodeFunctionData({ abi: [item], data: input });
			c.wire = { input, output };
			c.args = decoded.args ?? null;
			c.result =
				viem.decodeFunctionResult({
					abi: [item],
					functionName: item.name,
					data: output,
				}) ?? null;
		}
	}
	cases.push(c);
}
assert.equal(cases.length, 8);
const eth = tree.fileName.replace("virtual.test-d.ts", "eth.ts"),
	ethTree = p.getSourceFile(eth);
const properties = {};
for (const st of ethTree.statements) {
	if (!ts.isTypeAliasDeclaration(st)) continue;
	properties[st.name.text] = checker
		.getPropertiesOfType(checker.getTypeFromTypeNode(st.type))
		.map((p) => p.name)
		.sort();
}
const ethFile = "packages/core/src/types/eth.ts";
const files = {
	[file]: hash,
	[ethFile]: createHash("sha256").update(ethTree.text).digest("hex"),
};
const fixture = { files, properties, cases };
const out = path.join(root, "config/testdata/virtual-events.json"),
	data = JSON.stringify(fixture, serial, 2) + "\n";
if (process.argv.includes("--write")) fs.writeFileSync(out, data);
else assert.equal(fs.readFileSync(out, "utf8"), data);
console.log(
	JSON.stringify({
		typescript: ts.version,
		cases: cases.length,
		strict_assertions: 16,
		files,
		fixture_sha256: createHash("sha256").update(data).digest("hex"),
	}),
);
