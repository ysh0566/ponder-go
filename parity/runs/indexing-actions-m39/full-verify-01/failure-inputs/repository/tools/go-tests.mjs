import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import crypto from "node:crypto";
import { sourceFingerprint } from "./go-source-fingerprint.mjs";

const root = path.resolve(import.meta.dirname, "..");
const directory = path.resolve(root, process.env.PONDER_RUN_DIR ?? "parity/runs");
const race = process.argv.includes("--race");
const selections = process.argv.filter(x => x.startsWith("--package=")).map(x => x.slice(10));
const name = race ? "go-race" : "go-test";
fs.mkdirSync(directory, { recursive: true });
const output = fs.openSync(path.join(directory, `${name}.jsonl`), "w");
const reportPath = path.join(directory, `${name}-execution.json`);
const report = {
	started_utc: new Date().toISOString(),
	race,
	scope: selections.length ? "explicit_package_subset" : "all_module_packages",
	policy: "Run every listed Test, Fuzz seed and Example once. Partition top-level functions into disjoint groups of four, sequentially. Go's original default test timeout and all test bodies are unchanged. This limits simultaneous PGlite memory and avoids the measured ten-minute whole-store timeout.",
	packages: [],
	complete: false,
	source_fingerprint: sourceFingerprint(root),
	driver_sha256: crypto.createHash("sha256").update(fs.readFileSync(import.meta.filename)).digest("hex"),
};
const save = () => fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
function discover(args) {
	const result = spawnSync("go", args, { cwd: root, encoding: "utf8", timeout: 300000, maxBuffer: 8 * 1024 * 1024 });
	if (result.error || result.status !== 0) throw new Error(`go ${args.join(" ")}: ${result.error?.message ?? result.stderr}`);
	return result.stdout.trim().split(/\r?\n/).filter(Boolean);
}
try {
	const packages = discover(["list", ...(selections.length ? selections : ["./..."])]);
	let failed = false;
	for (const pkg of packages) {
		const flags = race ? ["-race"] : [];
		const names = discover(["test", ...flags, "-list", ".", pkg]).filter(x => /^(Test|Fuzz|Example)[\p{L}\p{N}_]*$/u.test(x));
		const entry = { package: pkg, listed_functions: names, batches: [] };
		report.packages.push(entry);
		const groups = [];
		for (let i = 0; i < names.length; i += 4) groups.push(names.slice(i, i + 4));
		if (!groups.length) groups.push([]);
		for (const selected of groups) {
			const args = ["test", "-count=1", ...flags, "-json"];
			if (selected.length) args.push("-run", `^(${selected.join("|")})$`);
			args.push(pkg);
			const batch = { functions: selected, command: ["go", ...args], started_utc: new Date().toISOString(), complete: false };
			entry.batches.push(batch);
			save();
			process.stderr.write(`${name}: ${pkg} ${selected.join(",") || "no test functions"}\n`);
			const started = performance.now();
			const result = spawnSync("go", args, { cwd: root, encoding: "utf8", stdio: ["ignore", output, "pipe"], maxBuffer: 8 * 1024 * 1024 });
			Object.assign(batch, { complete: true, elapsed_ms: Math.round(performance.now() - started), exit_code: result.status, signal: result.signal, error: result.error?.message ?? null, stderr: result.stderr ?? "" });
			if (batch.stderr) process.stderr.write(batch.stderr);
			if (result.error || result.status !== 0) failed = true;
			save();
		}
		const actual = entry.batches.flatMap(x => x.functions);
		if (new Set(actual).size !== names.length || actual.length !== names.length || names.some(x => !actual.includes(x))) throw new Error(`Test partition is incomplete or duplicated: ${pkg}`);
	}
	report.finished_source_fingerprint = sourceFingerprint(root);
	if (report.finished_source_fingerprint.sha256 !== report.source_fingerprint.sha256) throw Error("Native sources changed during the execution; do not accept a mixed build");
	report.complete = true;
	report.finished_utc = new Date().toISOString();
	report.success = !failed;
	process.exitCode = failed ? 1 : 0;
} catch (error) {
	report.error = error.message;
	process.exitCode = 1;
} finally {
	fs.closeSync(output);
	save();
}
