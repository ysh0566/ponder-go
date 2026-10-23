import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
const root = path.resolve(import.meta.dirname, "..");
const upstream = path.resolve(
	root,
	process.env.PONDER_UPSTREAM ?? "../ponder-upstream",
);
const require = createRequire(path.join(upstream, "package.json"));
const ts = require("typescript");
const revision = execFileSync("git", ["rev-parse", "HEAD"], {
	cwd: upstream,
	encoding: "utf8",
}).trim();
if (revision !== "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334")
	throw Error("Wrong oracle revision");
const files = execFileSync("git", ["ls-files"], {
	cwd: upstream,
	encoding: "utf8",
})
	.trim()
	.split("\n");
const hash = (text) => crypto.createHash("sha256").update(text).digest("hex");
const write = (file, data) => {
	fs.mkdirSync(path.dirname(file), { recursive: true });
	fs.writeFileSync(
		file,
		typeof data === "string" ? data : JSON.stringify(data, null, 2) + "\n",
	);
};
const mappings = fs.existsSync(path.join(root, "parity/mappings.json"))
	? JSON.parse(fs.readFileSync(path.join(root, "parity/mappings.json")))
	: [];
const tests = [],
	testFiles = [],
	unresolved = [];
function callInfo(expr) {
	const modifiers = [];
	let parameters;
	while (true) {
		if (ts.isPropertyAccessExpression(expr)) {
			modifiers.unshift(expr.name.text);
			expr = expr.expression;
		} else if (ts.isCallExpression(expr)) {
			parameters = expr.arguments.map((x) => x.getText());
			expr = expr.expression;
		} else if (ts.isTaggedTemplateExpression(expr)) {
			parameters = [expr.template.getText()];
			expr = expr.tag;
		} else break;
	}
	return {
		kind: ts.isIdentifier(expr) ? expr.text : undefined,
		modifiers,
		parameters,
	};
}
function literal(n, env) {
	if (!n) return undefined;
	if (
		ts.isAsExpression(n) ||
		ts.isSatisfiesExpression(n) ||
		ts.isParenthesizedExpression(n)
	)
		return literal(n.expression, env);
	if (ts.isStringLiteralLike(n)) return n.text;
	if (ts.isNumericLiteral(n)) return Number(n.text);
	if (n.kind === ts.SyntaxKind.TrueKeyword) return true;
	if (n.kind === ts.SyntaxKind.FalseKeyword) return false;
	if (ts.isIdentifier(n)) return env[n.text];
	if (ts.isPropertyAccessExpression(n) && n.getText() === "Number.NaN")
		return { $special: "NaN" };
	if (ts.isArrayLiteralExpression(n))
		return n.elements.map((x) => literal(x, env));
	if (ts.isPrefixUnaryExpression(n) && n.operator === ts.SyntaxKind.MinusToken)
		return -literal(n.operand, env);
	return undefined;
}
for (const file of files.filter((x) =>
	/\.(test|spec)(-d)?\.[cm]?[jt]sx?$/.test(x),
)) {
	const source = fs.readFileSync(path.join(upstream, file), "utf8");
	const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
	const fileHash = hash(source);
	const start = tests.length;
	let testDeclarationCount = 0;
	write(path.join(root, "parity/source-tests", file), source);
	const env = {};
	const declarations = (node) => {
		if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name)) {
			const v = literal(node.initializer, env);
			if (v !== undefined) env[node.name.text] = v;
		}
		ts.forEachChild(node, declarations);
	};
	declarations(sf);
	function visit(node, suites = [], contexts = []) {
		if (ts.isCallExpression(node)) {
			const info = callInfo(node.expression);
			if (["describe", "suite", "test", "it"].includes(info.kind)) {
				const name = literal(node.arguments[0], env) ?? {
					expression: node.arguments[0]?.getText(sf),
				};
				const callback = node.arguments.find(
					(x) => ts.isArrowFunction(x) || ts.isFunctionExpression(x),
				);
				if (["describe", "suite"].includes(info.kind)) {
					if (callback)
						visit(
							callback,
							[
								...suites,
								{
									name,
									modifiers: info.modifiers,
									parameters: info.parameters,
								},
							],
							contexts,
						);
					return;
				}
				testDeclarationCount++;
				let parameters = [null];
				if (info.modifiers.includes("each") || info.modifiers.includes("for")) {
					const factory = ts.isCallExpression(node.expression)
						? node.expression
						: undefined;
					const values = factory?.arguments[0]
						? literal(factory.arguments[0], env)
						: undefined;
					parameters = Array.isArray(values)
						? values
						: [{ unresolved: info.parameters }];
				}
				for (
					let parameterIndex = 0;
					parameterIndex < parameters.length;
					parameterIndex++
				) {
					const parameter = parameters[parameterIndex];
					let resolvedName =
						typeof name === "string" ? name : JSON.stringify(name);
					if (parameter !== null && !parameter?.unresolved) {
						let i = 0;
						const values = Array.isArray(parameter) ? parameter : [parameter];
						resolvedName = resolvedName.replace(/%[sdifjo#]/g, (x) => {
							const v = values[i++];
							return x === "%#"
								? String(parameterIndex)
								: String(v?.$special ?? v);
						});
					}
					const assertions = [],
						inputs = [];
					function scan(n) {
						if (ts.isCallExpression(n)) {
							let target = n;
							while (
								ts.isPropertyAccessExpression(target) ||
								ts.isCallExpression(target)
							)
								target = target.expression;
							let outer = n,
								ancestor = n.parent;
							while (
								ancestor &&
								(ts.isPropertyAccessExpression(ancestor) ||
									ts.isElementAccessExpression(ancestor) ||
									ts.isCallExpression(ancestor))
							) {
								if (ts.isCallExpression(ancestor)) outer = ancestor;
								ancestor = ancestor.parent;
							}
							if (
								ts.isIdentifier(target) &&
								["expect", "expectTypeOf", "assertType", "assert"].includes(
									target.text,
								) &&
								outer === n
							)
								assertions.push({
									kind: ["expectTypeOf", "assertType"].includes(target.text) ? "type" : "runtime",
									line:
										sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1,
									text: n.getText(sf),
								});
						}
						if (ts.isVariableDeclaration(n)) inputs.push(n.getText(sf));
						ts.forEachChild(n, scan);
					}
					if (callback) scan(callback);
					const id = hash(
						`${file}:${node.getStart(sf)}:${parameterIndex}`,
					).slice(0, 20);
					const inherited = suites.flatMap((x) => x.modifiers);
					const modifiers = [...inherited, ...info.modifiers];
					const mapping = mappings.find((x) => x.source_id === id);
					if (
						mapping?.assertions_sha256 &&
						mapping.assertions_sha256 !==
							hash(assertions.map((x) => x.text).join("\n"))
					)
						throw Error(`Source assertions changed for ${id}`);
					const entry = {
						id,
						source_file: file,
						source_sha256: fileHash,
						line: sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1,
						start: node.getStart(sf),
						end: node.end,
						suite_path: suites,
						title: name,
						full_name: [
							...suites.map((x) =>
								typeof x.name === "string" ? x.name : JSON.stringify(x.name),
							),
							resolvedName,
						].join(" > "),
						parameter_index: parameterIndex,
						parameter,
						parameter_expression: info.parameters ?? null,
						contexts,
						source_state: modifiers.includes("todo")
							? "todo"
							: modifiers.includes("skip")
								? "skip"
								: modifiers.includes("fails")
									? "expected_failure"
									: "active",
						modifiers,
						kind: file.includes(".test-d.")
							? "type"
							: file.startsWith("packages/react/")
								? "react"
								: file.startsWith("packages/client/")
									? "client"
									: "runtime",
						test_source: node.getText(sf),
						inputs,
						assertions,
						type_error_directives:
							node
								.getText(sf)
								.match(/\/\/\s*@ts-(?:expect-error|ignore)[^\n]*/g) ?? [],
						discovered: true,
						port_status: mapping ? "ported" : "unimplemented",
						mapping: mapping ?? null,
						execution_status: "not_run",
						pass_status: null,
						blockers: [],
					};
					if (
						parameter?.unresolved ||
						contexts.length ||
						suites.some((x) => x.parameters)
					) {
						entry.expansion_status = "requires_runtime_collection";
						unresolved.push(id);
					} else entry.expansion_status = "statically_expanded";
					tests.push(entry);
				}
				return;
			}
		}
		const next =
			ts.isForOfStatement(node) ||
			ts.isForInStatement(node) ||
			ts.isForStatement(node)
				? [
						...contexts,
						{ kind: ts.SyntaxKind[node.kind], source: node.getText(sf) },
					]
				: contexts;
		ts.forEachChild(node, (x) => visit(x, suites, next));
	}
	visit(sf);
	testFiles.push({
		path: file,
		sha256: fileHash,
		test_declarations: testDeclarationCount,
		expanded_records: tests.length - start,
		source_copy: `source-tests/${file}`,
	});
}
const publicPackages = [];
for (const file of files.filter((x) =>
	/^packages\/[^/]+\/package.json$/.test(x),
)) {
	const pkg = JSON.parse(fs.readFileSync(path.join(upstream, file)));
	const dir = path.dirname(file);
	const entry = path.join(dir, "src/index.ts");
	const symbols = [];
	if (files.includes(entry)) {
		const sf = ts.createSourceFile(
			entry,
			fs.readFileSync(path.join(upstream, entry), "utf8"),
			ts.ScriptTarget.Latest,
			true,
		);
		for (const statement of sf.statements) {
			if (
				statement.modifiers?.some(
					(m) => m.kind === ts.SyntaxKind.ExportKeyword,
				) ||
				ts.isExportDeclaration(statement)
			)
				symbols.push(statement.getText(sf));
		}
	}
	publicPackages.push({
		name: pkg.name,
		version: pkg.version,
		path: dir,
		exports: pkg.exports ?? null,
		bin: pkg.bin ?? null,
		scripts: pkg.scripts ?? {},
		public_declarations: symbols,
	});
}
const cliFile = "packages/core/src/bin/ponder.ts";
const cliText = fs.readFileSync(path.join(upstream, cliFile), "utf8");
const cliSF = ts.createSourceFile(
	cliFile,
	cliText,
	ts.ScriptTarget.Latest,
	true,
);
const cli = [];
function cliScan(n) {
	let owner = n.parent;
	while (owner && !ts.isVariableDeclaration(owner)) owner = owner.parent;
	const commandOwner = owner?.name?.getText(cliSF) ?? null;
	if (
		ts.isNewExpression(n) &&
		ts.isIdentifier(n.expression) &&
		n.expression.text === "Command"
	)
		cli.push({
			kind: "command",
			owner: commandOwner,
			arguments: n.arguments?.map((x) => x.getText(cliSF)) ?? [],
			line: cliSF.getLineAndCharacterOfPosition(n.getStart(cliSF)).line + 1,
		});
	if (
		ts.isCallExpression(n) &&
		ts.isPropertyAccessExpression(n.expression) &&
		["command", "option", "argument", "requiredOption"].includes(
			n.expression.name.text,
		)
	)
		cli.push({
			kind: n.expression.name.text,
			owner: commandOwner,
			arguments: n.arguments.map((x) => x.getText(cliSF)),
			line: cliSF.getLineAndCharacterOfPosition(n.getStart(cliSF)).line + 1,
		});
	ts.forEachChild(n, cliScan);
}
cliScan(cliSF);
const configFile = "packages/core/src/config/index.ts";
const configText = fs.readFileSync(path.join(upstream, configFile), "utf8");
const configSF = ts.createSourceFile(
	configFile,
	configText,
	ts.ScriptTarget.Latest,
	true,
);
const configs = [];
function cfg(n) {
	if (ts.isPropertySignature(n))
		configs.push({
			name: n.name.getText(configSF),
			optional: !!n.questionToken,
			type: n.type?.getText(configSF),
			line:
				configSF.getLineAndCharacterOfPosition(n.getStart(configSF)).line + 1,
		});
	ts.forEachChild(n, cfg);
}
cfg(configSF);
const inventory = {
	baseline: {
		repository: "https://github.com/ponder-sh/ponder",
		revision,
		core_version: "0.18.2",
	},
	packages: publicPackages,
	cli: { source: cliFile, entries: cli },
	configuration: { source: configFile, properties: configs },
	templates: files
		.filter(
			(x) =>
				x.startsWith("packages/create-ponder/templates/") ||
				x.startsWith("examples/"),
		)
		.map((x) => ({
			path: x,
			sha256: hash(fs.readFileSync(path.join(upstream, x))),
		})),
	docs: files.filter(
		(x) =>
			x.startsWith("docs/pages/docs/") && !/^docs\/pages\/docs\/0\./.test(x),
	),
};
const summary = {
	source_files: testFiles.length,
	test_declarations: testFiles.reduce((n, x) => n + x.test_declarations, 0),
	expanded_static_records: tests.length,
	unresolved_expansion_records: unresolved.length,
	assertion_expressions: tests.reduce((n, x) => n + x.assertions.length, 0),
	type_records: tests.filter((x) => x.kind === "type").length,
	inline_type_assertions: tests.filter(x => x.kind !== "type").reduce((n, x) => n + x.assertions.filter(a => a.kind === "type").length, 0),
	type_error_directives: tests.reduce((n, x) => n + x.type_error_directives.length, 0),
	source_states: Object.fromEntries(
		["active", "skip", "todo", "expected_failure"].map((x) => [
			x,
			tests.filter((t) => t.source_state === x).length,
		]),
	),
	ported: tests.filter((x) => x.port_status === "ported").length,
	executed: 0,
	passed: 0,
	failed: 0,
	blocked: 0,
	unimplemented: tests.filter((x) => x.port_status === "unimplemented").length,
};
write(path.join(root, "parity/test-ledger.json"), {
	format_version: 1,
	baseline: inventory.baseline,
	counting:
		"Records expand literal test.each tables; dynamic contexts are explicitly unresolved. Discovery is not implementation or execution. Runtime collection is authoritative for instantiated case counts.",
	summary,
	unresolved_expansion_ids: unresolved,
	files: testFiles,
	tests,
});
write(path.join(root, "parity/public-manifest.json"), inventory);
console.log(JSON.stringify(summary, null, 2));
