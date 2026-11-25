// Verify Virtual.FormatEventNames and chain contexts in the pinned compiler.
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
function fromExpression(e) {
	if (ts.isObjectLiteralExpression(e))
		return Object.fromEntries(
			e.properties.map((prop) => [
				prop.name.text,
				fromExpression(prop.initializer),
			]),
		);
	if (ts.isArrayLiteralExpression(e)) return e.elements.map(fromExpression);
	if (ts.isIdentifier(e) && Object.hasOwn(variables, e.text))
		return variables[e.text];
	if (ts.isStringLiteral(e)) return e.text;
	if (ts.isNumericLiteral(e)) return Number(e.text);
	if (
		e.kind === ts.SyntaxKind.TrueKeyword ||
		e.kind === ts.SyntaxKind.FalseKeyword
	)
		return e.kind === ts.SyntaxKind.TrueKeyword;
	throw Error("Unexpected config expression " + e.getText(tree));
}
const variables = {},
	input = {};
for (const s of tree.statements) {
	if (ts.isVariableStatement(s))
		for (const d of s.declarationList.declarations) {
			const f = d.initializer;
			if (
				ts.isCallExpression(f) &&
				f.expression.getText(tree) === "parseAbiItem"
			)
				variables[d.name.text] = viem.parseAbiItem(f.arguments[0].text);
			if (
				ts.isCallExpression(f) &&
				f.expression.getText(tree) === "createConfig"
			)
				Object.assign(input, fromExpression(f.arguments[0]));
		}
}
const abiAlias = tree.statements.find(
	(n) => ts.isTypeAliasDeclaration(n) && n.name.text === "abi",
);
const abi = abiAlias.type.type.elements.map((t) => {
	const alias = tree.statements.find(
		(n) => ts.isTypeAliasDeclaration(n) && n.name.text === t.typeName.text,
	);
	return variables[alias.type.exprName.text];
});
const fixture = { files: { [file]: hash }, application: input, cases: [] };
const chainCases = new Set([
	"Context single chain",
	"Context multi chain",
	"Context block chain",
	"Context chain without event",
]);
for (const st of tree.statements) {
	if (!ts.isExpressionStatement(st) || !ts.isCallExpression(st.expression))
		continue;
	const [title, cb] = st.expression.arguments;
	if (!title.text.startsWith("FormatEventName") && !chainCases.has(title.text))
		continue;
	const rec = records.find((t) => t.title === title.text);
	const [a] = cb.body.statements;
	const strict = cb.body.statements
		.filter(
			(n) =>
				ts.isExpressionStatement(n) &&
				n.expression.expression.getText(tree) === "assertType",
		)
		.map((n) => {
			const call = n.expression,
				got = checker.getTypeAtLocation(call.arguments[0]),
				want = checker.getTypeFromTypeNode(call.typeArguments[0]);
			assert(checker.isTypeAssignableTo(got, want));
			return call.getText(tree);
		});
	assert.equal(strict.length, 2);
	const type = checker.getTypeFromTypeNode(a.type);
	const union = type.isUnion() ? type.types : [type];
	const c = { id: rec.id, file, line: rec.line, title: title.text, strict };
	if (chainCases.has(title.text)) {
		c.event = ts.isLiteralTypeNode(a.type.objectType.typeArguments[2])
			? a.type.objectType.typeArguments[2].literal.text
			: null;
		c.chains = union
			.map((t) =>
				Object.fromEntries(
					["name", "id"].map((k) => [
						k,
						checker.getTypeOfSymbolAtLocation(t.getProperty(k), a).value,
					]),
				),
			)
			.sort((a, b) => a.id - b.id);
	} else {
		c.names = union
			.map((t) => {
				assert(t.isStringLiteral());
				return t.value;
			})
			.sort();
		const sections = ["contracts", "accounts", "blocks"];
		const decl = {};
		let dynamic = [];
		a.type.typeArguments.forEach((kind, i) => {
			const entries = {};
			for (const source of kind.members) {
				const props = {};
				for (const field of source.type.members) {
					if (field.name.text === "abi") {
						const text = field.type.getText(tree);
						assert(["abi", "abi[number][]", "Abi"].includes(text));
						c.abi_shape = text;
						props.abi = text === "Abi" ? [] : abi;
						if (text === "Abi") dynamic.push(source.name.text);
					} else if (ts.isLiteralTypeNode(field.type)) {
						const lit = field.type.literal;
						props[field.name.text] = ts.isStringLiteral(lit)
							? lit.text
							: ts.isNumericLiteral(lit)
								? Number(lit.text)
								: lit.kind === ts.SyntaxKind.TrueKeyword;
					} else throw Error("Unexpected source field " + field.getText(tree));
				}
				entries[source.name.text] = props;
			}
			decl[sections[i]] = entries;
		});
		c.configuration = decl;
		c.dynamic_abis = dynamic;
	}
	fixture.cases.push(c);
}
assert.equal(fixture.cases.length, 10);
const out = path.join(root, "config/testdata/virtual-names.json"),
	data = JSON.stringify(fixture, null, 2) + "\n";
if (process.argv.includes("--write")) fs.writeFileSync(out, data);
else assert.equal(fs.readFileSync(out, "utf8"), data);
console.log(
	JSON.stringify({
		typescript: ts.version,
		cases: 10,
		strict_assertions: 20,
		files: fixture.files,
		fixture_sha256: createHash("sha256").update(data).digest("hex"),
	}),
);
