import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
const root = path.resolve(import.meta.dirname, ".."),
	upstream = path.resolve(root, "../ponder-upstream");
const require = createRequire(path.join(upstream, "package.json")),
	ts = require("typescript");
const files = execFileSync("git", ["ls-files"], {
	cwd: upstream,
	encoding: "utf8",
})
	.trim()
	.split("\n");
const ledger = JSON.parse(
	fs.readFileSync(path.join(root, "parity/test-ledger.json")),
);
const audit = {
	baseline: ledger.baseline,
	method:
		"TypeScript 5.4.5 AST over all tracked JS/TS files, independently compared with filename-based manifest. Literal .each tables expanded; source ranges, inherited suites, modifiers, hooks, imports and local fixture graph preserved.",
	limitations: [
		"Static analysis cannot prove runtime collection completeness for arbitrary JavaScript execution, eval, computed property calls or test plugins. No claim of exhaustive runtime collection until full Vitest and typecheck collection succeeds.",
		"The 905 records are finite statically discovered instances, not 905 ports. Only literal each tables are expanded. Nested describe parameterization, nonliteral cases, loops or helper-generated cases require unresolved classification and runtime collection.",
		"Assertion expressions are source expressions, not the count of runtime assertion invocations; async branches can execute differently. Complete module source plus pinned baseline remains authoritative.",
		"Type tests require Go success/failure fixtures or retained SDK tests. Merely retaining TS source does not implement or pass them.",
		"Simulation is a seeded, app-parameterized external-RPC harness with unbounded seeds, not a finite Vitest test file. It is separately inventoried and remains unported/unexecuted.",
		"SDK/React ownership is retained separately; compatibility against Go requires original clients interacting with the Go server.",
	],
	test_imports: [],
	unaccounted_files: [],
	parameterized_calls: [],
	conditional_or_only: [],
	hook_and_fixture_files: [],
	support_files: [],
	external_packages: [],
	simulation: {
		status: "unimplemented",
		execution_status: "not_run",
		seed_space: "unbounded",
		apps: [],
		source_files: [],
		validation_sites: [],
		parameters_source: null,
	},
	counts: {},
};
const support = new Set(),
	external = new Set();
