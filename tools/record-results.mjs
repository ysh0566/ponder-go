import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { sourceFingerprint } from "./go-source-fingerprint.mjs";
import { backendLabelMatches } from "./backend-labels.mjs";
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
const executionPaths = ["parity/runs/go-test-execution.json", "parity/runs/go-race-execution.json"];
const executions = executionPaths.map(file => exists(file) ? read(file) : null);
if (executions.some(Boolean)) {
	if (executions.some(x => !x?.complete || !x?.success || x.scope !== "all_module_packages")) throw Error("Both complete module-wide Go executions are required; old or partial reports cannot substitute");
	const currentFingerprint = sourceFingerprint(root).sha256;
	if (executions.some(x => x.source_fingerprint?.sha256 !== currentFingerprint || x.finished_source_fingerprint?.sha256 !== currentFingerprint)) throw Error("Go and race evidence must describe the current, unchanged native sources");
	const driverHash = crypto.createHash("sha256").update(fs.readFileSync(path.join(root,"tools/go-tests.mjs"))).digest("hex");
	if (executions.some(x => x.driver_sha256 !== driverHash)) throw Error("Go execution driver changed after the recorded runs");
	for (const [index, results] of [go, race].entries()) {
		for (const pkg of executions[index].packages)
			for (const name of pkg.listed_functions)
				if (!results.has(`${pkg.package}:${name}`)) throw Error(`Listed Go function has no execution result: ${pkg.package}:${name}`);
		const start = Date.parse(executions[index].started_utc), end = Date.parse(executions[index].finished_utc);
		if ([...results.values()].some(x => Date.parse(x.Time) < start - 1000 || Date.parse(x.Time) > end + 1000)) throw Error("Go results do not belong to the recorded execution: " + executionPaths[index]);
	}
}
const typedGo = goResults("parity/runs/abi-typed-go.jsonl");
const typedRace = goResults("parity/runs/abi-typed-race.jsonl");
const compile = exists("parity/runs/compile-fixtures.json") ? read("parity/runs/compile-fixtures.json") : null;
const ormCompile = exists("parity/runs/orm-compile-fixtures.json") ? read("parity/runs/orm-compile-fixtures.json") : null;
const eventCompile = exists("parity/runs/event-compile-fixtures.json") ? read("parity/runs/event-compile-fixtures.json") : null;
const bloomCompile = exists("parity/runs/bloom-compile-fixtures.json") ? read("parity/runs/bloom-compile-fixtures.json") : null;
const configCompile = exists("parity/runs/config-live-verification.json") ? read("parity/runs/config-live-verification.json") : null;
const parameterTypes = exists("parity/runs/parameters-typecheck.json") ? read("parity/runs/parameters-typecheck.json") : null;
const syncVerification = exists("parity/runs/source-sync-verification.json") ? read("parity/runs/source-sync-verification.json") : null;
const fileHash = file => crypto.createHash("sha256").update(fs.readFileSync(path.join(root,file))).digest("hex");

if(ledger.tests.some(t=>t.mapping&&t.source_file==="packages/core/src/sync-store/index.test.ts")&&!syncVerification)throw Error("SyncStore mappings require complete actual source/native/type proof");
if(syncVerification){
 const refs=[syncVerification.pristine,syncVerification.source_types,syncVerification.compile,...syncVerification.inputs,...syncVerification.executions.flatMap(e=>[e.log,e.native,e.capture,e.original,e.result,...e.source_context])];
 if(!syncVerification.complete||syncVerification.scope!=="complete_module_source_sync"||syncVerification.executions.length!==4||syncVerification.summary.source_cases!==38||syncVerification.summary.source_assertions!==63||syncVerification.directives.length!==9||refs.some(f=>!exists(f.file)||fileHash(f.file)!==f.sha256))throw Error("Incomplete or stale SyncStore source/native evidence");
}

if (configCompile) {
 if(configCompile.executions.length!==2||configCompile.executions.some(x=>!x.complete||![x.native,x.capture,x.original,x.compile,...x.source_context].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256))||!configCompile.inputs.every(f=>exists(f.file)&&fileHash(f.file)===f.sha256))throw Error("Incomplete or stale live config/native proof");
}

