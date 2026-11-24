import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import crypto from "node:crypto";
import { createInterface } from "node:readline";
import { sourceFingerprint } from "./go-source-fingerprint.mjs";

const root = path.resolve(import.meta.dirname, "..");
const directory = path.resolve(root, process.env.PONDER_RUN_DIR ?? "parity/runs");
const race = process.argv.includes("--race");
const selections = process.argv.filter(x => x.startsWith("--package=")).map(x => x.slice(10));
const batchSize = Number(process.env.PONDER_GO_TEST_BATCH_SIZE ?? 4);
if (!Number.isSafeInteger(batchSize) || batchSize < 1) {
	throw new Error("PONDER_GO_TEST_BATCH_SIZE must be a positive integer");
}
const minutes = process.env.PONDER_GO_TEST_TIMEOUT_MINUTES;
// A Go duration is a signed 64-bit nanosecond count.
if (minutes !== undefined && (!/^[1-9][0-9]*$/.test(minutes) || Number(minutes) > 153722867)) {
	throw new Error("PONDER_GO_TEST_TIMEOUT_MINUTES must be positive whole minutes within Go's duration range");
}
const timeout = minutes === undefined ? null : minutes + "m";
const name = race ? "go-race" : "go-test";
fs.mkdirSync(directory, { recursive: true });
const outputFile = path.join(directory, `${name}.jsonl`);
const output = fs.openSync(outputFile, "w");
const reportPath = path.join(directory, `${name}-execution.json`);
const report = {
	started_utc: new Date().toISOString(),
	race,
	batch_size: batchSize,
	batch_timeout: timeout,
	scope: selections.length ? "explicit_package_subset" : "all_module_packages",
	policy: `Run every listed Test, Fuzz seed and Example once. Run disjoint batches of up to ${batchSize} top-level functions in sequence. Each batch uses ${timeout ?? "Go's default test timeout"}.`,
	packages: [],
	complete: false,
	source_fingerprint: sourceFingerprint(root),
	driver_sha256: crypto.createHash("sha256").update(fs.readFileSync(import.meta.filename)).digest("hex"),
};
const save = () => fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
function discover(args) {
	const result = spawnSync("go", args, { cwd: root, encoding: "utf8", timeout: 300000, maxBuffer: 8 * 1024 * 1024 });
	if (result.error || result.status !== 0) {
		const reason = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || `exit ${result.status}`;
		throw new Error(`go ${args.join(" ")}: ${reason}`);
	}
	return result.stdout.trim().split(/\r?\n/).filter(Boolean);
}
async function batchOutcomes(file, start, end, pkg, selected) {
	const states = new Map(selected.map(name => [name, "not_started"]));
	const running = new Set();
	const failedTests = [];
	let packageResult = null;
	if (end > start) {
		const stream = fs.createReadStream(file, { start, end: end - 1, encoding: "utf8" });
		const lines = createInterface({ input: stream, crlfDelay: Infinity });
		try {
			for await (const line of lines) {
				if (!line) continue;
				const { Package, Action, Test } = JSON.parse(line);
				if (Package !== pkg) continue;
				const terminal = ["pass", "fail", "skip"].includes(Action);
				if (!Test) {
					if (terminal) packageResult = Action;
					continue;
				}
				if (Action !== "run" && !terminal) continue;
				const top = Test.split("/", 1)[0];
				if (!states.has(top)) throw Error("Unlisted test in batch: " + Test);
				if (Action === "run") running.add(Test);
				else running.delete(Test);
				if (Test === top) states.set(top, terminal ? Action : "unfinished");
				if (Action === "fail") failedTests.push(Test);
			}
		} finally {
			lines.close();
			stream.destroy();
		}
	}
	const notStarted = selected.filter(name => states.get(name) === "not_started");
	const counts = {pass: 0, fail: 0, skip: 0};
	for (const state of states.values()) if (Object.hasOwn(counts, state)) counts[state]++;
	return {package_result: packageResult, listed_results: counts, not_started: notStarted,
		unfinished: [...running], failed_tests: failedTests};
}
try {
	const packages = discover(["list", ...(selections.length ? selections : ["./..."])]);
	if (!packages.length) throw Error("Go package selection matched no packages");
	let failed = false;
	for (const pkg of packages) {
		const flags = race ? ["-race"] : [];
		const entry = { package: pkg, listed_functions: [], batches: [] };
		report.packages.push(entry);
		let names;
		try {
			names = discover(["test", ...flags, "-list", ".", pkg]).filter(x => /^(Test|Fuzz|Example)[\p{L}\p{N}_]*$/u.test(x));
			entry.listed_functions = names;
		} catch (error) {
			entry.discovery_error = error.message;
			process.stderr.write(error.message + "\n");
			failed = true;
			save();
			continue;
		}
		const groups = [];
		for (let i = 0; i < names.length; i += batchSize) groups.push(names.slice(i, i + batchSize));
		if (!groups.length) groups.push([]);
		for (const selected of groups) {
			const args = ["test", "-count=1", ...flags, "-json"];
			if (timeout) args.push("-timeout=" + timeout);
			if (selected.length) args.push("-run", `^(${selected.join("|")})$`);
			args.push(pkg);
			const batch = { functions: selected, command: ["go", ...args], started_utc: new Date().toISOString(), complete: false };
			entry.batches.push(batch);
			save();
			process.stderr.write(`${name}: ${pkg} ${selected.join(",") || "no test functions"}\n`);
			const started = performance.now();
			const offset = fs.fstatSync(output).size;
			const result = spawnSync("go", args, { cwd: root, encoding: "utf8", stdio: ["ignore", output, "pipe"], maxBuffer: 8 * 1024 * 1024 });
			Object.assign(batch, { complete: true, elapsed_ms: Math.round(performance.now() - started), exit_code: result.status, signal: result.signal, error: result.error?.message ?? null, stderr: result.stderr ?? "" });
			try {
				batch.outcomes = await batchOutcomes(outputFile, offset, fs.fstatSync(output).size, pkg, selected);
				const { package_result, not_started, unfinished, failed_tests } = batch.outcomes;
				if (!["pass", "skip"].includes(package_result) || not_started.length || unfinished.length || failed_tests.length) failed = true;
			} catch (error) {
				batch.outcome_error = error.message;
				failed = true;
			}
			if (batch.stderr) process.stderr.write(batch.stderr);
			if (result.error || result.status !== 0) failed = true;
			save();
		}
		const actual = entry.batches.flatMap(x => x.functions);
		if (new Set(actual).size !== names.length || actual.length !== names.length || names.some(x => !actual.includes(x))) throw new Error(`Test partition is incomplete or duplicated: ${pkg}`);
	}
	report.finished_source_fingerprint = sourceFingerprint(root);
	if (report.finished_source_fingerprint.sha256 !== report.source_fingerprint.sha256) throw Error("Native sources changed during the execution; do not accept a mixed build");
	report.complete = report.packages.every(pkg => !pkg.discovery_error && pkg.batches.every(batch =>
		batch.outcomes?.package_result && !batch.outcomes.not_started.length && !batch.outcomes.unfinished.length));
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
