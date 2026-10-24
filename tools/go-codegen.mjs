import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";

// Compile cold dependencies separately from the bounded generator execution.
// Record both failures and timings, so compiler work cannot masquerade as a
// generator timeout or as a completed fixture check.
export function buildGenerator(root, packagePath, name, evidence) {
	const binary = path.join(root, ".parity-build", name);
	fs.mkdirSync(path.dirname(binary), { recursive: true });
	fs.mkdirSync(path.dirname(evidence), { recursive: true });
	const report = { format_version: 1, started_utc: new Date().toISOString(), stages: [] };
	function run(stage, executable, args, timeout) {
		const started = performance.now();
		const result = spawnSync(executable, args, { cwd: root, encoding: "utf8", timeout, maxBuffer: 8 * 1024 * 1024 });
		report.stages.push({ stage, command: [executable, ...args], timeout_ms: timeout, elapsed_ms: Math.round(performance.now() - started), exit_code: result.status, signal: result.signal, error: result.error?.message ?? null, stdout: result.stdout ?? "", stderr: result.stderr ?? "" });
		fs.writeFileSync(evidence, `${JSON.stringify(report, null, 2)}\n`);
		if (result.error) throw result.error;
		if (result.status !== 0) throw Error(`${stage}: ${result.stdout}${result.stderr}`);
	}
	run("build", "go", ["build", "-o", binary, packagePath], 300000);
	return (stage, args) => run(stage, binary, args, 45000);
}
