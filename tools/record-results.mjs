import {atomicMultichainProofReferencesValid} from './multichain-atomic-evidence.mjs';
import {independentProofReferencesValid} from './independent-storage-evidence.mjs';
import {indexingProfileSupplementalTests,indexingProfileStoreTests,indexingProfileStoreScenarios} from './indexing-profile-acceptance.mjs';
import {indexingProfileTargets} from './source-indexing-profile-evidence.mjs';
import {indexingActionsTargets} from './source-indexing-actions-evidence.mjs';
import {indexingServiceTargets,indexingServiceTypeGaps} from './source-indexing-service-evidence.mjs';
import {multichainTargets,multichainProbeNames} from './source-runtime-multichain-evidence.mjs';
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { sourceFingerprint } from "./go-source-fingerprint.mjs";
import { backendLabelMatches } from "./backend-labels.mjs";
import { validateContextEntry } from "./loopback-context.mjs";
import { historicalCaptureContext } from "./historical-capture-context.mjs";
const root = path.resolve(import.meta.dirname, "..");
const read = (file) =>
	JSON.parse(fs.readFileSync(path.resolve(root, file), "utf8"));
const exists = (file) => typeof file === "string" && fs.existsSync(path.resolve(root, file));
const fileHash = file => crypto.createHash("sha256").update(fs.readFileSync(path.resolve(root,file))).digest("hex");
const diskValid = entry => !!entry && exists(entry.file) && fileHash(entry.file) === entry.sha256;
const runArgument = process.argv.find(value => value.startsWith("--run-dir="))?.slice(10);
const runDirectory = runArgument === undefined ? null : path.resolve(root, runArgument);
const runDir = runDirectory ? path.relative(root, runDirectory) : null;
if (runDirectory && (!runDir.startsWith("parity/runs/") || runDir.includes(".."))) throw Error("Explicit run directory must be inside parity/runs");
const runFile = name => path.join(runDir, name);
const requireFile = file => { if (!exists(file)) throw Error("Missing current-run evidence; no historical fallback: " + file); return file; };
const fullRunFile = runDir ? requireFile(runFile("result.json")) : null;
const fullRun = fullRunFile ? read(fullRunFile) : null;
if (runDir && (!fullRun.complete || !fullRun.success || fullRun.environment_adapted !== true || fullRun.pristine_file !== false || fullRun.source_manifest_verified_after !== true)) throw Error("A completed successful environment-adapted full run is required");
const archiveCache = new Map();
function loadArchive(ref) {
	if (!diskValid(ref)) throw Error("Context archive must have an existing, verified hash reference");
	const key = path.resolve(root, ref.file) + ":" + ref.sha256;
	if (!archiveCache.has(key)) archiveCache.set(key, read(ref.file));
	return archiveCache.get(key);
}
const fullArchive = runDir ? loadArchive(fullRun.context_archive) : null;
if (runDir && path.resolve(root, fullRun.context_archive.file) !== path.join(runDirectory, "context-archive.json")) throw Error("Full run must anchor its exact context archive");
if (runDir && (!validateContextEntry({file:"tools/loopback-context.mjs",sha256:fileHash("tools/loopback-context.mjs")},fullArchive) || !fullRun.harness_inputs?.every(diskValid))) throw Error("Full-run context or verification harness changed");
if(["independent-storage-m41","multichain-atomic-m42"].some(stage=>runDir?.startsWith("parity/runs/"+stage+"/"))){
 const proof=read("parity/runs/independent-storage-verification.json");
 if(!proof.complete||!proof.success||proof.source_credit!==0||proof.scope!=="independent_storage_and_reusable_bindings"||proof.run_dir!==runDir||!diskValid(proof.full_verification)||proof.full_verification.sha256!==fileHash(fullRunFile)||!independentProofReferencesValid(proof,diskValid)||JSON.stringify(proof.postgres_fixture)!==JSON.stringify(fullRun.postgres_fixture)||proof.source.length!==2||proof.executions.length!==2)throw Error("Current complete zero-credit M41 lifecycle proof required");
}
if(runDir?.startsWith("parity/runs/multichain-atomic-m42/")){
 const proof=read("parity/runs/multichain-atomic-verification.json");
 if(!proof.complete||!proof.success||proof.run_dir!==runDir||!diskValid(proof.full_verification)||proof.full_verification.sha256!==fileHash(fullRunFile)||!atomicMultichainProofReferencesValid(proof,diskValid)||JSON.stringify(proof.postgres_fixture)!==JSON.stringify(fullRun.postgres_fixture))throw Error("Current complete zero-credit M42 atomic primitive proof required");
}
const ledger = read("parity/test-ledger.json");
function goResults(file, mixedLog = false) {
	if (!exists(file)) return new Map();
	const results = new Map();
	for (const line of fs
		.readFileSync(path.resolve(root, file), "utf8")
		.trim()
		.split("\n")) {
		if (!line || (mixedLog && !line.startsWith("{"))) continue;
		const x = JSON.parse(line);
		if (x.Test && ["pass", "fail", "skip"].includes(x.Action))
			results.set(`${x.Package}:${x.Test}`, x);
	}
	return results;
}
const goFile = runDir ? requireFile(runFile("native/go-test.jsonl")) : exists("parity/runs/go-test.jsonl")
	? "parity/runs/go-test.jsonl"
	: "parity/foundation-go.jsonl";
const raceFile = runDir ? requireFile(runFile("native/go-race.jsonl")) : "parity/runs/go-race.jsonl";
const go = goResults(goFile);
const race = goResults(raceFile);
const executionPaths = runDir ? ["native/go-test-execution.json", "native/go-race-execution.json"].map(name => requireFile(runFile(name))) : ["parity/runs/go-test-execution.json", "parity/runs/go-race-execution.json"];
const executions = executionPaths.map(file => exists(file) ? read(file) : null);
if (executions.some(Boolean)) {
	if (executions.some(x => !x?.complete || !x?.success || x.scope !== "all_module_packages")) throw Error("Both complete module-wide Go executions are required; old or partial reports cannot substitute");
	const currentFingerprint = sourceFingerprint(root).sha256;
	if (executions.some(x => x.source_fingerprint?.sha256 !== currentFingerprint || x.finished_source_fingerprint?.sha256 !== currentFingerprint)) throw Error("Go and race evidence must describe the current, unchanged native sources");
	const driverHash = crypto.createHash("sha256").update(fs.readFileSync(path.join(root,"tools/go-tests.mjs"))).digest("hex");
	if (executions.some(x => x.driver_sha256 !== driverHash)) throw Error("Go execution driver changed after the recorded runs");
	for (const [index, results] of [go, race].entries()) {
		if (executions[index].race !== (index === 1)) throw Error("Normal/race execution manifest is mislabeled");
		for (const pkg of executions[index].packages)
			for (const name of pkg.listed_functions)
				if (!results.has(`${pkg.package}:${name}`)) throw Error(`Listed Go function has no execution result: ${pkg.package}:${name}`);
		const start = Date.parse(executions[index].started_utc), end = Date.parse(executions[index].finished_utc);
		if (!Number.isFinite(start) || !Number.isFinite(end) || start > end || (runDir && (start < Date.parse(fullRun.started_utc) || end > Date.parse(fullRun.finished_utc)))) throw Error("Native execution is outside the selected full run");
		if ([...results.values()].some(x => !Number.isFinite(Date.parse(x.Time)) || Date.parse(x.Time) < start - 1000 || Date.parse(x.Time) > end + 1000)) throw Error("Go results do not belong to the recorded execution: " + executionPaths[index]);
	}
}
const typedGoFile = runDir ? requireFile(runFile("abi-generated-normal.log")) : "parity/runs/abi-typed-go.jsonl";
const typedRaceFile = runDir ? requireFile(runFile("abi-generated-race.log")) : "parity/runs/abi-typed-race.jsonl";
const typedGo = goResults(typedGoFile, !!runDir);
const typedRace = goResults(typedRaceFile, !!runDir);
const clientFile = runDir ? requireFile(runFile("sdk-client.json")) : "parity/client-original.json";
const reactFile = runDir ? requireFile(runFile("sdk-react.json")) : "parity/react-original-typecheck-supported.json";
if (runDir) for (const name of ["sync-type-controls", "historical-type-controls", "sdk-client-build", "prepared-native-inputs", "abi-generated-normal", "abi-generated-race", "go-normal", "go-race", "go-vet", "cli-build", "erc20-build", "core-oracle", "parameters-types", "sync-types", "historical-types", "runtime-index-types", "sdk-client-tests", "sdk-react-types"]) {
	const commands = fullRun.commands.filter(command => command.name === name);
	if (commands.length !== 1 || commands[0].exit_code !== 0 || !commands[0].finished_utc) throw Error("Current full-run command did not complete successfully: " + name);
}
const compile = exists("parity/runs/compile-fixtures.json") ? read("parity/runs/compile-fixtures.json") : null;
const ormCompile = exists("parity/runs/orm-compile-fixtures.json") ? read("parity/runs/orm-compile-fixtures.json") : null;
const eventCompile = exists("parity/runs/event-compile-fixtures.json") ? read("parity/runs/event-compile-fixtures.json") : null;
const bloomCompile = exists("parity/runs/bloom-compile-fixtures.json") ? read("parity/runs/bloom-compile-fixtures.json") : null;
const configCompile = exists("parity/runs/config-live-verification.json") ? read("parity/runs/config-live-verification.json") : null;
const parameterTypes = exists("parity/runs/parameters-typecheck.json") ? read("parity/runs/parameters-typecheck.json") : null;
const syncVerification = exists("parity/runs/source-sync-verification.json") ? read("parity/runs/source-sync-verification.json") : null;
const historicalVerification = exists("parity/runs/source-historical-verification.json") ? read("parity/runs/source-historical-verification.json") : null;
const realtimeVerification = exists("parity/runs/source-realtime-verification.json") ? read("parity/runs/source-realtime-verification.json") : null;
const runtimeIndexVerificationFile = "parity/runs/source-runtime-index-verification.json";
const runtimeIndexVerification = exists(runtimeIndexVerificationFile) ? read(runtimeIndexVerificationFile) : null;
const runtimeHistoricalVerificationFile = "parity/runs/source-runtime-historical-verification.json";
const runtimeHistoricalVerification = exists(runtimeHistoricalVerificationFile) ? read(runtimeHistoricalVerificationFile) : null;
const runtimeMultichainVerificationFile = "parity/runs/source-runtime-multichain-verification.json";
const runtimeMultichainVerification = exists(runtimeMultichainVerificationFile) ? read(runtimeMultichainVerificationFile) : null;
const indexingProfileVerificationFile="parity/runs/source-indexing-profile-verification.json";
const indexingProfileVerification=exists(indexingProfileVerificationFile)?read(indexingProfileVerificationFile):null;
const indexingActionsVerificationFile="parity/runs/source-indexing-actions-verification.json";
const indexingActionsVerification=exists(indexingActionsVerificationFile)?read(indexingActionsVerificationFile):null;
const indexingServiceVerificationFile="parity/runs/source-indexing-service-verification.json";
const indexingServiceVerification=exists(indexingServiceVerificationFile)?read(indexingServiceVerificationFile):null;
if(!runDir&&[syncVerification,historicalVerification,realtimeVerification,configCompile,runtimeIndexVerification,runtimeHistoricalVerification,runtimeMultichainVerification,indexingServiceVerification,indexingActionsVerification,indexingProfileVerification].some(proof=>proof?.environment_adapted))throw Error("Adapted canonical verification requires an explicit --run-dir; historical native logs cannot substitute");
function proofArchive(verification, execution) {
	return runDir ? loadArchive(execution?.context_archive ?? verification.context_archive ?? fullRun.context_archive) : null;
}
function proofValid(entry, verification, execution) {
	return runDir ? validateContextEntry(entry, proofArchive(verification, execution)) : diskValid(entry);
}
function validateCurrentProof(verification, name) {
	if (!runDir) return;
	if (!verification || verification.complete !== true || verification.run_dir !== runDir || verification.environment_adapted !== true || verification.pristine_file !== false) throw Error("Current adapted verification required: " + name);
	const anchors = [verification.full_verification, ...(verification.inputs ?? [])].filter(Boolean);
	if (!anchors.some(ref => path.resolve(root, ref.file) === path.resolve(root, fullRunFile) && diskValid(ref))) throw Error("Verification does not anchor this exact full run: " + name);
	for (const execution of verification.executions ?? []) {
		const mode = execution.native_mode ?? execution.mode;
		const log = mode === "normal" || mode === "go-test" ? goFile : mode === "race" || mode === "go-race" ? raceFile : null;
		if (!log || !diskValid(execution.log) || path.resolve(root, execution.log.file) !== path.resolve(root, log)) throw Error("Verification uses a different native execution: " + name);
		proofArchive(verification, execution);
	}
}
for (const [name, proof] of [["sync", syncVerification], ["historical", historicalVerification], ["realtime", realtimeVerification], ["config", configCompile]]) validateCurrentProof(proof, name);

