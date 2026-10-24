import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { spawnSync } from "node:child_process";

const root = path.resolve(import.meta.dirname, "..");
const upstream = path.resolve(root, "../ponder-upstream");
const argument = name => process.argv.find(x => x.startsWith(`--${name}=`))?.slice(name.length + 3);
const backend = argument("backend");
if (!["postgres", "pglite"].includes(backend)) throw Error("An explicit --backend=postgres or --backend=pglite is required");
const destination = argument("output");
if (!destination) throw Error("Choose a new evidence directory with --output=...");
const output = path.resolve(root, destination);
if (fs.existsSync(output)) throw Error("Preserve existing evidence; choose a new output directory");
for (const directory of [root, path.join(upstream, "packages/core")]) {
	if (fs.existsSync(path.join(directory, ".env.local"))) throw Error(`Unexpected .env.local: ${directory}; inspect before running an oracle`);
}
const files = [
	"database/actions.test.ts", "database/index.test.ts",
	"indexing-store/index.test.ts", "indexing-store/cache.test.ts", "indexing-store/profile.test.ts",
	"runtime/events.test.ts", "runtime/filter.test.ts", "rpc/actions.test.ts",
];
fs.mkdirSync(output, { recursive: true });
const report = { backend, complete: false, started_utc: new Date().toISOString(), method: "Each unmodified original file runs with its original globalSetup and helpers in a fresh process. maxWorkers1 and loopback-only transport adapter; no replaced helper, trace fallback or assertion changes. This is an eight-file subset, not complete upstream acceptance.", files: [] };
const save = () => fs.writeFileSync(path.join(output, "result.json"), `${JSON.stringify(report, null, 2)}\n`);
const environment = { ...process.env, CI: "1", NODE_OPTIONS: `${process.env.NODE_OPTIONS ?? ""} --import=${path.join(root, "tools/loopback-only.mjs")}`.trim() };
if (backend === "pglite") delete environment.DATABASE_URL;
else {
	const url = new URL(environment.PONDER_TEST_POSTGRES ?? "");
	if (url.hostname !== "127.0.0.1" || url.port !== "53398" || url.pathname !== "/ponder_test") throw Error("This cloud acceptance runner requires the explicit task-only PostgreSQL cluster");
	environment.DATABASE_URL = environment.PONDER_TEST_POSTGRES;
}
try {
	for (const file of files) {
		const label = file.replaceAll("/", "-").replace(/\.test\.ts$/, "");
		const source = fs.readFileSync(path.join(upstream, "packages/core/src", file));
		const entry = { file: `packages/core/src/${file}`, source_sha256: crypto.createHash("sha256").update(source).digest("hex"), complete: false };
		report.files.push(entry);
		save();
		const fd = fs.openSync(path.join(output, `${label}.txt`), "w");
		const args = ["run", "--config", "tools/full-oracle.config.mjs", "--configLoader", "native", `src/${file}`, "--reporter=json", `--outputFile=${path.join(output, `${label}.json`)}`];
		const started = performance.now();
		let result;
		try {
			result = spawnSync(path.join(upstream, "packages/core/node_modules/.bin/vitest"), args, { cwd: root, env: { ...environment, PONDER_BIND_AUDIT: path.join(output, `${label}-bindings.jsonl`) }, stdio: ["ignore", fd, fd] });
		} finally { fs.closeSync(fd); }
		Object.assign(entry, { complete: true, exit_code: result.status, signal: result.signal, error: result.error?.message ?? null, elapsed_ms: Math.round(performance.now() - started), report: `${label}.json` });
		const json = path.join(output, `${label}.json`);
		if (fs.existsSync(json)) {
			const data = JSON.parse(fs.readFileSync(json));
			entry.counts = { passed: data.numPassedTests, failed: data.numFailedTests, pending: data.numPendingTests, total: data.numTotalTests };
		}
		save();
		process.stderr.write(`${backend} original ${file}: exit=${result.status}\n`);
	}
	report.complete = true;
	report.success = report.files.every(x => x.exit_code === 0 && x.counts?.failed === 0);
	report.finished_utc = new Date().toISOString();
	process.exitCode = report.success ? 0 : 1;
} catch (error) {
	report.error = error.message;
	process.exitCode = 1;
} finally { save(); }
