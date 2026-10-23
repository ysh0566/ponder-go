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
const typedGo = goResults("parity/runs/abi-typed-go.jsonl");
const typedRace = goResults("parity/runs/abi-typed-race.jsonl");
const compile = exists("parity/runs/compile-fixtures.json") ? read("parity/runs/compile-fixtures.json") : null;
const ormCompile = exists("parity/runs/orm-compile-fixtures.json") ? read("parity/runs/orm-compile-fixtures.json") : null;
const parameterTypes = exists("parity/runs/parameters-typecheck.json") ? read("parity/runs/parameters-typecheck.json") : null;
const fileHash = file => crypto.createHash("sha256").update(fs.readFileSync(path.join(root,file))).digest("hex");
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
		"parity/runs/parameters-oracle.json",
		"parity/runs/factory-offset-oracle.json",
		"parity/runs/indexing-store-oracle.json",
		"parity/runs/normalize-oracle.json",
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
		const originals = (selected.report.testResults ?? [])
			.filter((x) =>
				x.name.endsWith(test.source_file.replace("packages/core/", "")),
			)
			.flatMap((x) => x.assertionResults ?? [])
			.filter(
				(x) =>
					[...(x.ancestorTitles ?? []), x.title].join(" > ") === test.full_name,
			);
		// The source contains duplicate suite/title paths. Match their occurrence
		// order, never reuse the first test result for every matching record.
		const occurrence = ledger.tests.filter(x => x.source_file === test.source_file && x.full_name === test.full_name && x.start < test.start).length;
		const original = originals[occurrence];
		if (original)
			test.oracle = { status: original.status, evidence: selected.path };
	}
	if (test.source_file.endsWith("/decodeAbiParameters.test.ts") && parameterTypes?.source_sha256 === test.source_sha256)
		test.oracle_types = {status: parameterTypes.exit_code === 0 ? "passed" : "failed", evidence:"parity/runs/parameters-typecheck.json"};
	if (test.source_file.endsWith("/decodeEventLog.test.ts") && parameterTypes?.event_source_sha256 === test.source_sha256)
		test.oracle_types = {status: parameterTypes.exit_code === 0 ? "passed" : "failed", evidence:"parity/runs/parameters-typecheck.json"};
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
		if (test.mapping.required_compile_source_id) {
			const obligations = compile?.obligations.filter(x => x.source_id === test.id && x.source_sha256 === test.source_sha256) ?? [];
			const expected = test.assertions.filter(x => x.kind === "type").length;
			const typedKey = `ponder.local/ponder-go/abi/testdata/compile/generated:TestGeneratedTypedParameters/${test.id}`;
			const valid = obligations.length === expected && expected > 0 && obligations.every(x => x.status === "passed" && [x.positive,x.negative_control,x.generated].every(f => exists(f.file) && fileHash(f.file) === f.sha256)) && typedGo.get(typedKey)?.Action === "pass";
			test.type_verification = {status:valid?"passed":"blocked", obligations:expected, compile_evidence:"parity/runs/compile-fixtures.json", typed_runtime_evidence:"parity/runs/abi-typed-go.jsonl"};
			test.runtime_status = status;
			if (!valid) {status="skip";test.blockers.push("Native runtime alone is insufficient: inline type compilation/typed output evidence is incomplete.");}
		}
		if (test.type_error_directives.length) {
			const directives=[...(compile?.directives??[]),...(ormCompile?.directives??[])].filter(x=>x.source_id===test.id&&x.source_sha256===test.source_sha256);
			const generatedValid=!ormCompile?.directives.some(x=>x.source_id===test.id)||((ormCompile.inputs?.length??0)>=3&&[...ormCompile.generated,...ormCompile.inputs].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256));
			const valid=generatedValid&&directives.length===test.type_error_directives.length&&directives.every(x=>x.status==="passed"&&[x.positive,x.negative].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256));
			test.type_directives_verification={status:valid?"passed":"blocked",evidence:["parity/runs/compile-fixtures.json",...(ormCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/orm-compile-fixtures.json"]:[])]};
			if(!valid){status="skip";test.blockers.push("Source type-error directive requires a verified native compile-failure fixture.");}
		}
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
	test.parity_status = test.pass_status === "passed" ? test.oracle?.status === "passed" ? "target_and_original_oracle_passed" : test.oracle?.status === "blocked" ? "target_passed_original_oracle_blocked" : "target_passed_original_oracle_not_passed" : "mapped_target_not_passed";
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
	go_passed: ledger.tests.filter(x => x.mapping && x.mapping.target_kind !== "retained_sdk" && x.pass_status === "passed").length,
	sdk_passed: ledger.tests.filter(x => x.mapping?.target_kind === "retained_sdk" && x.pass_status === "passed").length,
	go_and_original_oracle_passed: ledger.tests.filter(x => x.mapping && x.mapping.target_kind !== "retained_sdk" && x.pass_status === "passed" && x.oracle?.status === "passed").length,
	target_and_original_oracle_passed: ledger.tests.filter(x => x.mapping && x.pass_status === "passed" && x.oracle?.status === "passed").length,
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
	source_records: {statically_discovered:ledger.tests.length,mapped:ledger.summary.ported,unimplemented:ledger.summary.unimplemented,discovery_is_not_proven_runtime_exhaustive:true},
	mapped_go: {mapped:ledger.summary.go_ports,passed_with_required_type_checks:ledger.summary.go_passed,original_oracle_also_passed:ledger.summary.go_and_original_oracle_passed,original_oracle_blocked:ledger.tests.filter(x=>x.mapping&&x.mapping.target_kind!=="retained_sdk"&&x.oracle?.status==="blocked").length},
	retained_sdk: {mapped:ledger.summary.retained_sdk,passed:ledger.summary.sdk_passed},
	filter_fragment_differential: exists("testdata/runtime/filter-differential.json") ? (() => {
		const fixture=read("testdata/runtime/filter-differential.json"),selected=native.filter(x=>x.Package==="ponder.local/ponder-go/filter"&&x.Test.startsWith("TestFilterDifferential/")),selectedRace=nativeRace.filter(x=>x.Package==="ponder.local/ponder-go/filter"&&x.Test.startsWith("TestFilterDifferential/"));
		return {counting:fixture.counting,vectors:fixture.vectors.length,source_expected_exceptions:fixture.vectors.filter(x=>x.error).length,go:count(selected),race:count(selectedRace),evidence:"testdata/runtime/filter-differential.json"};
	})() : null,
	original_oracle: {mapped_passed:ledger.summary.oracle_passed,mapped_blocked:ledger.summary.oracle_blocked,meaning:"Original tests executed separately; only explicit differential vectors are labelled differential verification."},
	type_fixtures: compile?.summary ?? null,
	orm_type_fixtures: ormCompile?.summary ?? null,
	type_scope: {
		dedicated_source_records: ledger.summary.type_records,
		dedicated_retained_sdk_passed: ledger.tests.filter(x=>x.kind==="type"&&x.mapping?.target_kind==="retained_sdk"&&x.pass_status==="passed").length,
		inline_assertions_discovered:ledger.summary.inline_type_assertions,
		inline_assertions_native_compiled:ledger.tests.filter(x=>x.type_verification?.status==="passed").reduce((n,x)=>n+x.type_verification.obligations,0),
		error_directives_discovered:ledger.summary.type_error_directives,
		error_directives_native_verified:ledger.tests.filter(x=>x.type_directives_verification?.status==="passed").reduce((n,x)=>n+x.type_error_directives.length,0),
		error_directives_retained_sdk_verified:ledger.tests.filter(x=>x.mapping?.target_kind==="retained_sdk"&&x.pass_status==="passed").reduce((n,x)=>n+x.type_error_directives.length,0),
	},
	typed_abi_runtime: {file:"parity/runs/abi-typed-go.jsonl",leaf_cases:leaves(typedGo).length,...count(leaves(typedGo)),counting:"Supplementary typed decoder execution; not additional source test records."},
	typed_abi_race: {file:"parity/runs/abi-typed-race.jsonl",leaf_cases:leaves(typedRace).length,...count(leaves(typedRace))},
	backend_execution: Object.fromEntries(["postgres","pglite"].map(backend=>{
		const selected=native.filter(x=>x.Test.toLowerCase().includes(backend)),selectedRace=nativeRace.filter(x=>x.Test.toLowerCase().includes(backend));
		return [backend,{counting:"Backend-explicit native leaf test names. Source mappings and backend companions are not added together for source parity.",go:{leaf_cases:selected.length,...count(selected)},race:{leaf_cases:selectedRace.length,...count(selectedRace)},evidence:goFile}];
	})),
	simulation: {seed_space:"unbounded",status:"unimplemented",executed_seeds:0},
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