const preparedNative=exists("parity/runs/prepared-native-inputs.json")?read("parity/runs/prepared-native-inputs.json"):null;
if(executions.every(Boolean)) {
 if(!preparedNative||executions.some(x=>x.source_fingerprint.sha256!==preparedNative.fingerprint.sha256)||Date.parse(preparedNative.recorded_utc)>Date.parse(executions[0].started_utc)||!preparedNative.files.every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)||fileHash(preparedNative.tool.file)!==preparedNative.tool.sha256)throw Error("Prepared native input snapshot must precede execution and remain unchanged");
}
const configOriginal=exists("parity/runs/config-oracle.json")?read("parity/runs/config-oracle.json"):null;
if(configOriginal&&(!configOriginal.complete||!configOriginal.success||![configOriginal.raw_oracle,...configOriginal.source_context].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)))throw Error("Pristine config oracle context changed");
for (const fixtureFile of ["testdata/runtime/captured-original-events.json", "testdata/runtime/captured-original-filters.json", "testdata/bloom/captured-original-blooms.json"]) {
	if (!exists(fixtureFile)) continue;
	const fixture = read(fixtureFile), proof = fixture.capture_proof;
	if (fixture.baseline?.revision !== ledger.baseline.revision || proof?.scope !== "instrumented_original_file_with_call_through" || !proof.source_context.every(x => exists(x.file) && fileHash(x.file) === x.sha256) || !exists(proof.raw_oracle.file) || fileHash(proof.raw_oracle.file) !== proof.raw_oracle.sha256 || !read(proof.raw_oracle.file).success) throw Error("Original capture context changed or is incomplete: " + fixtureFile);
	for (const captured of fixture.cases) {
		const source = ledger.tests.find(x => x.id === captured.id);
		if (!source || source.source_sha256 !== captured.source_sha256 || source.test_source !== captured.test_source || !captured.calls.length) throw Error("Stale or incomplete original captured declaration: " + captured.id);
	}
}
const sqlVerification=exists("parity/runs/sql-source-verification.json")?read("parity/runs/sql-source-verification.json"):null;
if(ledger.tests.some(t=>t.mapping&&t.source_file==="packages/core/src/utils/sql-parse.test.ts")&&!sqlVerification)throw Error("SQL source mappings require original/input/rules proof");
if(sqlVerification){
 const refs=[...sqlVerification.inputs,sqlVerification.rules,sqlVerification.pristine.report,sqlVerification.pristine.raw_oracle,...sqlVerification.pristine.source_context,sqlVerification.capture.fixture,sqlVerification.capture.raw_oracle,...sqlVerification.capture.source_context,sqlVerification.vectors.fixture,...sqlVerification.vectors.sources];
 if(sqlVerification.summary.source_cases!==14||sqlVerification.summary.source_assertions!==19||sqlVerification.summary.captured_operations!==38||refs.some(f=>!exists(f.file)||fileHash(f.file)!==f.sha256))throw Error("Incomplete/stale SQL original/native context");
}
const topicVerification=exists("parity/runs/topic-source-verification.json")?read("parity/runs/topic-source-verification.json"):null;
if(topicVerification){
 const refs=[topicVerification.fixture,...topicVerification.source_context,...topicVerification.inputs];
 if(topicVerification.summary.source_vectors!==50||topicVerification.summary.new_exact_source_records!==0||refs.some(f=>!exists(f.file)||fileHash(f.file)!==f.sha256))throw Error("Incomplete/stale indexed-topic source evidence");
}
const oraclePath = exists("parity/runs/foundation-oracle.json")
	? "parity/runs/foundation-oracle.json"
	: "parity/foundation-oracle-focused.json";
const oracle = read(oraclePath);
const coreOraclePath = exists("parity/runs/core-oracle.json")
	? "parity/runs/core-oracle.json"
	: null;
