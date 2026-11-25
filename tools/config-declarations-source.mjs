// Capture application declarations and check the original contextual typing.
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
const root = path.resolve(import.meta.dirname, "..");
const peer = path.resolve(
	process.env.PONDER_UPSTREAM ?? path.join(root, "../ponder-upstream"),
);
const core = path.join(peer, "packages/core"),
	require = createRequire(path.join(core, "package.json"));
const ts = require("typescript");
const files = ["index.test.ts", "address.test-d.ts"].map((x) =>
	path.join(core, "src/config", x),
);
const ledger = JSON.parse(
	fs.readFileSync(path.join(root, "parity/test-ledger.json"), "utf8"),
);
if (
	execFileSync("git", ["rev-parse", "HEAD"], {
		cwd: peer,
		encoding: "utf8",
	}).trim() !== ledger.baseline.revision
)
	throw Error("Wrong upstream revision");
const project = ts.parseJsonConfigFileContent(
	ts.readConfigFile(path.join(core, "tsconfig.json"), ts.sys.readFile).config,
	ts.sys,
	core,
);
const opts = { ...project.options, skipLibCheck: true, noEmit: true };
const checked = ts.createProgram(files, opts);
const capture = { files: {}, diagnostics: [], cases: [] };
const assertions = [];
function diagnostics(program) {
	return files.flatMap((file) => {
		const tree = program.getSourceFile(file);
		return [
			...program.getSemanticDiagnostics(tree),
			...program.getSyntacticDiagnostics(tree),
		].map((d) => ({
			file: path.basename(file),
			line: tree.getLineAndCharacterOfPosition(d.start).line + 1,
			code: d.code,
		}));
	});
}
if (diagnostics(checked).length)
	throw Error("Original targeted program has diagnostics");
const host = ts.createCompilerHost(opts),
	readFile = host.readFile;
host.readFile = (file) => {
	const text = readFile(file);
	return files.includes(file)
		? text.replace(/\/\/ @ts-expect-error[^\n]*/g, (m) => " ".repeat(m.length))
		: text;
};
capture.diagnostics = diagnostics(ts.createProgram(files, opts, host));
for (const file of files) {
	const tree = checked.getSourceFile(file),
		relative = path.relative(peer, file);
	const records = ledger.tests.filter((t) => t.source_file === relative);
	const hash = createHash("sha256").update(tree.text).digest("hex");
	const lines = tree.text
		.split("\n")
		.flatMap((s, i) => (s.includes("@ts-expect-error") ? [i + 2] : []));
	const found = capture.diagnostics.filter(
		(d) => d.file === path.basename(file),
	);
	if (
		records.some((t) => t.source_sha256 !== hash) ||
		JSON.stringify(found.map((d) => d.line)) !== JSON.stringify(lines)
	)
		throw Error("Source diagnostics changed in " + relative);
	capture.files[relative] = hash;
}
const checker = checked.getTypeChecker();
function literal(type, node) {
	if (type.isStringLiteral() || type.isNumberLiteral()) return type.value;
	if (type.flags & ts.TypeFlags.BooleanLiteral)
		return type.intrinsicName === "true";
	if (checker.isTupleType(type))
		return checker.getTypeArguments(type).map((t) => literal(t, node));
	if (type.flags & ts.TypeFlags.Object)
		return Object.fromEntries(
			type
				.getProperties()
				.map((p) => [
					p.name,
					literal(checker.getTypeOfSymbolAtLocation(p, node), node),
				]),
		);
	throw Error(
		"Unexpected factory declaration type: " + checker.typeToString(type),
	);
}
const declarations = checked.getSourceFile(files[1]);
const values = {};
for (const st of declarations.statements)
	if (ts.isVariableStatement(st))
		for (const d of st.declarationList.declarations)
			values[d.name.text] = literal(checker.getTypeAtLocation(d.name), d);
