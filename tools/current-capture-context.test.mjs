import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { currentCaptureContext } from "./current-capture-context.mjs";

const file = "testdata/sql/captured-original-sql.json";
const run = "20261124T120000-123";
const dir = `parity/runs/original-captures/${run}/sql`;
const sha = bytes => crypto.createHash("sha256").update(bytes).digest("hex");

// The small temporary files exercise provenance checks independently of Anvil.
// Use the real source-file inventory from the versioned SQL fixture.
function sqlCapture(t) {
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), "ponder-capture-context-"));
  t.after(() => fs.rmSync(temp, {recursive: true, force: true}));
  const project = path.join(temp, "project");
  const original = JSON.parse(fs.readFileSync(new URL("../" + file, import.meta.url)));
  function put(name, data) {
    const target = path.resolve(project, name);
    fs.mkdirSync(path.dirname(target), {recursive: true});
    const bytes = typeof data === "string" ? data : JSON.stringify(data);
    fs.writeFileSync(target, bytes);
    return sha(bytes);
  }
  const source = "packages/core/src/utils/sql-parse.test.ts";
  const passed = 14;
  const oracle = {
    success: true, numPassedTests: passed, numFailedTests: 0, numPendingTests: 0,
    testResults: [{name: path.resolve(project, "../ponder-upstream", source), status: "passed",
      assertionResults: Array.from({length: passed}, () => ({status: "passed"}))}],
  };
  const proof = original.capture_proof;
  for (const entry of proof.source_context) entry.sha256 = put(entry.file, entry.file);
  proof.raw_oracle = {file: dir + "/original.json", sha256: put(dir + "/original.json", oracle)};
  const report = {
    complete: true, success: true, exit_code: 0, signal: null, error: null, original_passed: passed,
    command: ["vitest", "run", "--config", path.join(project, "tools/sql-capture.config.mjs"),
      "--configLoader", "native", "src/utils/sql-parse.test.ts", "--reporter=json", "--outputFile=" + path.join(project, proof.raw_oracle.file)],
    scope: proof.scope, source_file: source, raw_oracle: proof.raw_oracle, source_context: proof.source_context,
    capture: {file: dir + "/calls.json", sha256: put(dir + "/calls.json", original)},
  };
  put(file, original);
  put(dir + "/result.json", report);
  return {project, original, report, put};
}

test("a fresh SQL run authenticates its own source and reports", t => {
  const {project, original} = sqlCapture(t);
  assert.deepEqual(currentCaptureContext(project, file, run), {
    sources: original.capture_proof.source_context, evidence: dir + "/result.json",
  });
  for (const bad of ["", "../" + run, "20261124T120000-124"]) {
    assert.throws(() => currentCaptureContext(project, file, bad));
  }
  assert.throws(() => currentCaptureContext(project, "../" + file, run));
});

test("a failed or foreign capture report cannot validate a fixture", t => {
  const {project, report, put} = sqlCapture(t);
  for (const change of [{success: false}, {original_passed: 13}, {command: [...report.command, "--passWithNoTests"]},
    {capture: {...report.capture, sha256: "0".repeat(64)}}]) {
    put(dir + "/result.json", {...report, ...change});
    assert.throws(() => currentCaptureContext(project, file, run));
  }
});

test("reject changed fixture bytes, missing source files and changed harness inputs", t => {
  const {project, original, report, put} = sqlCapture(t);
  put(file, {...original, method: "changed"});
  assert.throws(() => currentCaptureContext(project, file, run));
  put(file, original);
  const first = original.capture_proof.source_context[0];
  put(first.file, "changed");
  assert.throws(() => currentCaptureContext(project, file, run));
  put(first.file, first.file);
  original.capture_proof.source_context.shift();
  report.capture.sha256 = put(dir + "/calls.json", original);
  put(file, original);
  put(dir + "/result.json", report);
  assert.throws(() => currentCaptureContext(project, file, run));
});