if(ledger.tests.some(t=>t.mapping&&t.source_file==="packages/core/src/sync-store/index.test.ts")&&!syncVerification)throw Error("SyncStore mappings require complete actual source/native/type proof");
if(syncVerification){
 const refs=[...(runDir?[]:[syncVerification.pristine]),syncVerification.source_types,syncVerification.compile,...syncVerification.inputs];
 if(!syncVerification.complete||syncVerification.scope!=="complete_module_source_sync"||syncVerification.executions.length!==4||syncVerification.summary.source_cases!==38||syncVerification.summary.source_assertions!==63||syncVerification.directives.length!==9||refs.some(f=>!proofValid(f,syncVerification))||syncVerification.executions.some(e=>[e.log,e.native,e.capture,e.original,e.result,...e.source_context].some(f=>!proofValid(f,syncVerification,e))))throw Error("Incomplete or stale SyncStore source/native evidence");
}

if(ledger.tests.some(t=>t.mapping&&t.source_file.includes("/sync-historical/"))&&!historicalVerification)throw Error("Historical mappings require complete actual source/native/type proof");
if(historicalVerification){
 const refs=[...(runDir?[]:[historicalVerification.pristine]),historicalVerification.source_types,historicalVerification.compile,...historicalVerification.inputs];
 if(!historicalVerification.complete||historicalVerification.scope!=="complete_module_source_historical"||historicalVerification.executions.length!==8||historicalVerification.summary.source_cases!==40||historicalVerification.summary.source_assertions!==102||historicalVerification.summary.typed_positive_executions!==48||historicalVerification.directives.length!==12||refs.some(f=>!proofValid(f,historicalVerification))||historicalVerification.executions.some(e=>[e.log,e.native,e.capture,e.requests,e.original,e.result,...e.source_context].some(f=>!proofValid(f,historicalVerification,e))))throw Error("Incomplete or stale historical source/native evidence");
}

if(ledger.tests.some(t=>t.mapping&&t.source_file==="packages/core/src/sync-realtime/index.test.ts")&&!realtimeVerification)throw Error("Realtime mappings require complete actual source/native proof");
if(realtimeVerification){
 const refs=[...(runDir?[]:[realtimeVerification.pristine]),...realtimeVerification.inputs];
 if(!realtimeVerification.complete||realtimeVerification.scope!=="complete_module_source_realtime"||realtimeVerification.executions.length!==4||realtimeVerification.summary.source_cases!==30||realtimeVerification.summary.source_assertions!==203||realtimeVerification.summary.source_error_directives!==0||realtimeVerification.summary.native_calls_per_backend_mode!==83||realtimeVerification.summary.raw_provider_inputs_per_backend_mode!==46||refs.some(f=>!proofValid(f,realtimeVerification))||realtimeVerification.executions.some(e=>[e.log,e.native,e.capture,e.requests,e.original,e.result,...e.source_context].some(f=>!proofValid(f,realtimeVerification,e))))throw Error("Incomplete or stale realtime source/native evidence");
}

if (configCompile) {
 if(configCompile.executions.length!==2||configCompile.executions.some(x=>!x.complete||![x.native,x.capture,x.original,x.compile,...x.source_context].every(f=>proofValid(f,configCompile,x)))||!configCompile.inputs.every(f=>proofValid(f,configCompile)))throw Error("Incomplete or stale live config/native proof");
}

const runtimeHistoricalIDs = new Set(["1337ad403051f00c5d99","dd482eb7065250d54888","b259864fbc99fca7585d","6932328b1d777888e031","35500a04eb3407d46aa8","055e33151c56724c5459","eb47df94258c65be3a2e","1644fb814d5a0df1b23c","4a23b3323e9c3992e535","d685ca5ec1b8a49be6b3","42b8ee0fbd39ff105e51"]);
if (runDir && ledger.tests.some(test=>test.mapping&&test.source_file==="packages/core/src/runtime/historical.test.ts")&&!runtimeHistoricalVerification)throw Error("Runtime historical mappings require current complete evidence");
if(runtimeHistoricalVerification){
 const v=runtimeHistoricalVerification;validateCurrentProof(v,"runtime-historical");
 const selected=ledger.tests.filter(test=>test.source_file==="packages/core/src/runtime/historical.test.ts");
 if(!v.complete||v.success!==true||v.scope!=="complete_module_source_runtime_historical"||v.summary.source_cases!==14||v.summary.source_assertions!==29||v.summary.targeted_cases!==11||v.summary.targeted_assertions!==24||v.summary.source_error_directives!==0||v.executions.length!==4||v.inputs.some(ref=>!proofValid(ref,v)))throw Error("Incomplete runtime historical evidence");
 const combinations=new Set();
 for(const execution of v.executions){
  const combination=execution.mode+"/"+execution.backend;
  if(!["normal/postgres","normal/pglite","race/postgres","race/pglite"].includes(combination)||combinations.has(combination)||[execution.log,execution.native,execution.capture,execution.original,execution.result,execution.source_order_probe,execution.source_order_result,execution.source_order_original,execution.source_order_context,...execution.source_context].some(ref=>!proofValid(ref,v,execution)))throw Error("Runtime historical execution/context differs");
  combinations.add(combination);
  const results=execution.mode==="normal"?go:race,seen=new Set();
  if(execution.cases?.length!==11)throw Error("Runtime historical requires exactly eleven native cases");
  for(const record of execution.cases){
   const source=selected.find(test=>test.id===record.source_id),prefix="TestSourceRuntimeHistoricalOriginal/"+execution.backend;
   if(!source||!runtimeHistoricalIDs.has(source.id)||seen.has(source.id)||execution.native_test_prefix!==prefix||record.go_test!==prefix+"/"+source.title.replaceAll(/\s/g,"_")||record.native_type_obligations!==0||results.get(`ponder.local/ponder-go/runtime:${record.go_test}`)?.Action!=="pass")throw Error("Runtime historical native case missing or changed");
   seen.add(source.id);
  }
 }
}