function sourceFile(file) {
	const text = fs.readFileSync(path.join(upstream, file), "utf8");
	return ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true);
}
function resolveLocal(from, spec) {
	let base;
	if (spec.startsWith("."))
		base = path.posix.normalize(
			path.posix.join(path.posix.dirname(from), spec),
		);
	else if (spec.startsWith("@/")) base = "packages/core/src/" + spec.slice(2);
	else if (spec.startsWith("@ponder/")) {
		const parts = spec.slice(8).split("/");
		if (["client", "utils", "react"].includes(parts[0]))
			base = `packages/${parts.shift()}/src/${parts.join("/") || "index"}`;
		else base = "packages/core/src/" + parts.join("/");
	} else return null;
	for (const candidate of [
		base,
		base.replace(/\.[cm]?js$/, ".ts"),
		base + ".ts",
		base + ".tsx",
		base + "/index.ts",
	])
		if (files.includes(candidate)) return candidate;
	return { missing: base };
}
function collectSupport(file) {
	if (support.has(file)) return;
	support.add(file);
	const sf = sourceFile(file),
		imports = [];
	for (const n of sf.statements) {
		if (ts.isImportDeclaration(n) || ts.isExportDeclaration(n)) {
			if (!n.moduleSpecifier || !ts.isStringLiteral(n.moduleSpecifier))
				continue;
			const spec = n.moduleSpecifier.text;
			const local = resolveLocal(file, spec);
			imports.push({ specifier: spec, resolved: local });
			if (typeof local === "string" && /\.[cm]?[jt]sx?$/.test(local))
				collectSupport(local);
			else if (local === null) external.add(spec);
		}
	}
	const content = sf.text;
	const hash = crypto.createHash("sha256").update(content).digest("hex");
	audit.support_files.push({ path: file, sha256: hash, imports });
	const dest = path.join(root, "parity/source-support", file);
	fs.mkdirSync(path.dirname(dest), { recursive: true });
	fs.writeFileSync(dest, content);
}
for (const file of files.filter((x) => /\.[cm]?[jt]sx?$/.test(x))) {
	const sf = sourceFile(file),
		aliases = new Map([
			["test", "test"],
			["it", "test"],
			["describe", "describe"],
			["suite", "describe"],
		]);
	let explicit = false;
	for (const n of sf.statements) {
		if (
			ts.isImportDeclaration(n) &&
			ts.isStringLiteral(n.moduleSpecifier) &&
			["vitest", "bun:test", "node:test"].includes(n.moduleSpecifier.text)
		) {
			const bindings = n.importClause?.namedBindings;
			if (bindings && ts.isNamedImports(bindings)) {
				for (const el of bindings.elements) {
					const imported = (el.propertyName ?? el.name).text;
					if (["test", "it", "describe", "suite"].includes(imported)) {
						aliases.set(
							el.name.text,
							["test", "it"].includes(imported) ? "test" : "describe",
						);
						explicit = true;
						audit.test_imports.push({
							file,
							imported,
							local: el.name.text,
							provider: n.moduleSpecifier.text,
						});
					}
				}
			} else if (bindings && ts.isNamespaceImport(bindings)) {
				audit.unaccounted_files.push({
					file,
					reason: "namespace test import requires runtime review",
				});
			}
		}
	}
	let calls = 0;
	const hooks = [];
	function scan(n) {
		if (ts.isCallExpression(n)) {
			let expr = n.expression;
			const mods = [];
			while (
				ts.isPropertyAccessExpression(expr) ||
				ts.isCallExpression(expr) ||
				ts.isTaggedTemplateExpression(expr)
			) {
				if (ts.isPropertyAccessExpression(expr)) {
					mods.push(expr.name.text);
					expr = expr.expression;
				} else if (ts.isCallExpression(expr)) {
					expr = expr.expression;
				} else expr = expr.tag;
			}
			if (ts.isIdentifier(expr) && aliases.has(expr.text)) {
				if (
					aliases.get(expr.text) === "test" &&
					n.arguments.length &&
					ts.isStringLiteralLike(n.arguments[0])
				)
					calls++;
				if (
					mods.some((x) => ["each", "for"].includes(x)) &&
					n.arguments.some(
						(x) => ts.isArrowFunction(x) || ts.isFunctionExpression(x),
					)
				)
					audit.parameterized_calls.push({
						file,
						line: sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1,
						source: n.getText(sf),
					});
				if (mods.some((x) => ["only", "skipIf", "runIf"].includes(x)))
					audit.conditional_or_only.push({
						file,
						modifiers: mods,
						source: n.getText(sf),
					});
			}
			if (
				ts.isIdentifier(n.expression) &&
				[
					"beforeEach",
					"beforeAll",
					"afterEach",
					"afterAll",
					"onTestFinished",
				].includes(n.expression.text)
			)
				hooks.push({ name: n.expression.text, source: n.getText(sf) });
		}
		ts.forEachChild(n, scan);
	}
	scan(sf);
	if (explicit && !ledger.files.some((x) => x.path === file) && calls)
		audit.unaccounted_files.push({
			file,
			reason: "test provider import and test calls outside filename manifest",
			calls,
		});
	if (ledger.files.some((x) => x.path === file)) {
		collectSupport(file);
		audit.hook_and_fixture_files.push({ file, hooks });
	}
	if (file.startsWith("simulation-test/") || file.startsWith("benchmark/")) {
		if (file.startsWith("simulation-test/")) {
			audit.simulation.source_files.push({
				path: file,
				sha256: crypto.createHash("sha256").update(sf.text).digest("hex"),
			});
			function validations(n) {
				if (ts.isThrowStatement(n))
					audit.simulation.validation_sites.push({
						file,
						line: sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1,
						source: n.getText(sf),
					});
				if (ts.isVariableDeclaration(n) && n.name.getText(sf) === "SIM_PARAMS")
					audit.simulation.parameters_source = n.getText(sf);
				ts.forEachChild(n, validations);
			}
			validations(sf);
		}
	}
}
for (const file of [
	"packages/core/src/_test/globalSetup.ts",
	"packages/core/vite.config.ts",
	"packages/client/vite.config.ts",
	"packages/react/vite.config.ts",
	"packages/utils/vite.config.ts",
	"packages/create-ponder/vite.config.ts",
])
	if (files.includes(file)) collectSupport(file);
audit.benchmarks = {
	status: "not_run",
	counting:
		"Performance scenarios are separate from finite functional test records.",
	files: files.filter(
		(x) => x.startsWith("benchmark/") || x.includes(".bench."),
	),
};
audit.external_packages = [...external].sort();
audit.simulation.apps = [
	...new Set(
		files
			.filter((x) => x.startsWith("simulation-test/apps/"))
			.map((x) => x.split("/")[2]),
	),
];
audit.counts = {
	test_files: ledger.files.length,
	static_records: ledger.tests.length,
	type_records: ledger.tests.filter((x) => x.kind === "type").length,
	inline_type_assertions: ledger.tests.filter(x=>x.kind!=="type").reduce((n,x)=>n+x.assertions.filter(a=>a.kind==="type").length,0),
	type_error_directives: ledger.tests.reduce((n,x)=>n+x.type_error_directives.length,0),
	client_records: ledger.tests.filter((x) =>
		x.source_file.startsWith("packages/client/"),
	).length,
	react_records: ledger.tests.filter((x) =>
		x.source_file.startsWith("packages/react/"),
	).length,
	e2e_records: ledger.tests.filter((x) => x.source_file.includes("/_test/e2e/"))
		.length,
	each_declarations: audit.parameterized_calls.length,
	support_files: support.size,
	unaccounted: audit.unaccounted_files.length,
	unresolved_records: ledger.unresolved_expansion_ids.length,
	simulation_apps: audit.simulation.apps.length,
};
fs.writeFileSync(
	path.join(root, "parity/discovery-audit.json"),
	JSON.stringify(audit, null, 2) + "\n",
);
console.log(audit.counts);
if (audit.unaccounted_files.length) process.exitCode = 1;