const types = [];
for (const st of declarations.statements) {
	if (!ts.isExpressionStatement(st)) continue;
	const [name, cb] = st.expression.arguments;
	const obj = cb.body.statements[0].expression.arguments[0];
	const entry = {};
	for (const p of obj.properties) {
		const v = p.initializer;
		entry[p.name.text] = ts.isAsExpression(v)
			? {}
			: ts.isIdentifier(v)
				? values[v.text]
				: v.text;
	}
	types.push({ name: name.text, input: entry });
}
// Call the actual original functions. Registration and type-only assertions
// are observed separately from the untouched Vitest run.
const { createConfig } = await import(
	pathToFileURL(path.join(core, "src/config/index.ts"))
);
const { factory } = await import(
	pathToFileURL(path.join(core, "src/config/address.ts"))
);
const tree = checked.getSourceFile(files[0]);
let strictAssignments = 0;
function checkStrictAssignment(node) {
	if (
		ts.isCallExpression(node) &&
		ts.isIdentifier(node.expression) &&
		node.expression.text === "expectTypeOf"
	) {
		if (node.typeArguments?.length !== 1 || node.arguments.length !== 1)
			throw Error("Unexpected source type assertion");
		const expected = checker.getTypeFromTypeNode(node.typeArguments[0]);
		const actual = checker.getTypeAtLocation(node.arguments[0]);
		if (!checker.isTypeAssignableTo(actual, expected))
			throw Error("Source configuration lost its contextual type");
		strictAssignments++;
	}
	ts.forEachChild(node, checkStrictAssignment);
}
checkStrictAssignment(tree);
const source = ts.transpileModule(tree.text, {
	compilerOptions: {
		...opts,
		module: ts.ModuleKind.CommonJS,
		target: ts.ScriptTarget.ES2022,
		noEmit: false,
	},
}).outputText;
const runtime = [];
let current;
const wrappedRequire = (id) => {
	if (id === "viem") return require(id);
	if (id === "vitest")
		return {
			test: (name, fn) => {
				current = { name };
				fn();
				runtime.push(current);
			},
			expectTypeOf: (value) => {
				assertions.push(value);
			},
		};
	if (id === "./index.js")
		return {
			createConfig: (input) => {
				const result = createConfig(input);
				if (result !== input)
					throw Error("Original configuration identity changed");
				current.input = result;
				return result;
			},
		};
	if (id === "./address.js")
		return {
			factory: (input) => {
				const result = factory(input);
				if (result !== input) throw Error("Original factory identity changed");
				return result;
			},
		};
	throw Error("Unexpected test dependency " + id);
};
new Function("require", "exports", source)(wrappedRequire, {});
for (const [i, cases] of [runtime, types].entries()) {
	const records = ledger.tests.filter(
		(t) => t.source_file === path.relative(peer, files[i]),
	);
	if (cases.length !== records.length) throw Error("Changed source case count");
	for (const [j, item] of cases.entries()) {
		const t = records[j];
		if (t.title !== item.name || !item.input)
			throw Error("Changed source declaration " + t.id);
		capture.cases.push({
			id: t.id,
			file: t.source_file,
			name: t.title,
			line: t.line,
			input: item.input,
		});
	}
}
if (
	capture.cases.length !== 14 ||
	assertions.length !== 2 ||
	strictAssignments !== 2 ||
	capture.diagnostics.length !== 3
)
	throw Error("Incomplete source declaration verification");
const out = path.join(root, "config/testdata/app-declarations.json"),
	data = JSON.stringify(capture, null, 2) + "\n";
if (process.argv.includes("--write")) fs.writeFileSync(out, data);
else if (fs.readFileSync(out, "utf8") !== data)
	throw Error("Native fixtures differ from source declarations");
console.log(
	JSON.stringify({
		typescript: ts.version,
		files: capture.files,
		fixture_sha256: createHash("sha256").update(data).digest("hex"),
		cases: capture.cases.length,
		strict_assertions: assertions.length,
		removed_diagnostics: capture.diagnostics,
	}),
);