const runtimeMultichainIDs = new Set(multichainTargets);
if (runDir && ledger.tests.some(test=>test.mapping&&runtimeMultichainIDs.has(test.id)&&test.source_file==="packages/core/src/runtime/historical.test.ts")&&!runtimeMultichainVerification)throw Error("Runtime multichain mappings require current complete evidence");
if(runtimeMultichainVerification){
 const v=runtimeMultichainVerification;validateCurrentProof(v,"runtime-multichain");
 const selected=ledger.tests.filter(test=>test.source_file==="packages/core/src/runtime/historical.test.ts");
 if(!v.complete||v.success!==true||v.scope!=="complete_module_source_runtime_multichain"||v.summary.source_cases!==14||v.summary.source_assertions!==29||v.summary.targeted_cases!==3||v.summary.supplemental_probe_cases!==multichainProbeNames.length||v.summary.supplementary_source_credit!==0||v.summary.targeted_assertions!==5||v.summary.source_error_directives!==0||v.executions.length!==4||v.inputs.some(ref=>!proofValid(ref,v)))throw Error("Incomplete runtime multichain evidence");
 const combinations=new Set();
 for(const execution of v.executions){
  const combination=execution.mode+"/"+execution.backend;
  if(!["normal/postgres","normal/pglite","race/postgres","race/pglite"].includes(combination)||combinations.has(combination)||[execution.log,execution.native,execution.capture,execution.original,execution.result,execution.supplemental_probe,execution.supplemental_result,execution.supplemental_original,execution.supplemental_context,...execution.supplemental_source_context,...execution.source_context].some(ref=>!proofValid(ref,v,execution)))throw Error("Runtime multichain execution/context differs");
  combinations.add(combination);
  const results=execution.mode==="normal"?go:race,seen=new Set();
  if(!Array.isArray(execution.supplemental_cases)||JSON.stringify(execution.supplemental_cases.map(c=>c.name))!==JSON.stringify(multichainProbeNames)||new Set(execution.supplemental_cases.map(c=>c.name)).size!==execution.supplemental_cases.length||execution.supplemental_cases.some(c=>c.source_credit!==0||c.go_test!=="TestSourceRuntimeMultichainProbes/"+execution.backend+"/"+c.name.replaceAll(/\s/g,"_")||results.get(`ponder.local/ponder-go/runtime:${c.go_test}`)?.Action!=="pass"))throw Error("Runtime multichain supplemental native coverage missing");
  if(execution.cases?.length!==3)throw Error("Runtime multichain requires exactly three native cases");
  for(const record of execution.cases){
   const source=selected.find(test=>test.id===record.source_id),prefix="TestSourceRuntimeMultichainOriginal/"+execution.backend;
   if(!source||!runtimeMultichainIDs.has(source.id)||seen.has(source.id)||execution.native_test_prefix!==prefix||record.go_test!==prefix+"/"+source.title.replaceAll(/\s/g,"_")||record.native_type_obligations!==0||results.get(`ponder.local/ponder-go/runtime:${record.go_test}`)?.Action!=="pass")throw Error("Runtime multichain native case missing or changed");
   seen.add(source.id);
  }
 }
}

const indexingProfileIDs=new Set(indexingProfileTargets);
if(runDir&&ledger.tests.some(t=>t.mapping&&indexingProfileIDs.has(t.id))&&!indexingProfileVerification)throw Error("Indexing profile mappings require current complete evidence");
if(indexingProfileVerification){
 const v=indexingProfileVerification;validateCurrentProof(v,"indexing-profile");
 if(!v.success||v.scope!=="complete_module_source_indexing_profile"||v.summary.source_cases!==24||v.summary.source_assertions!==63||v.summary.targeted_cases!==12||v.summary.targeted_assertions!==30||v.summary.source_error_directives!==0||v.summary.complete_mapping_candidates!==12||v.summary.supplementary_source_credit!==0||v.summary.profile_originals!==10||v.summary.prefetch_originals!==2||v.summary.unmapped_client_originals!==12||v.executions.length!==4||v.inputs.some(ref=>!proofValid(ref,v))||![v.previous_actions_proof,v.source_only].every(diskValid))throw Error("Complete12/30/0 indexing profile proof required");
 const selected=ledger.tests.filter(t=>indexingProfileIDs.has(t.id)),seen=new Set();
 if(JSON.stringify(v.supplemental_tests)!==JSON.stringify(indexingProfileSupplementalTests)||JSON.stringify(v.store_tests)!==JSON.stringify(indexingProfileStoreTests)||JSON.stringify(v.store_scenarios)!==JSON.stringify(indexingProfileStoreScenarios))throw Error("Exact current profile supplemental required");
 if(ledger.tests.some(t=>t.mapping&&t.source_file==="packages/core/src/indexing/client.test.ts"&&!indexingProfileIDs.has(t.id)))throw Error("Other twelve client originals remain unmapped in M40");
 for(const e of v.executions){const key=e.mode+"/"+e.backend,results=e.mode==="normal"?go:race;
  if(!["normal/postgres","normal/pglite","race/postgres","race/pglite"].includes(key)||seen.has(key)||e.cases.length!==12||[e.log,e.native,e.artifact,e.probe_artifact,e.capture,e.original,e.result,e.context_archive,...e.source_context,...Object.values(e.probe).filter(x=>x?.file),...e.probe.source_context].some(r=>!proofValid(r,v,e)))throw Error("All native profile mode/backend results required");seen.add(key);
  for(const name of indexingProfileSupplementalTests)if(results.get(`ponder.local/ponder-go/indexing:${name}`)?.Action!=="pass")throw Error("Current profile supplemental missing");
  for(const name of indexingProfileStoreTests)if(results.get(`ponder.local/ponder-go/store:${name}`)?.Action!=="pass")throw Error("Current read queue test missing");
  for(const name of indexingProfileStoreScenarios)if(results.get(`ponder.local/ponder-go/store:TestSourceRPCAsyncReadBackends/${e.backend}/${name}`)?.Action!=="pass")throw Error("Current read queue backend lifecycle missing");
  for(const t of selected){const cs=e.cases.filter(c=>c.source_id===t.id),c=cs[0],prefix=t.source_file.endsWith("/profile.test.ts")?"TestSourceIndexingProfile":"TestSourceIndexingPrefetch",name=prefix+"/"+e.backend+"/"+t.id;if(cs.length!==1||c.go_test!==name||!c.runtime_passed||!c.formal_credit||c.native_type_obligations!==0||t.type_error_directives.length!==0||results.get(`ponder.local/ponder-go/indexing:${name}`)?.Action!=="pass")throw Error("Exact profile/prefetch original result required");}
 }
}

const indexingActionsIDs=new Set(indexingActionsTargets);
if(runDir&&ledger.tests.some(t=>t.mapping&&indexingActionsIDs.has(t.id))&&!indexingActionsVerification)throw Error("Indexing action mappings require current complete evidence");
if(indexingActionsVerification){
 const v=indexingActionsVerification;validateCurrentProof(v,"indexing-actions");
 if(!v.success||v.scope!=="complete_module_source_indexing_actions"||v.summary.source_cases!==23||v.summary.source_assertions!==42||v.summary.targeted_cases!==8||v.summary.targeted_assertions!==9||v.summary.source_error_directives!==0||v.summary.complete_mapping_candidates!==8||v.summary.supplementary_source_credit!==0||v.executions.length!==4||v.inputs.some(ref=>!proofValid(ref,v))||![v.previous_handler_proof,v.source_only].every(diskValid))throw Error("Complete indexing actions proof required");
 const selected=ledger.tests.filter(t=>indexingActionsIDs.has(t.id)),seen=new Set();
 for(const e of v.executions){const key=e.mode+"/"+e.backend,results=e.mode==="normal"?go:race;
  if(!["normal/postgres","normal/pglite","race/postgres","race/pglite"].includes(key)||seen.has(key)||e.cases.length!==8||[e.log,e.native,e.artifact,e.probe_artifact,e.capture,e.original,e.result,e.context_archive,...e.source_context,...Object.values(e.probe).filter(x=>x?.file),...e.probe.source_context].some(r=>!proofValid(r,v,e)))throw Error("All native actions mode/backend results required");seen.add(key);
  for(const t of selected){const cs=e.cases.filter(c=>c.source_id===t.id),c=cs[0],name="TestSourceIndexingActions/"+e.backend+"/"+t.id;if(cs.length!==1||c.go_test!==name||!c.runtime_passed||!c.formal_credit||c.native_type_obligations!==0||t.type_error_directives.length!==0||results.get(`ponder.local/ponder-go/indexing:${name}`)?.Action!=="pass")throw Error("Exact action original result required");}
  if(results.get(`ponder.local/ponder-go/indexing:TestSourceIndexingActionsProbes/${e.backend}`)?.Action!=="pass")throw Error("Real action cache/error vectors missing");
 }
}

