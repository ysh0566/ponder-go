// Check the diagnostics in the unchanged handler and row-prefetch declarations.
import fs from "node:fs";
import path from "node:path";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
const root = path.resolve(import.meta.dirname, ".."),
	peer = path.resolve(
		process.env.PONDER_UPSTREAM ?? path.join(root, "../ponder-upstream"),
	);
const core = path.join(peer, "packages/core"),
	req = createRequire(path.join(core, "package.json")),
	ts = req("typescript");
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
const files = [
	"src/indexing/index.test.ts",
	"src/indexing-store/cache.test.ts",
].map((f) => path.join(core, f));
const opts = {
	...ts.parseJsonConfigFileContent(
		ts.readConfigFile(path.join(core, "tsconfig.json"), ts.sys.readFile).config,
		ts.sys,
		core,
	).options,
	noEmit: true,
	skipLibCheck: true,
};
function check(remove) {
	const host = ts.createCompilerHost(opts),
		read = host.readFile;
	host.readFile = (f) => {
		const s = read(f);
		return files.includes(f) && remove
			? s.replace(/\/\/ @ts-expect-error[^\n]*/g, (m) => " ".repeat(m.length))
			: s;
	};
	const program = ts.createProgram(files, opts, host);
	return files.flatMap((f) => {
		const tree = program.getSourceFile(f);
		return [
			...program.getSemanticDiagnostics(tree),
			...program.getSyntacticDiagnostics(tree),
		].map((d) => ({
			file: path.relative(peer, f),
			line: tree.getLineAndCharacterOfPosition(d.start).line + 1,
			code: d.code,
			message: ts.flattenDiagnosticMessageText(d.messageText, " "),
		}));
	});
}
assert.deepEqual(check(false), []);
const ds = check(true),
	properties = [
		"mockRejectedValue",
		"mockRejectedValue",
		"transaction",
		"args",
	];
assert.deepEqual(
	ds.map((x) => x.line),
	[722, 1052, 1265, 504],
);
const fixture = { files: {}, cases: [] };
for (const [i, d] of ds.entries()) {
	assert.equal(d.code, 2339);
	assert(
		d.message.startsWith(
			"Property '" + properties[i] + "' does not exist on type ",
		),
	);
	const text = fs.readFileSync(path.join(peer, d.file), "utf8"),
		hash = createHash("sha256").update(text).digest("hex");
	const t = ledger.tests.find(
		(t) =>
			t.source_file === d.file &&
			t.line <= d.line &&
			t.line + t.test_source.split("\n").length > d.line,
	);
	assert(t && t.source_sha256 === hash && t.type_error_directives.length === 1);
	fixture.files[d.file] = hash;
	fixture.cases.push({
		id: t.id,
		file: d.file,
		line: t.line,
		name: t.title,
		diagnostic_line: d.line,
		code: d.code,
		property: properties[i],
	});
}
const data = JSON.stringify(fixture, null, 2) + "\n",
	dest = path.join(root, "testdata/indexing/handler-declarations.json");
if (process.argv.includes("--write")) fs.writeFileSync(dest, data);
else assert.equal(fs.readFileSync(dest, "utf8"), data);
console.log(
	JSON.stringify({
		typescript: ts.version,
		cases: 4,
		files: fixture.files,
		fixture_sha256: createHash("sha256").update(data).digest("hex"),
		removed_diagnostics: ds.map(({ file, line, code }, i) => ({
			file: path.basename(file),
			line,
			code,
			property: properties[i],
		})),
	}),
);
