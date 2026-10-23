import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import crypto from "node:crypto";
import { createRequire } from "node:module";
const root = path.resolve(import.meta.dirname, ".."),
	upstream = path.resolve(root, "../ponder-upstream");
const require = createRequire(
	path.join(upstream, "packages/core/package.json"),
);
const ts = require("typescript"),
	{ toEventSelector } = require("viem");
const file = "packages/core/src/utils/decodeEventLog.test.ts",
	source = fs.readFileSync(path.join(upstream, file), "utf8"),
	sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
const ledger = JSON.parse(
	fs.readFileSync(path.join(root, "parity/test-ledger.json"), "utf8"),
);
const hash = crypto.createHash("sha256").update(source).digest("hex");
const sourceCases = ledger.tests.filter((x) => x.source_file === file);
if (sourceCases.some((x) => x.source_sha256 !== hash))
	throw Error("Stale test ledger");
const printer = ts.createPrinter();
const code = sf.statements
	.filter((x) => !ts.isImportDeclaration(x))
	.map((x) => printer.printNode(ts.EmitHint.Unspecified, x, sf))
	.join("\n");
const tagged = (value) =>
	JSON.parse(
		JSON.stringify(value, (_, v) =>
			typeof v === "bigint" ? { $bigint: v.toString() } : v,
		),
	);
const output = [],
	suites = [];
let current;
function record(actual, matcher, expected) {
	const invocation = typeof actual === "function" ? actual() : actual;
	if (!invocation?.input) throw Error("Unexpected test expression");
	const assertion = { input: tagged(invocation.input), matcher };
	if (matcher === "toThrow" || matcher === "toThrowError") {
		if (Object.prototype.toString.call(expected) === "[object RegExp]")
			assertion.error = { regex: expected.source, flags: expected.flags };
		else assertion.error = { contains: expected ?? null };
	} else if (matcher === "toMatchInlineSnapshot") {
		assertion.raw_snapshot = expected;
		assertion.expected = tagged(vm.runInNewContext("(" + expected + ")", {}));
	} else assertion.expected = tagged(expected);
	current.assertions.push(assertion);
}
const context = {
	toEventSelector,
	decodeEventLog: (input) => ({ input }),
	describe: (name, fn) => {
		suites.push(name);
		try {
			fn();
		} finally {
			suites.pop();
		}
	},
	test: (name, fn) => {
		const full = [...suites, name].join(" > ");
		const matching = sourceCases.filter((x) => x.full_name === full);
		if (matching.length !== 1) throw Error("Ambiguous source case " + full);
		const original = matching[0];
		current = {
			id: original.id,
			source_file: file,
			source_sha256: hash,
			title: name,
			full_name: full,
			assertions: [],
		};
		const returned = fn();
		if (returned?.then) throw Error("Async fixture extraction unsupported");
		if (current.assertions.length !== original.assertions.length)
			throw Error("Assertion count changed for " + full);
		output.push(current);
		current = null;
	},
	expect: (actual) =>
		Object.fromEntries(
			["toEqual", "toMatchInlineSnapshot", "toThrowError", "toThrow"].map(
				(matcher) => [matcher, (expected) => record(actual, matcher, expected)],
			),
		),
};
vm.runInNewContext(
	ts.transpileModule(code, {
		compilerOptions: {
			target: ts.ScriptTarget.ES2022,
			module: ts.ModuleKind.None,
		},
	}).outputText,
	context,
	{ filename: file, timeout: 5000 },
);
if (output.length !== sourceCases.length) throw Error("Case count changed");
fs.mkdirSync(path.join(root, "testdata/abi"), { recursive: true });
fs.writeFileSync(
	path.join(root, "testdata/abi/event-cases.json"),
	JSON.stringify(output, null, 2) + "\n",
);
console.log({
	source_cases: output.length,
	assertions: output.reduce((n, x) => n + x.assertions.length, 0),
	method:
		"Source callbacks evaluated with symbolic ABI calls; no Go or oracle results used as expected values.",
});