const indexingServiceIDs=new Set(indexingServiceTargets),indexingServiceGapIDs=new Set(indexingServiceTypeGaps);
if(runDir&&ledger.tests.some(t=>t.mapping&&t.source_file==="packages/core/src/indexing/index.test.ts")&&!indexingServiceVerification)throw Error("Indexing service mappings require current complete evidence");
if(indexingServiceVerification){
 const v=indexingServiceVerification;validateCurrentProof(v,"indexing-service");
 const selected=ledger.tests.filter(t=>t.source_file==="packages/core/src/indexing/index.test.ts"&&indexingServiceIDs.has(t.id));
 if(!v.success||v.scope!=="complete_module_source_indexing_service"||v.summary.source_cases!==23||v.summary.source_assertions!==42||v.summary.targeted_cases!==15||v.summary.targeted_assertions!==33||v.summary.source_error_directives!==3||v.summary.complete_mapping_candidates!==12||v.summary.native_type_pending_cases!==3||v.summary.native_type_obligations_verified!==0||v.summary.supplementary_source_credit!==0||v.summary.proxy_source_operations!==23||v.summary.proxy_native_operations!==(["indexing-actions-m39","indexing-profile-m40","independent-storage-m41","multichain-atomic-m42"].some(stage=>runDir?.startsWith("parity/runs/"+stage+"/"))?23:22)||v.summary.proxy_descriptor_gap!==(["indexing-actions-m39","indexing-profile-m40","independent-storage-m41","multichain-atomic-m42"].some(stage=>runDir?.startsWith("parity/runs/"+stage+"/"))?0:1)||v.summary.cache_invocation_prefix_gap!==!["indexing-actions-m39","indexing-profile-m40","independent-storage-m41","multichain-atomic-m42"].some(stage=>runDir?.startsWith("parity/runs/"+stage+"/"))||(["indexing-actions-m39","indexing-profile-m40","independent-storage-m41","multichain-atomic-m42"].some(stage=>runDir?.startsWith("parity/runs/"+stage+"/"))&&(v.summary.cache_invocation_prefix_scope!=="strict_standalone_prepared_admission_only"||v.summary.cache_read_visibility_equivalence!==false))||v.summary.source_sync_table_snapshots!=='archival_only'||v.summary.native_business_database_assertions!=='original_insert_find_only'||JSON.stringify(v.type_gaps)!==JSON.stringify(indexingServiceTypeGaps)||v.executions.length!==4||v.inputs.some(ref=>!proofValid(ref,v)))throw Error("Complete indexing runtime proof and honest static gaps required");
 if(!v.proxy||!v.cache||v.proxy.source_credit!==0||v.cache.source_credit!==0||[v.proxy.capture,v.proxy.result,v.proxy.original,v.proxy.context_archive,v.proxy.fixture,...v.proxy.source_context,v.cache.capture,v.cache.result,v.cache.original,v.cache.context_archive,v.cache.fixture,...v.cache.source_context,v.source_only].some(ref=>!diskValid(ref)))throw Error("Current zero-credit actual proxy proof missing");
 const expectedSupplementalTests=['TestSourceIndexingServiceCache','TestClientAsyncCacheOwnership','TestDatabaseErrorBrandNotDisplayName','TestServiceErrorBrandNotDisplayName','TestSourceIndexingServiceProxy','TestPropertyViewReadOrderAndPresence','TestPropertyViewInvalidAccessStickyAndMutation','TestPropertyViewWritesDeleteAndBindingIdentity','TestPropertyViewHasKeysAndDisabledInspection','TestPropertyViewComponentsAndValidation','TestColumnIncludeSourceOrder','TestClientEventBalanceAndIdentity','TestClientPersistentCacheAndErrors','TestClientHTTPTransport','TestDatabaseErrorSourceClassification','TestDatabaseStickyOnlyRetryable','TestDatabaseAdaptersRealBackends','TestDatabasePostgresRealConnectionLossSticky','TestServiceSupplementalTwoChainIdentityAndSetup','TestServiceSupplementalStickyErrorTiming','TestServiceSupplementalConvergenceAndInvalidation','TestServiceSupplementalCancellationAndReentrancy'];
 if(JSON.stringify(v.supplemental_tests)!==JSON.stringify(expectedSupplementalTests))throw Error("Exact indexing supplemental census required");
 const combinations=new Set();
 for(const e of v.executions){
  const combination=e.mode+"/"+e.backend,results=e.mode==="normal"?go:race;
  if(!["normal/postgres","normal/pglite","race/postgres","race/pglite"].includes(combination)||combinations.has(combination)||[e.log,e.native,e.artifact,e.capture,e.original,e.result,...e.source_context].some(ref=>!proofValid(ref,v,e))||e.cases.length!==15)throw Error("All native indexing mode/backend results required");
  combinations.add(combination);
  const seen=new Set();for(const c of e.cases){const source=selected.find(t=>t.id===c.source_id),gap=indexingServiceGapIDs.has(c.source_id),name="TestSourceIndexingService/"+e.backend+"/"+c.source_id;if(!source||seen.has(c.source_id)||c.go_test!==name||!c.runtime_passed||c.formal_credit!==!gap||c.native_type_obligations!==source.type_error_directives.length||c.native_type_status!==(gap?"pending_exact_static_counterpart":"not_applicable")||results.get(`ponder.local/ponder-go/indexing:${name}`)?.Action!=="pass")throw Error("Indexing native runtime/static result differs");seen.add(c.source_id);}
  for(const name of v.supplemental_tests)if(results.get(`ponder.local/ponder-go/indexing:${name}`)?.Action!=="pass")throw Error("Indexing supplemental missing");
  if(results.get(`ponder.local/ponder-go/runtime:TestSourceHistoricalIndexingServiceComposition/${e.backend}`)?.Action!=="pass")throw Error("Actual M37 event to indexing/store composition missing");
 }
 for(const t of selected)if(indexingServiceGapIDs.has(t.id)&&t.mapping)throw Error("TS-only indexing directive must remain unmapped");
}

if (runDir && ledger.tests.some(test => test.mapping && test.source_file === "packages/core/src/runtime/index.test.ts") && !runtimeIndexVerification) throw Error("Runtime index mappings require current complete source/native evidence");
if (runtimeIndexVerification) {
 validateCurrentProof(runtimeIndexVerification, "runtime-index");
 const v=runtimeIndexVerification, selected=ledger.tests.filter(test=>test.source_file==="packages/core/src/runtime/index.test.ts");
 const refs=[v.source_types,...v.inputs];
 if(!v.complete||v.success!==true||v.scope!=="complete_module_source_runtime_index"||v.summary.source_cases!==15||v.summary.source_assertions!==30||v.summary.source_error_directives!==6||v.executions.length!==4||refs.some(ref=>!proofValid(ref,v)))throw Error("Incomplete or stale runtime index source/native proof");
 const combinations=new Set();
 for(const execution of v.executions){
  const combination=execution.mode+"/"+execution.backend;
  if(!["normal/postgres","normal/pglite","race/postgres","race/pglite"].includes(combination)||combinations.has(combination)||[execution.log,execution.native,execution.capture,execution.original,execution.result,...execution.source_context].some(ref=>!proofValid(ref,v,execution)))throw Error("Runtime index execution coverage or context differs");
  combinations.add(combination);
  const results=execution.mode==="normal"?go:race,seen=new Set();
  if(execution.cases?.length!==15)throw Error("Runtime index requires all fifteen actual native source-case results");
  for(const record of execution.cases){
   const source=selected.find(test=>test.id===record.source_id),prefix=execution.native_test_prefix;
   if(!source||seen.has(source.id)||prefix!=="TestSourceRuntimeIndexFullFile/"+execution.backend||record.go_test!==prefix+"/"+source.title.replaceAll(" ","_")||results.get(`${record.go_package??"ponder.local/ponder-go/runtime"}:${record.go_test}`)?.Action!=="pass")throw Error("Runtime index native case is missing or not passed: "+record.source_id);
   seen.add(source.id);
  }
 }
 const types=read(v.source_types.file);
 if(!types.complete||!types.success||types.source_credit!==0||!types.original_source_unchanged||types.pristine_diagnostics!==0||types.stripped_diagnostics!==6||types.explicit_key_union_diagnostics!==0||types.obligations.length!==6||types.source_sha256!==selected[0]?.source_sha256||types.go_obligation_treatment?.translated_go_negative_compile_tests!==0)throw Error("Runtime index source-language inference audit is incomplete");
 if(types.executions?.length!==3||types.executions.some(execution=>execution.exit_code!==0||!diskValid(execution.report)||!execution.result?.success||!execution.result.source_context.every(ref=>proofValid(ref,v))))throw Error("Runtime index compiler execution context changed");
 for(const test of selected.filter(test=>test.type_error_directives.length)){
  if(test.mapping)throw Error("Runtime index inference-only cases must remain unmapped while native type treatment is pending");
  if(types.obligations.filter(obligation=>obligation.source_id===test.id).length!==test.type_error_directives.length)throw Error("Runtime index directive mapping differs");
 }
}