const fullOriginalPaths = ["parity/runs/full-original-postgres-oracle.json", "parity/runs/full-original-pglite-oracle.json"];
if (fullOriginalPaths.some(exists) && !fullOriginalPaths.every(exists)) throw Error("Full original acceptance requires separate PG and PGlite evidence");
const fullOriginalReports = fullOriginalPaths.filter(exists).map(path => {
	const report = read(path), acceptance = report.ponder_acceptance;
	if (!acceptance?.complete || acceptance.baseline.revision !== ledger.baseline.revision || !acceptance.source_context.every(f => exists(f.file) && fileHash(f.file) === f.sha256) || !acceptance.raw_reports.every(f => exists(f.file) && fileHash(f.file) === f.sha256)) throw Error("Original acceptance context changed or is incomplete: " + path);
	return { path, report, fullOriginal: true, backend: acceptance.backend };
});
const oracleReports = [
 ...(sqlVerification?[{path:sqlVerification.pristine.raw_oracle.file,report:read(sqlVerification.pristine.raw_oracle.file),completeOriginal:true}]:[]),
	{ path: oraclePath, report: oracle },
	...[
		"parity/runs/finality-oracle.json",
		"parity/runs/schema-oracle.json",
		"parity/runs/event-oracle.json",
		"parity/runs/parameters-oracle.json",
		"parity/runs/factory-offset-oracle.json",
		"parity/runs/indexing-store-oracle.json",
		"parity/runs/normalize-oracle.json",
		"parity/runs/rpc-actions-oracle.json",
		coreOraclePath,
	]
		.filter((x) => x && exists(x))
		.map((path) => ({ path, report: read(path) })),
	...fullOriginalReports,
 ...(exists("testdata/bloom/captured-original-blooms.json") ? (()=>{ const proof=read("testdata/bloom/captured-original-blooms.json").capture_proof;return [{path:proof.raw_oracle.file,report:read(proof.raw_oracle.file),instrumented:true}];})() : []),
];
function sdkResult(file, title) {
	if (!exists(file)) return;
	for (const f of read(file).testResults ?? [])
		for (const t of f.assertionResults ?? []) if (t.title === title) return t;
}
for (const test of ledger.tests) {
	const fullBackends = {};
	for (const selected of oracleReports) {
		if (selected.fullOriginal && !selected.report.ponder_acceptance.sources.some(x => x.file === test.source_file && x.sha256 === test.source_sha256)) continue;
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
			test.oracle = { status: original.status, evidence: selected.path, ...(selected.instrumented ? {mode:"instrumented_original_file_with_call_through",pristine_original_acceptance:false}: {}), ...(selected.completeOriginal?{mode:"complete_unmodified_original_file_with_original_setup",full_original:true}:{}) };
		if (selected.fullOriginal && original) fullBackends[selected.backend] = { status: original.status, evidence: selected.path };
	}
	if (Object.keys(fullBackends).length) {
		const results = Object.values(fullBackends), passed = results.length === 2 && results.every(x => x.status === "passed");
		test.oracle = { status: passed ? "passed" : results.some(x => x.status === "failed") ? "failed" : results.every(x => x.status === "skipped") ? "skipped" : "blocked", evidence: fullBackends.pglite?.evidence ?? fullBackends.postgres?.evidence, mode: "unmodified_original_files_with_original_setup", full_original: true, backends: fullBackends };
		if (passed && test.mapping?.oracle_blocker) {
			test.resolved_historical_oracle_blocker = test.mapping.oracle_blocker;
			test.blockers = test.blockers.filter(x => x !== test.mapping.oracle_blocker.reason);
		}
	}

 if (test.source_file === "packages/core/src/sync-store/index.test.ts" && syncVerification) {
  const pristine=read(syncVerification.pristine.file), backends={};
  for(const execution of pristine.executions){
   const original=read(execution.raw_oracle.file).testResults.flatMap(f=>f.assertionResults).find(t=>t.title===test.title);
   if(original)backends[execution.backend]={status:original.status,evidence:execution.raw_oracle.file};
  }
  const passed=Object.keys(backends).length===2&&Object.values(backends).every(x=>x.status==="passed");
  test.oracle={status:passed?"passed":"blocked",evidence:syncVerification.pristine.file,mode:"complete_unmodified_original_file_with_original_setup",full_original:true,backends};
  test.runtime_validation={status:"passed",evidence:"parity/runs/source-sync-verification.json",meaning:"Actual native values, ordered Maps/identities, SQL ranges and nine physical tables compared during complete original executions in both Go modes and both backends"};
 }
 if (test.source_file === "packages/core/src/build/config.test.ts" && configOriginal) {
  const title=test.title.includes("%s")?test.title.replace("%s",test.parameter?.$special??String(test.parameter)):test.title;
  const original=read(configOriginal.raw_oracle.file).testResults.flatMap(f=>f.assertionResults).find(t=>t.title===title);
  if(original)test.oracle={status:original.status,evidence:"parity/runs/config-oracle.json",mode:"complete_unmodified_original_file_with_original_setup",full_original:true};
  if (configCompile && test.source_state==="active") {
   test.runtime_validation={status:"passed",evidence:"parity/runs/config-live-verification.json",meaning:"Actual native runtime construction checked in both complete Go modes; source port/type completion remains separate"};
   if (!test.mapping && test.type_error_directives.length) test.remaining_native_type_obligations=test.type_error_directives.length;
  }
 }
	if (test.source_file.endsWith("/decodeAbiParameters.test.ts") && parameterTypes?.source_sha256 === test.source_sha256)
		test.oracle_types = {status: parameterTypes.exit_code === 0 ? "passed" : "failed", evidence:"parity/runs/parameters-typecheck.json"};
	if (test.source_file.endsWith("/decodeEventLog.test.ts") && parameterTypes?.event_source_sha256 === test.source_sha256)
		test.oracle_types = {status: parameterTypes.exit_code === 0 ? "passed" : "failed", evidence:"parity/runs/parameters-typecheck.json"};

 for(const mode of ["runtime","mapped"]) {
  const selectionFile=`parity/selected-${mode}-declarations.json`,reportFile=`parity/runs/selected-${mode}-oracle.json`;
  if(!exists(selectionFile)||!exists(reportFile))continue;
  const selection=read(selectionFile),report=read(reportFile),source=selection.cases.find(c=>c.id===test.id&&c.source_sha256===test.source_sha256&&c.test_source===test.test_source),generated=selection.generated_file??"testdata/oracle/selected-runtime.test.ts";
  if(!test.oracle?.full_original&&source&&report.success&&fileHash(generated)===selection.generated_sha256&&fs.readFileSync(path.join(root,generated),"utf8").includes(source.test_source)) {
   const result=report.testResults.flatMap(r=>r.assertionResults).find(r=>r.title===test.title);
   if(result)test.oracle={status:result.status,evidence:reportFile,mode:"selected_unchanged_source_declarations_actual_dependencies",source_selection:selectionFile,full_original_suite:{status:"blocked_before_collection",evidence:test.source_file.endsWith("/filter.test.ts")?"parity/runs/filter-oracle-blocked.json":"parity/runs/runtime-events-oracle-blocked.json",reason:"Missing generated.js; these selected original declarations have no generated ABI dependency."}};
  }
 }

 if(!test.oracle?.full_original&&!test.mapping&&test.source_file.endsWith("/runtime/filter.test.ts")&&["isTransactionFilterMatched()","isTransactionFilterMatched() with null transaction.to","isTransferFilterMatched()"].includes(test.title)&&exists("parity/runs/selected-with-eth-oracle.json")) {
  const report=read("parity/runs/selected-with-eth-oracle.json"),result=report.testResults.flatMap(r=>r.assertionResults).find(r=>r.title===test.title);
  if(result?.status==="failed"&&result.failureMessages.some(s=>s.includes("debug_traceBlockByHash")&&s.includes("MethodNotFound"))) {test.oracle={status:"blocked",actual_vitest_status:result.status,evidence:"parity/runs/selected-with-eth-oracle.json",reason:"Existing Anvil0.2.0 lacks debug_traceBlockByHash required by unchanged transferEth fixture; failure before assertions. Foundry1.8.5 installation remains pending.",capability_evidence:"parity/runs/anvil-trace-capabilities.json"};test.blockers.push(test.oracle.reason);}
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
			const directives=[...(compile?.directives??[]),...(ormCompile?.directives??[]),...(eventCompile?.directives??[]),...(bloomCompile?.directives??[]),...(configCompile?.directives??[]),...(syncVerification?.directives??[])].filter(x=>x.source_id===test.id&&x.source_sha256===test.source_sha256);
			const generatedValid=!ormCompile?.directives.some(x=>x.source_id===test.id)||((ormCompile.inputs?.length??0)>=3&&[...ormCompile.generated,...ormCompile.inputs].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256));
			const eventValid=!eventCompile?.directives.some(x=>x.source_id===test.id)||((eventCompile.inputs?.length??0)>=3&&eventCompile.inputs.every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&Number.isInteger(x.build_negative?.exit_code)&&x.build_negative.exit_code>0&&/callbacks\[0\]\.ABIItem undefined/.test(x.build_negative?.stderr??"")));
			const bloomValid=!bloomCompile?.directives.some(x=>x.source_id===test.id)||((bloomCompile.inputs?.length??0)>=5&&bloomCompile.inputs.every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&Number.isInteger(x.build_negative?.exit_code)&&x.build_negative.exit_code>0&&/cannot use filter\.Undefined.*as filter\.HexTopic/.test(x.build_negative?.stderr??"")));
			const configDiagnostic=x=>x.mode?.startsWith("network-")?/does not satisfy config\.SourceTypedContractShape/.test(x.build_negative.stderr):x.mode?.startsWith("bounds-")?/string does not satisfy config\.SourceBlockBound/.test(x.build_negative.stderr):x.mode==="literal-chain"?/bindings\.Chains\.Mainnetz undefined/.test(x.build_negative.stderr):x.mode==="literal-event"?/bindings\.Events\.Event2 undefined/.test(x.build_negative.stderr):/Filter\.Interval undefined.*config\.SourceFilter has no field or method Interval/.test(x.build_negative.stderr);
   const configValid=!configCompile?.directives.some(x=>x.source_id===test.id)||(configCompile.executions.length===2&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&x.build_negative?.exit_code>0&&configDiagnostic(x)));
			const syncValid=!syncVerification?.directives.some(x=>x.source_id===test.id)||(syncVerification.executions.length===4&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&x.execute_positive.executions.length===4&&x.execute_positive.executions.every(e=>e.run?.passed)&&x.build_negative?.exit_code>0&&/cannot use \[\]string.*as rpc\.SourceCallParameters value/.test(x.build_negative.stderr)));
			const valid=generatedValid&&eventValid&&bloomValid&&configValid&&syncValid&&directives.length===test.type_error_directives.length&&directives.every(x=>x.status==="passed"&&[x.positive,x.negative].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256));
			test.type_directives_verification={status:valid?"passed":"blocked",evidence:["parity/runs/compile-fixtures.json",...(ormCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/orm-compile-fixtures.json"]:[]),...(eventCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/event-compile-fixtures.json"]:[]),...(bloomCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/bloom-compile-fixtures.json"]:[]),...(configCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/config-live-verification.json"]:[]),...(syncVerification?.directives.some(x=>x.source_id===test.id)?["parity/runs/source-sync-verification.json"]:[])]};
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
	rpc_action_differential: exists("testdata/rpc/actions-differential.json") ? (() => {
		const fixture = read("testdata/rpc/actions-differential.json");
		if (fixture.sources.some(x => fileHash("../ponder-upstream/" + x.file) !== x.sha256)) throw Error("RPC differential source changed");
		const select = xs => xs.filter(x => x.Package === "ponder.local/ponder-go/rpc" && x.Test.startsWith("TestRPCActionDifferential/"));
		return {counting: fixture.counting, vectors: fixture.cases.length, source_expected_errors: fixture.cases.filter(x => x.error).length, go: count(select(native)), race: count(select(nativeRace)), evidence: "testdata/rpc/actions-differential.json"};
	})() : null,

 event_build_differential: exists("testdata/runtime/event-build-differential.json") ? (()=>{const fixture=read("testdata/runtime/event-build-differential.json"),select=xs=>xs.filter(x=>x.Package==="ponder.local/ponder-go/events"&&x.Test.startsWith("TestEventBuildDifferential/"));return {counting:fixture.counting,vectors:fixture.vectors.length,source_expected_errors:fixture.vectors.filter(v=>v.error).length,go:count(select(native)),race:count(select(nativeRace)),evidence:"testdata/runtime/event-build-differential.json"};})():null,
 selected_source_oracle: exists("parity/runs/selected-runtime-oracle.json")?{evidence:"parity/runs/selected-runtime-oracle.json",mode:"unchanged_selected_declarations_with_actual_source_dependencies",total:read("parity/runs/selected-runtime-oracle.json").numTotalTests,passed:read("parity/runs/selected-runtime-oracle.json").numPassedTests,success:read("parity/runs/selected-runtime-oracle.json").success}:null,
 selected_existing_oracle: exists("parity/runs/selected-mapped-oracle.json")?{evidence:"parity/runs/selected-mapped-oracle.json",mode:"unchanged_already_mapped_filter_genesis_split_declarations",total:read("parity/runs/selected-mapped-oracle.json").numTotalTests,passed:read("parity/runs/selected-mapped-oracle.json").numPassedTests,success:read("parity/runs/selected-mapped-oracle.json").success}:null,
	filter_fragment_differential: exists("testdata/runtime/filter-differential.json") ? (() => {
		const fixture=read("testdata/runtime/filter-differential.json"),selected=native.filter(x=>x.Package==="ponder.local/ponder-go/filter"&&x.Test.startsWith("TestFilterDifferential/")),selectedRace=nativeRace.filter(x=>x.Package==="ponder.local/ponder-go/filter"&&x.Test.startsWith("TestFilterDifferential/"));
		return {counting:fixture.counting,vectors:fixture.vectors.length,source_expected_exceptions:fixture.vectors.filter(x=>x.error).length,go:count(selected),race:count(selectedRace),evidence:"testdata/runtime/filter-differential.json"};
	})() : null,
	original_oracle: {mapped_passed:ledger.summary.oracle_passed,mapped_blocked:ledger.summary.oracle_blocked,meaning:"Original tests executed separately; only explicit differential vectors are labelled differential verification."},
	full_original_backend_oracles: fullOriginalReports.map(x => ({backend:x.backend,evidence:x.path,scope:"Eight original source files, not complete upstream acceptance or additional Go port credit",passed:x.report.numPassedTests,failed:x.report.numFailedTests,pending:x.report.numPendingTests,source_context_hashes_verified:true})),
	native_execution_schedule: executions.every(Boolean) ? Object.fromEntries(executions.map((x,i)=>[i===0?"go":"race",{evidence:executionPaths[i],complete:x.complete,success:x.success,policy:x.policy,packages:x.packages.length,top_level_functions:x.packages.reduce((n,p)=>n+p.listed_functions.length,0),batches:x.packages.reduce((n,p)=>n+p.batches.length,0)}])) : null,
	type_fixtures: compile?.summary ?? null,
	orm_type_fixtures: ormCompile?.summary ?? null,
	event_type_fixtures: eventCompile?.summary ?? null,
 bloom_type_fixtures: bloomCompile?.summary ?? null,
 config_builder: configCompile?.summary ?? null,
 bloom_differential: exists("testdata/bloom/differential.json") ? (()=>{const fixture=read("testdata/bloom/differential.json");if(fixture.sources.some(x=>fileHash("../ponder-upstream/"+x.file)!==x.sha256))throw Error("Bloom source changed");const select=xs=>xs.filter(x=>x.Package==="ponder.local/ponder-go/bloom"&&x.Test.startsWith("TestBloomDifferential/"));return{counting:fixture.counting,vectors:fixture.cases.length,source_expected_errors:fixture.cases.filter(x=>x.error).length,go:count(select(native)),race:count(select(nativeRace)),evidence:"testdata/bloom/differential.json"};})():null,
	type_scope: {
		dedicated_source_records: ledger.summary.type_records,
		dedicated_retained_sdk_passed: ledger.tests.filter(x=>x.kind==="type"&&x.mapping?.target_kind==="retained_sdk"&&x.pass_status==="passed").length,
		inline_assertions_discovered:ledger.summary.inline_type_assertions,
		inline_assertions_native_compiled:ledger.tests.filter(x=>x.type_verification?.status==="passed").reduce((n,x)=>n+x.type_verification.obligations,0),
		error_directives_discovered:ledger.summary.type_error_directives,
		error_directives_native_verified:ledger.tests.filter(x=>x.type_directives_verification?.status==="passed").reduce((n,x)=>n+x.type_error_directives.length,0),
		error_directives_retained_sdk_verified:ledger.tests.filter(x=>x.mapping?.target_kind==="retained_sdk"&&x.pass_status==="passed").reduce((n,x)=>n+x.type_error_directives.length,0),
	},
 indexed_topic_source_verification:topicVerification?{...topicVerification.summary,evidence:"parity/runs/topic-source-verification.json"}:null,
 source_sync_verification:syncVerification?{...syncVerification.summary,evidence:"parity/runs/source-sync-verification.json"}:null,
 sql_source_verification:sqlVerification?{...sqlVerification.summary,evidence:"parity/runs/sql-source-verification.json"}:null,
	typed_abi_runtime: {file:"parity/runs/abi-typed-go.jsonl",leaf_cases:leaves(typedGo).length,...count(leaves(typedGo)),counting:"Supplementary typed decoder execution; not additional source test records."},
	typed_abi_race: {file:"parity/runs/abi-typed-race.jsonl",leaf_cases:leaves(typedRace).length,...count(leaves(typedRace))},
	backend_execution: Object.fromEntries(["postgres","pglite"].map(backend=>{
		const selected=native.filter(x=>backendLabelMatches(x.Test,backend)),selectedRace=nativeRace.filter(x=>backendLabelMatches(x.Test,backend));
		return [backend,{counting:"First explicit slash dialect component wins; legacy backend-tagged names remain supported. Source titles mentioning another backend do not double count. Source mappings and backend companions remain separate from source parity.",go:{leaf_cases:selected.length,...count(selected)},race:{leaf_cases:selectedRace.length,...count(selectedRace)},evidence:goFile}];
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
