import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
const root = path.resolve(import.meta.dirname, "..");
const read = (file) =>
	JSON.parse(fs.readFileSync(path.join(root, file), "utf8"));
const exists = (file) => fs.existsSync(path.join(root, file));
const ledger = read("parity/test-ledger.json");
function goResults(file) {
	if (!exists(file)) return new Map();
	const results = new Map();
	for (const line of fs
		.readFileSync(path.join(root, file), "utf8")
		.trim()
		.split("\n")) {
		if (!line) continue;
		const x = JSON.parse(line);
		if (x.Test && ["pass", "fail", "skip"].includes(x.Action))
			results.set(`${x.Package}:${x.Test}`, x);
	}
	return results;
}
const goFile = exists("parity/runs/go-test.jsonl")
	? "parity/runs/go-test.jsonl"
	: "parity/foundation-go.jsonl";
const go = goResults(goFile);
const race = goResults("parity/runs/go-race.jsonl");
const oraclePath = exists("parity/runs/foundation-oracle.json")
	? "parity/runs/foundation-oracle.json"
	: "parity/foundation-oracle-focused.json";
const oracle = read(oraclePath);
const coreOraclePath = exists("parity/runs/core-oracle.json")
	? "parity/runs/core-oracle.json"
	: null;
const oracleReports = [
	{ path: oraclePath, report: oracle },
	...[
		"parity/runs/finality-oracle.json",
		"parity/runs/schema-oracle.json",
		"parity/runs/event-oracle.json",
		"parity/runs/indexing-store-oracle.json",
		coreOraclePath,
	]
		.filter((x) => x && exists(x))
		.map((path) => ({ path, report: read(path) })),
];
function sdkResult(file, title) {
	if (!exists(file)) return;
	for (const f of read(file).testResults ?? [])
		for (const t of f.assertionResults ?? []) if (t.title === title) return t;
}
for (const test of ledger.tests) {
	for (const selected of oracleReports) {
		const original = (selected.report.testResults ?? [])
			.filter((x) =>
				x.name.endsWith(test.source_file.replace("packages/core/", "")),
			)
			.flatMap((x) => x.assertionResults ?? [])
			.find(
				(x) =>
					[...(x.ancestorTitles ?? []), x.title].join(" > ") === test.full_name,
			);
		if (original)
			test.oracle = { status: original.status, evidence: selected.path };
	}
	if (!test.mapping) continue;
	if (!test.oracle && test.mapping.oracle_blocker)
		test.oracle = { ...test.mapping.oracle_blocker };
	let status, evidence;
	if (test.mapping.target_kind === "retained_sdk") {
		const actual = fs.readFileSync(path.join(root, test.mapping.target_file));
		const hash = crypto.createHash("sha256").update(actual).digest("hex");
		if (hash !== test.source_sha256)
			throw Error("Retained SDK test changed: " + test.mapping.target_file);
		evidence =
			test.mapping.owner === "@ponder/react"
				? "parity/react-original-typecheck-supported.json"
				: "parity/client-original.json";
		status = sdkResult(evidence, test.title)?.status;
		test.ownership = test.mapping.owner;
	} else {
		const key = `${test.mapping.go_package}:${test.mapping.go_test}`;
		status = go.get(key)?.Action;
		evidence = goFile;
		if (race.has(key))
			test.race = {
				status: race.get(key).Action,
				evidence: "parity/runs/go-race.jsonl",
			};
	}
	if (status) {
		if (test.mapping.target_kind === "retained_sdk")
			test.oracle = {
				status: ["pass", "passed"].includes(status) ? "passed" : status,
				evidence,
			};
		test.execution_status = status === "skip" ? "blocked" : "executed";
		test.pass_status = ["pass", "passed"].includes(status)
			? "passed"
			: status === "skip"
				? "not_run"
				: "failed";
		test.execution_evidence = evidence;
		if (status === "skip")
			test.blockers.push("Execution reported skipped; no parity credit.");
	}
}
ledger.summary = {
	...ledger.summary,
	ported: ledger.tests.filter((x) => x.mapping).length,
	go_ports: ledger.tests.filter(
		(x) => x.mapping && x.mapping.target_kind !== "retained_sdk",
	).length,
	retained_sdk: ledger.tests.filter(
		(x) => x.mapping?.target_kind === "retained_sdk",
	).length,
	executed: ledger.tests.filter((x) => x.execution_status === "executed")
		.length,
	passed: ledger.tests.filter((x) => x.pass_status === "passed").length,
	failed: ledger.tests.filter((x) => x.pass_status === "failed").length,
	blocked: ledger.tests.filter((x) => x.execution_status === "blocked").length,
	unimplemented: ledger.tests.filter((x) => !x.mapping).length,
	oracle_passed: ledger.tests.filter(
		(x) => x.mapping && x.oracle?.status === "passed",
	).length,
	oracle_blocked: ledger.tests.filter(
		(x) => x.mapping && x.oracle?.status === "blocked",
	).length,
};
fs.writeFileSync(
	path.join(root, "parity/test-ledger.json"),
	JSON.stringify(ledger, null, 2) + "\n",
);
function leaves(results) {
	const entries = [...results.entries()];
	return entries
		.filter(([key]) => !entries.some(([other]) => other.startsWith(key + "/")))
		.map(([, value]) => value);
}
const native = leaves(go),
	nativeRace = leaves(race);
const count = (xs) =>
	Object.fromEntries(
		["pass", "fail", "skip"].map((state) => [
			state,
			xs.filter((x) => x.Action === state).length,
		]),
	);
const summary = {
	baseline: ledger.baseline,
	counting:
		"Only exact source mappings count toward upstream parity. Native regression cases and generated differential vectors are separate evidence.",
	parity: ledger.summary,
	native_go: { file: goFile, leaf_cases: native.length, ...count(native) },
	race: {
		file: "parity/runs/go-race.jsonl",
		leaf_cases: nativeRace.length,
		...count(nativeRace),
	},
	foundation_oracle: {
		file: oraclePath,
		total: oracle.numTotalTests,
		passed: oracle.numPassedTests,
		failed: oracle.numFailedTests,
	},
	core_oracle: coreOraclePath
		? {
				file: coreOraclePath,
				total: read(coreOraclePath).numTotalTests,
				passed: read(coreOraclePath).numPassedTests,
				failed: read(coreOraclePath).numFailedTests,
			}
		: null,
	client: sdkResult("parity/client-original.json", "setDatabaseSchema table")
		? {
				file: "parity/client-original.json",
				...Object.fromEntries(
					["numTotalTests", "numPassedTests", "numFailedTests"].map((k) => [
						k,
						read("parity/client-original.json")[k],
					]),
				),
			}
		: null,
	react: exists("parity/react-original-typecheck-supported.json")
		? read("parity/react-original-typecheck-supported.json").success
		: false,
	differential: exists("parity/runs/differential.json")
		? read("parity/runs/differential.json")
		: null,
};
fs.mkdirSync(path.join(root, "parity/runs"), { recursive: true });
fs.writeFileSync(
	path.join(root, "parity/runs/summary.json"),
	JSON.stringify(summary, null, 2) + "\n",
);
console.log({
	parity: summary.parity,
	native_go: summary.native_go,
	race: summary.race,
});