const preparedNativeFile=runDir?requireFile(runFile("prepared-native-inputs.json")):"parity/runs/prepared-native-inputs.json";
const preparedNative=exists(preparedNativeFile)?read(preparedNativeFile):null;
if(executions.every(Boolean)) {
 if(!preparedNative||executions.some(x=>x.source_fingerprint.sha256!==preparedNative.fingerprint.sha256)||Date.parse(preparedNative.recorded_utc)>Date.parse(executions[0].started_utc)||!preparedNative.files.every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)||fileHash(preparedNative.tool.file)!==preparedNative.tool.sha256)throw Error("Prepared native input snapshot must precede execution and remain unchanged");
 if(runDir&&(!diskValid(fullRun.prepared_native_inputs)||path.resolve(root,fullRun.prepared_native_inputs.file)!==path.resolve(root,preparedNativeFile)||Date.parse(preparedNative.recorded_utc)<Date.parse(fullRun.started_utc)||fullRun.native_inputs?.sha256!==preparedNative.fingerprint.sha256||fullRun.finished_native_inputs?.sha256!==preparedNative.fingerprint.sha256))throw Error("Prepared native snapshot belongs to a different full run");
}
const configOriginal=!runDir&&exists("parity/runs/config-oracle.json")?read("parity/runs/config-oracle.json"):null;
if(configOriginal&&(!configOriginal.complete||!configOriginal.success||![configOriginal.raw_oracle,...configOriginal.source_context].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)))throw Error("Pristine config oracle context changed");
for (const fixtureFile of ["testdata/runtime/captured-original-events.json", "testdata/runtime/captured-original-filters.json", "testdata/bloom/captured-original-blooms.json"]) {
	if (!exists(fixtureFile)) continue;
	const fixture = read(fixtureFile), proof = fixture.capture_proof;
	if (fixture.baseline?.revision !== ledger.baseline.revision || proof?.scope !== "instrumented_original_file_with_call_through" || !historicalCaptureContext(fixtureFile).every(x => exists(x.file) && fileHash(x.file) === x.sha256) || !exists(proof.raw_oracle.file) || fileHash(proof.raw_oracle.file) !== proof.raw_oracle.sha256 || !read(proof.raw_oracle.file).success) throw Error("Original capture context changed or is incomplete: " + fixtureFile);
	for (const captured of fixture.cases) {
		const source = ledger.tests.find(x => x.id === captured.id);
		if (!source || source.source_sha256 !== captured.source_sha256 || source.test_source !== captured.test_source || !captured.calls.length) throw Error("Stale or incomplete original captured declaration: " + captured.id);
	}
}
const sqlVerification=exists("parity/runs/sql-source-verification.json")?read("parity/runs/sql-source-verification.json"):null;
if(ledger.tests.some(t=>t.mapping&&t.source_file==="packages/core/src/utils/sql-parse.test.ts")&&!sqlVerification)throw Error("SQL source mappings require original/input/rules proof");
if(sqlVerification){
 if(JSON.stringify(sqlVerification.capture.source_context)!==JSON.stringify(historicalCaptureContext("testdata/sql/captured-original-sql.json")))throw Error("SQL historical archive differs from pinned capture");
 if(runDir)validateCurrentProof(sqlVerification,"sql");
 const originals=runDir?sqlVerification.current_original?.executions:[];
 if(runDir&&(!Array.isArray(originals)||originals.length!==2||new Set(originals.map(execution=>execution.backend)).size!==2||originals.some(execution=>!["postgres","pglite"].includes(execution.backend)||execution.source_cases!==14||execution.source_assertions!==19||path.resolve(root,execution.raw_oracle.file)!==path.join(runDirectory,"remaining-mapped-original-"+execution.backend+".json"))))throw Error("SQL requires this run's two complete original backend reports");
 const refs=[...sqlVerification.inputs,sqlVerification.rules,...(runDir?originals.flatMap(execution=>[execution.raw_oracle,...execution.source_context]):[sqlVerification.pristine.report,sqlVerification.pristine.raw_oracle,...sqlVerification.pristine.source_context]),sqlVerification.capture.fixture,sqlVerification.capture.raw_oracle,...sqlVerification.capture.source_context,sqlVerification.vectors.fixture,...sqlVerification.vectors.sources];
 if(sqlVerification.summary.source_cases!==14||sqlVerification.summary.source_assertions!==19||sqlVerification.summary.captured_operations!==38||refs.some(f=>!proofValid(f,sqlVerification)))throw Error("Incomplete/stale SQL original/native context");
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
const coreOraclePath = runDir ? requireFile(runFile("core-oracle.json")) : exists("parity/runs/core-oracle.json")
	? "parity/runs/core-oracle.json"
	: null;
const isolatedCoreMetadata = {
	mode:"isolated_complete_source_files",
	execution:"isolated complete source files without application global setup",
	isolated:true,full_original_file:true,environment_adapted:false,pristine_file:false,
	original_application_setup:false,configuration:"tools/oracle.config.mjs",
};
if(runDir&&!read(coreOraclePath).success)throw Error("Current core oracle did not pass");
const remainingOriginalReports = runDir ? ["postgres", "pglite"].map(backend => {
	const name="remaining-mapped-original-"+backend, file=requireFile(runFile(name+".json"));
	const commands=fullRun.commands.filter(command=>command.name===name),report=read(file);
	if(commands.length!==1||commands[0].exit_code!==0||!commands[0].finished_utc||!report.success)throw Error("Current remaining mapped originals did not finish: "+backend);
	return {path:file,report,backend,currentOriginal:true};
}) : [];
const fullOriginalPaths = runDir ? [] : ["parity/runs/full-original-postgres-oracle.json", "parity/runs/full-original-pglite-oracle.json"];
if (fullOriginalPaths.some(exists) && !fullOriginalPaths.every(exists)) throw Error("Full original acceptance requires separate PG and PGlite evidence");
const fullOriginalReports = fullOriginalPaths.filter(exists).map(path => {
	const report = read(path), acceptance = report.ponder_acceptance;
	if (!acceptance?.complete || acceptance.baseline.revision !== ledger.baseline.revision || !acceptance.source_context.every(f => exists(f.file) && fileHash(f.file) === f.sha256) || !acceptance.raw_reports.every(f => exists(f.file) && fileHash(f.file) === f.sha256)) throw Error("Original acceptance context changed or is incomplete: " + path);
	return { path, report, fullOriginal: true, backend: acceptance.backend };
});
const oracleReports = [
 ...(sqlVerification&&!runDir?[{path:sqlVerification.pristine.raw_oracle.file,report:read(sqlVerification.pristine.raw_oracle.file),completeOriginal:true}]:[]),
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
		.map((path) => ({ path, report: read(path), currentOriginal:!!runDir&&path===coreOraclePath, isolated:path===coreOraclePath })),
	...fullOriginalReports,
 ...remainingOriginalReports,
 ...(exists("testdata/bloom/captured-original-blooms.json") ? (()=>{ const proof=read("testdata/bloom/captured-original-blooms.json").capture_proof;return [{path:proof.raw_oracle.file,report:read(proof.raw_oracle.file),instrumented:true}];})() : []),
];
function sdkResult(file, title) {
	if (!exists(file)) return;
	for (const f of read(file).testResults ?? [])
		for (const t of f.assertionResults ?? []) if (t.title === title) return t;
}
function findOriginal(report, test) {
	const config=test.source_file==="packages/core/src/build/config.test.ts";
	const name=config&&test.title.includes("%s")?test.title.replace("%s",test.parameter?.$special??String(test.parameter)):test.full_name;
	const matches=(report.testResults??[]).filter(file=>file.name.endsWith(test.source_file.replace("packages/core/", "")))
		.flatMap(file=>file.assertionResults??[]).filter(result=>(config?result.title:[...(result.ancestorTitles??[]),result.title].join(" > "))===name);
	const occurrence=ledger.tests.filter(entry=>entry.source_file===test.source_file&&entry.full_name===test.full_name&&entry.start<test.start).length;
	return matches[occurrence];
}
function currentLiveOracle(test, verification, evidence, expectedExecutions) {
	const observed=[];
	for(const execution of verification.executions){
		const report=read(execution.original.file);
		if(!report.testResults?.some(file=>file.name.endsWith(test.source_file.replace("packages/core/", ""))))continue;
		const original=findOriginal(report,test);
		observed.push({mode:execution.native_mode??execution.mode,backend:execution.backend??null,status:original?.status??"missing",evidence:execution.original.file});
	}
	if(observed.length!==expectedExecutions)throw Error("Current original execution census differs for "+test.id);
	const status=observed.every(result=>result.status==="passed")?"passed":observed.some(result=>result.status==="failed")?"failed":observed.every(result=>["pending","skipped"].includes(result.status))?"skipped":"blocked";
	test.oracle={status,evidence,mode:"environment_adapted_complete_original_file",full_original:true,environment_adapted:true,pristine_file:false,verification_period:"current",run_dir:runDir,executions:observed};
	test.oracle_current_status=status;
	if(status==="passed"&&test.mapping?.oracle_blocker)test.blockers=test.blockers.filter(reason=>reason!==test.mapping.oracle_blocker.reason);
}
for (const test of ledger.tests) {
	if(runDir){
		// Never let a previous native pass survive a missing current test result.
		for(const key of ["execution_evidence","race","parity_status","type_verification","type_directives_verification","runtime_status","runtime_validation","remaining_native_type_obligations","oracle_types"])delete test[key];
		test.execution_status="not_run";test.pass_status=null;
		test.blockers=(test.blockers??[]).filter(reason=>!["Native runtime alone is insufficient: inline type compilation/typed output evidence is incomplete.","Source type-error directive requires a verified native compile-failure fixture.","Execution reported skipped; no parity credit.","No native result in the selected full run."].includes(reason));
		if(test.oracle){test.historical_oracle_reference=test.oracle;test.oracle={...test.oracle,verification_period:"historical",current_status:"historical_only"};}
		test.oracle_current_status=test.oracle?"historical_only":"not_run";
	}
	const fullBackends = {};
	const currentBackends = {};
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
		if (original && (!runDir || selected.currentOriginal || test.oracle?.verification_period!=="current")) {
			test.oracle = { status: original.status, evidence: selected.path, ...(selected.instrumented ? {mode:"instrumented_original_file_with_call_through",pristine_original_acceptance:false}: {}), ...(selected.completeOriginal?{mode:"complete_unmodified_original_file_with_original_setup",full_original:true}:{}), ...(runDir?selected.currentOriginal?{...(selected.isolated?isolatedCoreMetadata:{mode:"environment_adapted_complete_original_file",full_original:true,environment_adapted:true,pristine_file:false}),verification_period:"current",run_dir:runDir}:{verification_period:"historical",current_status:"historical_only"}:{}) };
			if(runDir)test.oracle_current_status=selected.currentOriginal?original.status:"historical_only";
		}
		if (selected.fullOriginal && original) fullBackends[selected.backend] = { status: original.status, evidence: selected.path };
		if (selected.currentOriginal && selected.backend && original) currentBackends[selected.backend] = { status: original.status, evidence: selected.path };
	}
	if (Object.keys(fullBackends).length && test.oracle?.verification_period!=="current") {
		const results = Object.values(fullBackends), passed = results.length === 2 && results.every(x => x.status === "passed");
		test.oracle = { status: passed ? "passed" : results.some(x => x.status === "failed") ? "failed" : results.every(x => x.status === "skipped") ? "skipped" : "blocked", evidence: fullBackends.pglite?.evidence ?? fullBackends.postgres?.evidence, mode: "unmodified_original_files_with_original_setup", full_original: true, backends: fullBackends, ...(runDir?{verification_period:"historical",current_status:"historical_only"}:{}) };
		if (passed && test.mapping?.oracle_blocker) {
			test.resolved_historical_oracle_blocker = test.mapping.oracle_blocker;
			test.blockers = test.blockers.filter(x => x !== test.mapping.oracle_blocker.reason);
		}
	}
	if(Object.keys(currentBackends).length){
		const values=Object.values(currentBackends),status=values.length===2&&values.every(value=>value.status==="passed")?"passed":values.some(value=>value.status==="failed")?"failed":values.length===2&&values.every(value=>["skipped","pending"].includes(value.status))?"skipped":"blocked";
		test.oracle={status,evidence:currentBackends.pglite?.evidence??currentBackends.postgres?.evidence,backends:currentBackends,mode:"environment_adapted_complete_original_file",full_original:true,environment_adapted:true,pristine_file:false,verification_period:"current",run_dir:runDir};test.oracle_current_status=status;
	}

 if (test.source_file === "packages/core/src/sync-store/index.test.ts" && syncVerification) {
  if(runDir)currentLiveOracle(test,syncVerification,"parity/runs/source-sync-verification.json",4);
  else {
  const pristine=read(syncVerification.pristine.file), backends={};
  for(const execution of pristine.executions){
   const original=read(execution.raw_oracle.file).testResults.flatMap(f=>f.assertionResults).find(t=>t.title===test.title);
   if(original)backends[execution.backend]={status:original.status,evidence:execution.raw_oracle.file};
  }
  const passed=Object.keys(backends).length===2&&Object.values(backends).every(x=>x.status==="passed");
  test.oracle={status:passed?"passed":"blocked",evidence:syncVerification.pristine.file,mode:"complete_unmodified_original_file_with_original_setup",full_original:true,backends};
  }
  test.runtime_validation={status:"passed",evidence:"parity/runs/source-sync-verification.json",meaning:"Actual native values, ordered Maps/identities, SQL ranges and nine physical tables compared during complete original executions in both Go modes and both backends"};
 }
 if (test.source_file.includes("/sync-historical/") && historicalVerification) {
  if(runDir)currentLiveOracle(test,historicalVerification,"parity/runs/source-historical-verification.json",4);
  else {
  const pristine=read(historicalVerification.pristine.file),backends={};
  for(const execution of pristine.executions){
   const original=read(execution.raw_oracle.file).testResults.filter(f=>f.name.endsWith(test.source_file.replace("packages/core/",""))).flatMap(f=>f.assertionResults).find(t=>[...(t.ancestorTitles??[]),t.title].join(" > ")===test.full_name);
   if(original)backends[execution.backend]={status:original.status,evidence:execution.raw_oracle.file};
  }
  const passed=Object.keys(backends).length===2&&Object.values(backends).every(x=>x.status==="passed");
  test.oracle={status:passed?"passed":"blocked",evidence:historicalVerification.pristine.file,mode:"complete_unmodified_original_files_with_original_setup",full_original:true,backends};
  }
  test.runtime_validation={status:"passed",evidence:"parity/runs/source-historical-verification.json",meaning:"Complete original historical files, genuine provider inputs and native values, ordered Map/identity semantics and nine physical tables compared in both Go modes/backends; exact type programs execute before original case Anvil cleanup"};
 }
 if (test.source_file === "packages/core/src/sync-realtime/index.test.ts" && realtimeVerification) {
  if(runDir)currentLiveOracle(test,realtimeVerification,"parity/runs/source-realtime-verification.json",4);
  else {
  const pristine=read(realtimeVerification.pristine.file),backends={};
  for(const execution of pristine.executions){
   const original=read(execution.raw_oracle.file).testResults.filter(f=>f.name.endsWith(test.source_file.replace("packages/core/",""))).flatMap(f=>f.assertionResults).find(t=>[...(t.ancestorTitles??[]),t.title].join(" > ")===test.full_name);
   if(original)backends[execution.backend]={status:original.status,evidence:execution.raw_oracle.file};
  }
  const passed=Object.keys(backends).length===2&&Object.values(backends).every(x=>x.status==="passed");
  test.oracle={status:passed?"passed":"blocked",evidence:realtimeVerification.pristine.file,mode:"complete_unmodified_original_file_with_original_setup",full_original:true,backends};
  }
  test.runtime_validation={status:"passed",evidence:"parity/runs/source-realtime-verification.json",meaning:"Complete original realtime file and independent native values/state, actual raw provider inputs and caller iterator steps, input mutations, Map/Set/frame identities and parent warnings in both Go modes/backends; native memory API opens no database"};
 }
 if (test.source_file === "packages/core/src/build/config.test.ts" && (runDir?configCompile:configOriginal)) {
  if(runDir)currentLiveOracle(test,configCompile,"parity/runs/config-live-verification.json",2);
  else {
  const title=test.title.includes("%s")?test.title.replace("%s",test.parameter?.$special??String(test.parameter)):test.title;
  const original=read(configOriginal.raw_oracle.file).testResults.flatMap(f=>f.assertionResults).find(t=>t.title===title);
  if(original)test.oracle={status:original.status,evidence:"parity/runs/config-oracle.json",mode:"complete_unmodified_original_file_with_original_setup",full_original:true};
  }
  if (configCompile && test.source_state==="active") {
   test.runtime_validation={status:"passed",evidence:"parity/runs/config-live-verification.json",meaning:"Actual native runtime construction checked in both complete Go modes; source port/type completion remains separate"};
   if (!test.mapping && test.type_error_directives.length) test.remaining_native_type_obligations=test.type_error_directives.length;
  }
 }
 if(runDir&&runtimeHistoricalIDs.has(test.id)&&test.source_file==="packages/core/src/runtime/historical.test.ts"&&runtimeHistoricalVerification){
  currentLiveOracle(test,runtimeHistoricalVerification,runtimeHistoricalVerificationFile,4);
  if(runtimeHistoricalIDs.has(test.id))test.runtime_validation={status:"passed",evidence:runtimeHistoricalVerificationFile,run_dir:runDir,meaning:"Eleven original local generator cases replayed on both real native backends in both full modes. Complete adapted fourteen-case source file remains separately recorded. PGlite atomic rollback and cancellation/error joining are documented safety extensions, not source-equivalence claims."};
 }
 if(runDir&&runtimeMultichainIDs.has(test.id)&&runtimeMultichainVerification){
  currentLiveOracle(test,runtimeMultichainVerification,runtimeMultichainVerificationFile,4);
  test.runtime_validation={status:"passed",evidence:runtimeMultichainVerificationFile,run_dir:runDir,meaning:"Three original single-chain coordinator cases, five assertions, both real backends and complete native modes. Separate genuine multi-chain and helper probes earn zero source credit. Outer indexing transactions and realtime cutover remain out of scope."};
 }
 if(runDir&&test.source_file==="packages/core/src/indexing/index.test.ts"&&indexingServiceVerification){
  currentLiveOracle(test,indexingServiceVerification,indexingServiceVerificationFile,4);
  if(indexingServiceIDs.has(test.id)){
   test.runtime_validation={status:"passed",evidence:indexingServiceVerificationFile,run_dir:runDir,meaning:"All fifteen handler-service originals replayed on real native PostgreSQL/PGlite in full normal/race modes. Explicit historical property accessor syntax differs from JS Proxy syntax. Separate actual M37 history-to-store and proxy reflection probes earn zero source credit; outer runner remains out of scope. Eight client facade cases have a separate M39 proof when present."};
   if(indexingServiceGapIDs.has(test.id)){test.runtime_status="passed";test.execution_status="executed";test.pass_status=null;test.execution_evidence=indexingServiceVerificationFile;test.race={status:"pass",evidence:raceFile};test.remaining_native_type_obligations=test.type_error_directives.length;test.type_directives_verification={status:"pending",native_verified:0,obligations:test.type_error_directives.length,meaning:"Exact TS obligations are missing mockRejectedValue on normal callback (two cases), and deletion of required transaction.maxPriorityFeePerGas (one case). Runtime execution is not static-equivalence proof."};test.parity_status="runtime_passed_type_obligations_pending";}
  }
 }
 if(runDir&&indexingProfileIDs.has(test.id)&&indexingProfileVerification){
  currentLiveOracle(test,indexingProfileVerification,indexingProfileVerificationFile,4);
  test.runtime_validation={status:"passed",evidence:indexingProfileVerificationFile,run_dir:runDir,meaning:"Ten unchanged profile originals and two prefetch originals, thirty assertions and zero static directives replay from actual inputs on both source/native backend fixtures in complete normal/race. Controlled source boundary probes earn no extra credit. Other twelve client originals, full cachedViemClient/raw transport, outer runner and topology remain out of scope."};
 }
 if(runDir&&indexingActionsIDs.has(test.id)&&indexingActionsVerification){
  currentLiveOracle(test,indexingActionsVerification,indexingActionsVerificationFile,4);
  test.runtime_validation={status:"passed",evidence:indexingActionsVerificationFile,run_dir:runDir,meaning:"Eight unchanged public-action originals, nine assertions and zero static directives replay on PostgreSQL/PGlite in full normal/race. Genuine ABI/aggregate3/cache behavior; real supplemental vectors earn no extra credit. No profiling/prefetch/LRU or full indexing/client.test.ts claim."};
 }
 if(runDir&&test.source_file==="packages/core/src/runtime/index.test.ts"&&runtimeIndexVerification){
  currentLiveOracle(test,runtimeIndexVerification,runtimeIndexVerificationFile,4);
  test.runtime_validation={status:"passed",evidence:runtimeIndexVerificationFile,run_dir:runDir,meaning:"All original runtime/index inputs and assertions replayed against native Go in both complete module modes and both backends; adapted source capture and native execution remain separately identified."};
  if(test.type_error_directives.length){
   test.runtime_status="passed";test.execution_status="executed";test.pass_status=null;
   test.execution_evidence=runtimeIndexVerificationFile;test.race={status:"pass",evidence:raceFile};
   test.remaining_native_type_obligations=test.type_error_directives.length;
   test.type_directives_verification={status:"pending",source_language_status:"verified",source_language_evidence:runtimeIndexVerification.source_types.file,native_verified:0,obligations:test.type_error_directives.length,meaning:"Heterogeneous TypeScript Map inference, not rejection of Factory keys. Dynamic Go keys preserve positive runtime behavior, but equivalent native type treatment remains pending."};
   test.parity_status="runtime_passed_type_obligations_pending";
  }
 }
	if (test.source_file.endsWith("/decodeAbiParameters.test.ts") && parameterTypes?.source_sha256 === test.source_sha256)
		test.oracle_types = {status: parameterTypes.exit_code === 0 ? "passed" : "failed", evidence:"parity/runs/parameters-typecheck.json"};
	if (test.source_file.endsWith("/decodeEventLog.test.ts") && parameterTypes?.event_source_sha256 === test.source_sha256)
		test.oracle_types = {status: parameterTypes.exit_code === 0 ? "passed" : "failed", evidence:"parity/runs/parameters-typecheck.json"};

 for(const mode of runDir?[]:["runtime","mapped"]) {
  const selectionFile=`parity/selected-${mode}-declarations.json`,reportFile=`parity/runs/selected-${mode}-oracle.json`;
  if(!exists(selectionFile)||!exists(reportFile))continue;
  const selection=read(selectionFile),report=read(reportFile),source=selection.cases.find(c=>c.id===test.id&&c.source_sha256===test.source_sha256&&c.test_source===test.test_source),generated=selection.generated_file??"testdata/oracle/selected-runtime.test.ts";
  if(!test.oracle?.full_original&&source&&report.success&&fileHash(generated)===selection.generated_sha256&&fs.readFileSync(path.join(root,generated),"utf8").includes(source.test_source)) {
   const result=report.testResults.flatMap(r=>r.assertionResults).find(r=>r.title===test.title);
   if(result)test.oracle={status:result.status,evidence:reportFile,mode:"selected_unchanged_source_declarations_actual_dependencies",source_selection:selectionFile,full_original_suite:{status:"blocked_before_collection",evidence:test.source_file.endsWith("/filter.test.ts")?"parity/runs/filter-oracle-blocked.json":"parity/runs/runtime-events-oracle-blocked.json",reason:"Missing generated.js; these selected original declarations have no generated ABI dependency."}};
  }
 }

 if(!runDir&&!test.oracle?.full_original&&!test.mapping&&test.source_file.endsWith("/runtime/filter.test.ts")&&["isTransactionFilterMatched()","isTransactionFilterMatched() with null transaction.to","isTransferFilterMatched()"].includes(test.title)&&exists("parity/runs/selected-with-eth-oracle.json")) {
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
				? reactFile
				: clientFile;
		status = sdkResult(evidence, test.title)?.status;
		test.ownership = test.mapping.owner;
	} else {
		const key = `${test.mapping.go_package}:${test.mapping.go_test}`;
		status = go.get(key)?.Action;
		evidence = goFile;
		if (race.has(key))
			test.race = {
				status: race.get(key).Action,
				evidence: raceFile,
			};
		if (test.mapping.required_compile_source_id) {
			const obligations = compile?.obligations.filter(x => x.source_id === test.id && x.source_sha256 === test.source_sha256) ?? [];
			const expected = test.assertions.filter(x => x.kind === "type").length;
			const typedKey = `ponder.local/ponder-go/abi/testdata/compile/generated:TestGeneratedTypedParameters/${test.id}`;
			const valid = obligations.length === expected && expected > 0 && obligations.every(x => x.status === "passed" && [x.positive,x.negative_control,x.generated].every(f => exists(f.file) && fileHash(f.file) === f.sha256)) && typedGo.get(typedKey)?.Action === "pass";
			test.type_verification = {status:valid?"passed":"blocked", obligations:expected, compile_evidence:"parity/runs/compile-fixtures.json", typed_runtime_evidence:typedGoFile};
			test.runtime_status = status;
			if (!valid) {status="skip";test.blockers.push("Native runtime alone is insufficient: inline type compilation/typed output evidence is incomplete.");}
		}
		if (test.type_error_directives.length) {
			const directives=[...(compile?.directives??[]),...(ormCompile?.directives??[]),...(eventCompile?.directives??[]),...(bloomCompile?.directives??[]),...(configCompile?.directives??[]),...(syncVerification?.directives??[]),...(historicalVerification?.directives??[])].filter(x=>x.source_id===test.id&&x.source_sha256===test.source_sha256);
			const generatedValid=!ormCompile?.directives.some(x=>x.source_id===test.id)||((ormCompile.inputs?.length??0)>=3&&[...ormCompile.generated,...ormCompile.inputs].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256));
			const eventValid=!eventCompile?.directives.some(x=>x.source_id===test.id)||((eventCompile.inputs?.length??0)>=3&&eventCompile.inputs.every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&Number.isInteger(x.build_negative?.exit_code)&&x.build_negative.exit_code>0&&/callbacks\[0\]\.ABIItem undefined/.test(x.build_negative?.stderr??"")));
			const bloomValid=!bloomCompile?.directives.some(x=>x.source_id===test.id)||((bloomCompile.inputs?.length??0)>=5&&bloomCompile.inputs.every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&Number.isInteger(x.build_negative?.exit_code)&&x.build_negative.exit_code>0&&/cannot use filter\.Undefined.*as filter\.HexTopic/.test(x.build_negative?.stderr??"")));
			const configDiagnostic=x=>x.mode?.startsWith("network-")?/does not satisfy config\.SourceTypedContractShape/.test(x.build_negative.stderr):x.mode?.startsWith("bounds-")?/string does not satisfy config\.SourceBlockBound/.test(x.build_negative.stderr):x.mode==="literal-chain"?/bindings\.Chains\.Mainnetz undefined/.test(x.build_negative.stderr):x.mode==="literal-event"?/bindings\.Events\.Event2 undefined/.test(x.build_negative.stderr):/Filter\.Interval undefined.*config\.SourceFilter has no field or method Interval/.test(x.build_negative.stderr);
   const configValid=!configCompile?.directives.some(x=>x.source_id===test.id)||(configCompile.executions.length===2&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&x.build_negative?.exit_code>0&&configDiagnostic(x)));
			const syncValid=!syncVerification?.directives.some(x=>x.source_id===test.id)||(syncVerification.executions.length===4&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&x.execute_positive.executions.length===4&&x.execute_positive.executions.every(e=>e.run?.passed)&&x.build_negative?.exit_code>0&&/cannot use \[\]string.*as rpc\.SourceCallParameters value/.test(x.build_negative.stderr)));
   const historicalValid=!historicalVerification?.directives.some(x=>x.source_id===test.id)||(historicalVerification.executions.length===8&&directives.every(x=>x.build_positive?.exit_code===0&&x.execute_positive?.exit_code===0&&x.execute_positive.executions.length===4&&x.execute_positive.executions.every(e=>e.run?.passed)&&x.build_negative?.exit_code>0&&x.specific_negative_diagnostic&&[x.positive_binary,...read(historicalVerification.compile.file).production_inputs].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256)));
			const valid=generatedValid&&eventValid&&bloomValid&&configValid&&syncValid&&historicalValid&&directives.length===test.type_error_directives.length&&directives.every(x=>x.status==="passed"&&[x.positive,x.negative].every(f=>exists(f.file)&&fileHash(f.file)===f.sha256));
			test.type_directives_verification={status:valid?"passed":"blocked",evidence:["parity/runs/compile-fixtures.json",...(ormCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/orm-compile-fixtures.json"]:[]),...(eventCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/event-compile-fixtures.json"]:[]),...(bloomCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/bloom-compile-fixtures.json"]:[]),...(configCompile?.directives.some(x=>x.source_id===test.id)?["parity/runs/config-live-verification.json"]:[]),...(syncVerification?.directives.some(x=>x.source_id===test.id)?["parity/runs/source-sync-verification.json"]:[]),...(historicalVerification?.directives.some(x=>x.source_id===test.id)?["parity/runs/source-historical-verification.json"]:[])]};
			if(!valid){status="skip";test.blockers.push("Source type-error directive requires a verified native compile-failure fixture.");}
		}
	}
	if (status) {
		if (test.mapping.target_kind === "retained_sdk")
			test.oracle = {
				status: ["pass", "passed"].includes(status) ? "passed" : status,
				evidence,
				...(runDir?{verification_period:"current",run_dir:runDir}:{}),
			};
		if(runDir&&test.mapping.target_kind==="retained_sdk")test.oracle_current_status=test.oracle.status;
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
	else if(runDir)test.blockers.push("No native result in the selected full run.");
	test.parity_status = test.pass_status === "passed" ? test.oracle?.status === "passed" ? runDir&&test.oracle_current_status==="historical_only"?"target_passed_historical_original_oracle_passed":"target_and_original_oracle_passed" : test.oracle?.status === "blocked" ? "target_passed_original_oracle_blocked" : "target_passed_original_oracle_not_passed" : "mapped_target_not_passed";
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
	...(runDir?{
		current_original_oracle_passed:ledger.tests.filter(x=>x.mapping&&x.oracle_current_status==="passed").length,
		historical_only_original_oracles:ledger.tests.filter(x=>x.mapping&&x.oracle_current_status==="historical_only").length,
		runtime_passed_type_pending:ledger.tests.filter(x=>!x.mapping&&x.runtime_status==="passed"&&x.type_directives_verification?.status==="pending").length,
	}:{}),
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
	...(runDir?{run_dir:runDir,environment_adapted:true,pristine_file:false,full_verification:{file:fullRunFile,sha256:fileHash(fullRunFile)},context_archive:fullRun.context_archive,prepared_native_inputs:{file:preparedNativeFile,sha256:fileHash(preparedNativeFile)}}:{}),
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
 selected_source_oracle: exists("parity/runs/selected-runtime-oracle.json")?{evidence:"parity/runs/selected-runtime-oracle.json",...(runDir?{verification_period:"historical",current_status:"historical_only"}:{}),mode:"unchanged_selected_declarations_with_actual_source_dependencies",total:read("parity/runs/selected-runtime-oracle.json").numTotalTests,passed:read("parity/runs/selected-runtime-oracle.json").numPassedTests,success:read("parity/runs/selected-runtime-oracle.json").success}:null,
 selected_existing_oracle: exists("parity/runs/selected-mapped-oracle.json")?{evidence:"parity/runs/selected-mapped-oracle.json",...(runDir?{verification_period:"historical",current_status:"historical_only"}:{}),mode:"unchanged_already_mapped_filter_genesis_split_declarations",total:read("parity/runs/selected-mapped-oracle.json").numTotalTests,passed:read("parity/runs/selected-mapped-oracle.json").numPassedTests,success:read("parity/runs/selected-mapped-oracle.json").success}:null,
	filter_fragment_differential: exists("testdata/runtime/filter-differential.json") ? (() => {
		const fixture=read("testdata/runtime/filter-differential.json"),selected=native.filter(x=>x.Package==="ponder.local/ponder-go/filter"&&x.Test.startsWith("TestFilterDifferential/")),selectedRace=nativeRace.filter(x=>x.Package==="ponder.local/ponder-go/filter"&&x.Test.startsWith("TestFilterDifferential/"));
		return {counting:fixture.counting,vectors:fixture.vectors.length,source_expected_exceptions:fixture.vectors.filter(x=>x.error).length,go:count(selected),race:count(selectedRace),evidence:"testdata/runtime/filter-differential.json"};
	})() : null,
	original_oracle: {mapped_passed:ledger.summary.oracle_passed,mapped_blocked:ledger.summary.oracle_blocked,...(runDir?{current_mapped_passed:ledger.summary.current_original_oracle_passed,historical_only_mapped:ledger.summary.historical_only_original_oracles}:{}),meaning:"Original execution and native execution are separate evidence. Historical-only references are not current-run source passes; only explicit differential vectors are labelled differential verification."},
	full_original_backend_oracles: fullOriginalReports.map(x => ({backend:x.backend,evidence:x.path,scope:"Eight original source files, not complete upstream acceptance or additional Go port credit",passed:x.report.numPassedTests,failed:x.report.numFailedTests,pending:x.report.numPendingTests,source_context_hashes_verified:true})),
	current_remaining_original_backend_oracles:remainingOriginalReports.map(x=>({backend:x.backend,evidence:x.path,scope:"Nine complete original source files covering remaining mapped cases; unmapped cases add no port credit",environment_adapted:true,pristine_file:false,passed:x.report.numPassedTests,failed:x.report.numFailedTests,pending:x.report.numPendingTests,context_archive:fullRun.context_archive})),
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
		error_directives_source_language_verified_native_pending:ledger.tests.filter(x=>x.type_directives_verification?.status==="pending"&&x.type_directives_verification.source_language_status==="verified").reduce((n,x)=>n+x.type_error_directives.length,0),
	},
 indexed_topic_source_verification:topicVerification?{...topicVerification.summary,evidence:"parity/runs/topic-source-verification.json"}:null,
 source_realtime_verification:realtimeVerification?{...realtimeVerification.summary,evidence:"parity/runs/source-realtime-verification.json"}:null,
 source_historical_verification:historicalVerification?{...historicalVerification.summary,evidence:"parity/runs/source-historical-verification.json"}:null,
 source_sync_verification:syncVerification?{...syncVerification.summary,evidence:"parity/runs/source-sync-verification.json"}:null,
 source_indexing_profile_verification:indexingProfileVerification?{...indexingProfileVerification.summary,evidence:indexingProfileVerificationFile,environment_adapted:true,pristine_file:false}:null,
 source_indexing_actions_verification:indexingActionsVerification?{...indexingActionsVerification.summary,evidence:indexingActionsVerificationFile,environment_adapted:true,pristine_file:false}:null,
 source_indexing_service_verification:indexingServiceVerification?{...indexingServiceVerification.summary,evidence:indexingServiceVerificationFile,environment_adapted:true,pristine_file:false}:null,
 source_runtime_multichain_verification:runtimeMultichainVerification?{...runtimeMultichainVerification.summary,evidence:runtimeMultichainVerificationFile,environment_adapted:true,pristine_file:false}:null,
 source_runtime_historical_verification:runtimeHistoricalVerification?{...runtimeHistoricalVerification.summary,evidence:runtimeHistoricalVerificationFile,environment_adapted:true,pristine_file:false}:null,
 source_runtime_index_verification:runtimeIndexVerification?{...runtimeIndexVerification.summary,evidence:runtimeIndexVerificationFile,environment_adapted:runtimeIndexVerification.environment_adapted,pristine_file:runtimeIndexVerification.pristine_file,native_type_obligations_passed:0}:null,
 sql_source_verification:sqlVerification?{...sqlVerification.summary,evidence:"parity/runs/sql-source-verification.json"}:null,
	typed_abi_runtime: {file:typedGoFile,leaf_cases:leaves(typedGo).length,...count(leaves(typedGo)),counting:"Supplementary typed decoder execution; not additional source test records."},
	typed_abi_race: {file:typedRaceFile,leaf_cases:leaves(typedRace).length,...count(leaves(typedRace))},
	backend_execution: Object.fromEntries(["postgres","pglite"].map(backend=>{
		const selected=native.filter(x=>backendLabelMatches(x.Test,backend)),selectedRace=nativeRace.filter(x=>backendLabelMatches(x.Test,backend));
		return [backend,{counting:"First explicit slash dialect component wins; legacy backend-tagged names remain supported. Source titles mentioning another backend do not double count. Source mappings and backend companions remain separate from source parity.",go:{leaf_cases:selected.length,...count(selected)},race:{leaf_cases:selectedRace.length,...count(selectedRace)},evidence:goFile}];
	})),
	simulation: {seed_space:"unbounded",status:"unimplemented",executed_seeds:0},
	native_go: { file: goFile, leaf_cases: native.length, ...count(native) },
	race: {
		file: raceFile,
		leaf_cases: nativeRace.length,
		...count(nativeRace),
	},
	foundation_oracle: {
		file: oraclePath,
		...(runDir?{verification_period:"historical",current_status:"historical_only"}:{}),
		total: oracle.numTotalTests,
		passed: oracle.numPassedTests,
		failed: oracle.numFailedTests,
	},
	core_oracle: coreOraclePath
		? {
				file: coreOraclePath,
				...isolatedCoreMetadata,
				...(runDir?{verification_period:"current"}:{}),
				total: read(coreOraclePath).numTotalTests,
				passed: read(coreOraclePath).numPassedTests,
				failed: read(coreOraclePath).numFailedTests,
			}
		: null,
	client: sdkResult(clientFile, "setDatabaseSchema table")
		? {
				file: clientFile,
				...Object.fromEntries(
					["numTotalTests", "numPassedTests", "numFailedTests"].map((k) => [
						k,
						read(clientFile)[k],
					]),
				),
			}
		: null,
	react: exists(reactFile)
		? read(reactFile).success
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
