import fs from "node:fs";
import path from "node:path";
import { spawnSync, execFileSync } from "node:child_process";
import { isDeepStrictEqual } from "node:util";
import { createRequire } from "node:module";
const root = path.resolve(import.meta.dirname, ".."),
	upstream = path.resolve(root, "../ponder-upstream");
const env = {
	...process.env,
	GOMODCACHE: path.join(root, ".toolcache/gomod"),
	GOCACHE: path.join(root, ".toolcache/gobuild"),
};
fs.mkdirSync(path.join(root, "bin"), { recursive: true });
execFileSync("go", ["build", "-o", "bin/parity-probe", "./cmd/parity-probe"], {
	cwd: root,
	env,
});
let seed = 0x17c6d5ba;
const random = (n) => {
	seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
	return seed % n;
};
const inputs = [];
const makeIntervals = () => {
	let start = random(8);
	return Array.from({ length: random(8) }, () => {
		const x = [start, start + random(8)];
		start = x[1] + random(4) + 1;
		return x;
	});
};
for (let i = 0; i < 400; i++) {
	const cp = {
		blockTimestamp: String(random(1e9)),
		chainId: (
			BigInt(random(2 ** 31)) * 1000000n +
			BigInt(random(1e6))
		).toString(),
		blockNumber: String(random(1e9)),
		transactionIndex: String(random(999999)),
		eventType: String(random(10)),
		eventIndex: String(random(999999)),
	};
	inputs.push({ op: "encode", checkpoint: cp });
	const encoded = Object.entries(cp)
		.map(([k, v]) =>
			v.padStart(k === "blockTimestamp" ? 10 : k === "eventType" ? 1 : 16, "0"),
		)
		.join("");
	inputs.push({ op: "decode", encoded });
	const A = makeIntervals(),
		B = makeIntervals();
	inputs.push(
		{ op: "union", A: [...A].reverse() },
		{ op: "intersection", A, B },
		{ op: "difference", A, B },
		{ op: "chunks", Interval: [0, random(100)], Size: random(20) + 1 },
	);
}
const input = inputs.map((x) => JSON.stringify(x)).join("\n") + "\n";
const run = (file, args) => {
	const result = spawnSync(file, args, {
		cwd: root,
		env,
		input,
		encoding: "utf8",
		maxBuffer: 32 << 20,
	});
	if (result.status !== 0) throw Error(`${file}: ${result.stderr}`);
	return result.stdout
		.trim()
		.split("\n")
		.map((x) => JSON.parse(x));
};
const require = createRequire(
	path.join(upstream, "packages/core/package.json"),
);
const oracle = run(process.execPath, [
		"--import",
		require.resolve("tsx"),
		"bridge/oracle-probe.ts",
	]),
	go = run(path.join(root, "bin/parity-probe"), []);
const differences = [];
for (let i = 0; i < inputs.length; i++)
	if (!isDeepStrictEqual(go[i], oracle[i]))
		differences.push({
			index: i,
			input: inputs[i],
			go: go[i],
			oracle: oracle[i],
		});
const result = {
	baseline: "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334",
	seed: "0x17c6d5ba",
	counting: "Generated differential vectors, not transplanted upstream tests.",
	vectors: inputs.length,
	passed: inputs.length - differences.length,
	failed: differences.length,
	go: execFileSync("go", ["version"], { encoding: "utf8" }).trim(),
	node: process.version,
	differences,
};
fs.mkdirSync(path.join(root, "parity/runs"), { recursive: true });
fs.writeFileSync(
	path.join(root, "parity/runs/differential-inputs.jsonl"),
	input,
);
fs.writeFileSync(
	path.join(root, "parity/runs/differential.json"),
	JSON.stringify(result, null, 2) + "\n",
);
console.log({
	vectors: result.vectors,
	passed: result.passed,
	failed: result.failed,
});
if (differences.length) process.exitCode = 1;
