// Full verification writes fresh source captures before it runs the Go tests.
// An explicit run ID binds their fixture bytes, source files and Vitest report
// to the same invocation. Other callers continue to validate the frozen archive.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { isDeepStrictEqual } from "node:util";
import { historicalCaptureContext, manifestFile } from "./historical-capture-context.mjs";

const root = path.resolve(import.meta.dirname, "..");
const modes = new Map([
  ["testdata/runtime/captured-original-events.json", ["events", "runtime/events.test.ts", 11, 6]],
  ["testdata/runtime/captured-original-filters.json", ["filters", "runtime/filter.test.ts", 23, 9]],
  ["testdata/bloom/captured-original-blooms.json", ["blooms", "sync-realtime/bloom.test.ts", 8, 8]],
  ["testdata/sql/captured-original-sql.json", ["sql", "utils/sql-parse.test.ts", 14, 14]],
]);

function sourceFiles(mode, test) {
  const relative = mode === "sql"
    ? [test, "utils/sql-parse.ts", "index.ts"]
    : ["_test/utils.ts", "_test/simulate.ts", "_test/constants.ts", "runtime/events.ts",
        "runtime/filter.ts", test, "rpc/actions.ts", "rpc/index.ts", "utils/decodeEventLog.ts",
        "utils/decodeAbiParameters.ts", "sync-realtime/bloom.ts", "internal/types.ts",
        "utils/lowercase.ts", "utils/checkpoint.ts", "build/factory.ts", "config/address.ts"];
  const core = "../ponder-upstream/packages/core/";
  const files = [
    ...[...relative, "_test/globalSetup.ts", "_test/setup.ts", "_test/generated.ts"].map(name => core + "src/" + name),
    core + "vite.config.ts", "../ponder-upstream/pnpm-lock.yaml", "tools/full-oracle.config.mjs", "tools/loopback-only.mjs",
  ];
  return files.concat(mode === "sql"
    ? ["../ponder-upstream/packages/client/src/index.ts", core + "node_modules/pg-query-emscripten/pg_query.js",
        core + "node_modules/pg-query-emscripten/package.json", "tools/sql-original.mjs",
        "tools/sql-capture.config.mjs", "tools/sql-capture.setup.ts"]
    : ["tools/capture-original.config.mjs", "tools/capture-original.setup.ts", "tools/capture-original.mjs"]);
}

const digest = bytes => crypto.createHash("sha256").update(bytes).digest("hex");

export function captureContext(fixtureFile, run) {
  if (run === undefined) {
    return { sources: historicalCaptureContext(fixtureFile), evidence: manifestFile };
  }
  return currentCaptureContext(root, fixtureFile, run);
}

export function currentCaptureContext(project, fixtureFile, run) {
  const bytes = file => fs.readFileSync(path.join(project, file));
  if (!/^\d{8}T\d{6}-[1-9]\d*$/.test(run) || !modes.has(fixtureFile)) {
    throw Error("Unknown current capture run or fixture");
  }
  const [mode, test, passed, caseCount] = modes.get(fixtureFile);
  const dir = `parity/runs/original-captures/${run}/${mode}`;
  const evidence = dir + "/result.json";
  const report = JSON.parse(bytes(evidence));
  const fixtureBytes = bytes(fixtureFile);
  const fixture = JSON.parse(fixtureBytes);
  const proof = fixture.capture_proof;
  const source = "packages/core/src/" + test;
  const oracleFile = dir + "/original.json";
  const scope = mode === "sql" ? "complete_original_with_call_through" : "instrumented_original_file_with_call_through";
  const raw = bytes(oracleFile);
  const oracle = JSON.parse(raw);
  const suite = oracle.testResults?.[0];
  const config = mode === "sql" ? path.join(project, "tools/sql-capture.config.mjs") : "tools/capture-original.config.mjs";
  const command = ["vitest", "run", "--config", config, "--configLoader", "native", "src/" + test,
    "--reporter=json", "--outputFile=" + path.join(project, oracleFile)];
  if (!fixtureBytes.equals(bytes(dir + "/calls.json")) || fixture.source_file !== source ||
      fixture.cases.length !== caseCount || proof.scope !== scope || proof.pristine_original_acceptance !== false ||
      !isDeepStrictEqual(report.command, command) ||
      proof.original_passed !== passed || proof.original_pending !== 0 ||
      report.complete !== true || report.success !== true || report.exit_code !== 0 || report.signal !== null || report.error !== null ||
      report.original_passed !== passed || proof.raw_oracle.file !== oracleFile || proof.raw_oracle.sha256 !== digest(raw) ||
      oracle.success !== true || oracle.numPassedTests !== passed || oracle.numFailedTests !== 0 || oracle.numPendingTests !== 0 ||
      oracle.testResults.length !== 1 || suite.status !== "passed" || suite.assertionResults.length !== passed ||
      suite.assertionResults.some(result => result.status !== "passed") || suite.name !== path.resolve(project, "../ponder-upstream", source)) {
    throw Error("Incomplete or mismatched current " + mode + " capture");
  }
  if (mode === "sql") {
    if (report.scope !== "complete_original_with_call_through" || report.source_file !== source ||
        !isDeepStrictEqual(report.raw_oracle, proof.raw_oracle) || report.capture.file !== dir + "/calls.json" ||
        report.capture.sha256 !== digest(fixtureBytes) ||
        !isDeepStrictEqual(report.source_context, proof.source_context)) throw Error("Mismatched SQL capture report");
  } else if (report.mode !== mode || report.file !== source || report.selected_source_cases !== caseCount ||
             report.capture_file !== dir + "/calls.json" || typeof report.anvil_version !== "string" ||
             !report.anvil_version.trim() || report.anvil_version !== proof.anvil_version) {
    throw Error("Mismatched source capture report for " + mode);
  }
  const expected = sourceFiles(mode, test).sort();
  if (!isDeepStrictEqual(expected, proof.source_context.map(entry => entry.file).sort()) ||
      !proof.source_context.every(entry => digest(bytes(entry.file)) === entry.sha256)) {
    throw Error("Changed or incomplete source context for " + mode);
  }
  return { sources: proof.source_context, evidence };
}
