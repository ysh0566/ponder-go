// Capture original utility calls and verify all eight contextual type assertions.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";

const root = path.resolve(import.meta.dirname, "..");
const peer = path.resolve(
	process.env.PONDER_UPSTREAM ?? path.join(root, "../ponder-upstream"),
);
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
const sha = (data) => createHash("sha256").update(data).digest("hex");
const fixture = { files: {}, cases: [] };
const compilers = {};
const mark = (value) =>
	JSON.parse(
		JSON.stringify(value, (_, v) =>
			typeof v === "bigint" ? { $bigint: v.toString() } : v,
		),
	);
const files = [
	"packages/utils/src/mergeAbis.test.ts",
	"packages/utils/src/replaceBigInts.test.ts",
	"packages/core/src/utils/truncate.test.ts",
	"packages/core/src/utils/chains.test.ts",
];
for (const file of files) {
	const full = path.join(peer, file),
		src = fs.readFileSync(full, "utf8"),
		name = path.basename(file).split(".")[0];
	const require = createRequire(full),
		ts = require("typescript");
	compilers[file] = ts.version;
	const project = path.join(peer, file.split("/").slice(0, 2).join("/"));
	const opts = ts.parseJsonConfigFileContent(
		ts.readConfigFile(path.join(project, "tsconfig.json"), ts.sys.readFile)
			.config,
		ts.sys,
		project,
	).options;
	const program = ts.createProgram([full], {
			...opts,
			noEmit: true,
			skipLibCheck: true,
		}),
		tree = program.getSourceFile(full);
	const diagnostics = [
		...program.getSemanticDiagnostics(tree),
		...program.getSyntacticDiagnostics(tree),
	];
	assert.equal(
		diagnostics.length,
		0,
		ts.formatDiagnostics(diagnostics, {
			getCurrentDirectory: () => project,
			getCanonicalFileName: (x) => x,
			getNewLine: () => "\n",
		}),
	);
	const checker = program.getTypeChecker(),
		checked = new Map();
	function visit(node) {
		if (ts.isCallExpression(node) && node.expression.getText(tree) === "test") {
			const strict = [];
			function inspect(n) {
				if (
					ts.isCallExpression(n) &&
					n.expression.getText(tree) === "expectTypeOf"
				) {
					const expected = checker.getTypeFromTypeNode(n.typeArguments[0]),
						actual = checker.getTypeAtLocation(n.arguments[0]);
					assert(checker.isTypeAssignableTo(actual, expected));
					strict.push(checker.typeToString(expected));
				}
				ts.forEachChild(n, inspect);
			}
			inspect(node);
			checked.set(node.arguments[0].text, strict);
		} else ts.forEachChild(node, visit);
	}
	visit(tree);
	const module = await import(
		pathToFileURL(path.join(path.dirname(full), name + ".ts"))
	);
	const decls = ledger.tests.filter((t) => t.source_file === file);
	assert.equal(decls.length, checked.size);
	const digest = sha(src);
	assert(decls.every((t) => t.source_sha256 === digest));
	fixture.files[file] = digest;
	let c,
		index = 0;
	const dependencies = (id) => {
		if (id === "vitest")
			return {
				test: (title, fn) => {
					const decl = decls[index++];
					assert.equal(decl.title, title);
					c = {
						id: decl.id,
						file,
						line: decl.line,
						title,
						strict: checked.get(title),
						assertions: [],
					};
					fn();
					fixture.cases.push(c);
				},
				expectTypeOf: () => {},
				expect: (actual) =>
					Object.fromEntries(
						["toBe", "toStrictEqual", "toMatchObject"].map((method) => [
							method,
							(expected) => {
								assert.deepEqual(actual, expected);
								c.assertions.push({ method, expected: mark(expected) });
							},
						]),
					),
			};
		if (id === "./" + name + ".js") {
			if (name === "chains")
				return {
					get chains() {
						const exports = Object.entries(module.chains).map(([name, c]) => ({
							name,
							id: c.id,
						}));
						c.export_count = exports.length;
						c.exports_sha256 = sha(JSON.stringify(exports));
						return module.chains;
					},
				};
			return {
				[name]: (...args) => {
					c.input = mark(name === "replaceBigInts" ? args[0] : args);
					const out = module[name](...args);
					c.output = mark(out);
					return out;
				},
			};
		}
		if (["viem", "abitype"].includes(id)) return require(id);
		throw Error("Unexpected source import: " + id);
	};
	const js = ts.transpileModule(src, {
		compilerOptions: {
			module: ts.ModuleKind.CommonJS,
			target: ts.ScriptTarget.ES2022,
		},
	}).outputText;
	new Function("require", "exports", js)(dependencies, {});
	assert.equal(index, decls.length);
}
assert.equal(fixture.cases.length, 12);
assert.equal(
	fixture.cases.reduce((n, c) => n + c.strict.length, 0),
	8,
);
const data = JSON.stringify(fixture, null, 2) + "\n",
	out = path.join(root, "testdata/value-utilities.json");
if (process.argv.includes("--write")) fs.writeFileSync(out, data);
else assert.equal(fs.readFileSync(out, "utf8"), data);
console.log(
	JSON.stringify({
		cases: fixture.cases.length,
		strict_assertions: 8,
		compilers,
		files: fixture.files,
		fixture_sha256: sha(data),
	}),
);
