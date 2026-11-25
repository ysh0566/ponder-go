import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { isNativeInput } from "./go-source-fingerprint.mjs";

function workspace(t, files) {
  const parent = fs.mkdtempSync(path.join(os.tmpdir(), "ponder-verification-"));
  t.after(() => fs.rmSync(parent, { recursive:true, force:true }));
  const root = path.join(parent, "project");
  fs.mkdirSync(path.join(root, "tools"), { recursive:true });
  for (const name of files) fs.copyFileSync(new URL(name, import.meta.url), path.join(root, "tools", name));
  const bin = path.join(parent, "bin");
  fs.mkdirSync(bin);
  function executable(file, body) {
    fs.mkdirSync(path.dirname(file), {recursive:true});
    fs.writeFileSync(file, "#!" + process.execPath + "\n" + body, {mode:0o755});
  }
  const env = {...process.env, PATH:bin + path.delimiter + process.env.PATH};
  return {root, bin, env, executable};
}

test("verification inventories every top-level Go package", () => {
  const project = new URL("../", import.meta.url);
  for (const dir of fs.readdirSync(project, {withFileTypes: true})) {
    if (!dir.isDirectory()) continue;
    const files = fs.readdirSync(new URL(dir.name + "/", project));
    for (const name of files.filter(file => file.endsWith(".go"))) {
      const input = dir.name + "/" + name;
      assert(isNativeInput(input), input);
    }
  }
});

test("native reports distinguish failed discovery, unfinished, unstarted and skipped tests", t => {
  const {root, bin, env, executable} = workspace(t, ["go-tests.mjs", "go-source-fingerprint.mjs"]);
  assert.equal(spawnSync("git", ["init", "-q"], {cwd:root}).status, 0);
  executable(path.join(bin, "go"), `
    const args = process.argv.slice(2), pkg = args.at(-1);
    if (args[0] === "list") console.log(pkg === "./..." ? "broken\\ninterrupted\\nhealthy" : pkg);
    else if (args.includes("-list")) {
      if (pkg === "broken") { console.error("build error"); process.exit(1); }
      console.log("TestA\\nTestB");
    } else {
      const event = (Action, Test) => console.log(JSON.stringify({Package:pkg, Action, Test}));
      event("run", "TestA");
      if (pkg === "interrupted") {
        event("run","TestA/child");
        event("fail");
        process.exitCode = 1;
      } else if (pkg === "red") {
        event("fail","TestA"); event("run","TestB"); event("pass","TestB"); event("fail");
        process.exitCode = 1;
      } else {
        event("pass","TestA"); event("run","TestB"); event("skip","TestB"); event("pass");
      }
    }`);
  const run = (extra, ...args) => spawnSync(process.execPath, ["tools/go-tests.mjs", ...args], {cwd:root, env:{...env, ...extra}});
  assert.equal(run({PONDER_GO_TEST_TIMEOUT_MINUTES:"0"}).status, 1);
  assert(!fs.existsSync(path.join(root, "parity/runs")));
  assert.equal(run({PONDER_GO_TEST_TIMEOUT_MINUTES:"60", PONDER_GO_TEST_BATCH_SIZE:"4"}).status, 1);
  const report = JSON.parse(fs.readFileSync(path.join(root, "parity/runs/go-test-execution.json")));
  const [broken, interrupted, healthy] = report.packages;
  assert.match(broken.discovery_error, /build error/);
  assert.equal(report.success, false);
  assert.equal(report.complete, false);
  assert.equal(report.batch_timeout, "60m");
  const failed = interrupted.batches[0];
  assert(failed.command.includes("-timeout=60m"));
  assert.deepEqual(failed.outcomes.not_started, ["TestB"]);
  assert.deepEqual(failed.outcomes.unfinished, ["TestA", "TestA/child"]);
  assert.deepEqual(healthy.batches[0].outcomes.listed_results, {pass:1, fail:0, skip:1});
  for (const [pkg, complete] of [["interrupted", false], ["red", true]]) {
    assert.equal(run({PONDER_GO_TEST_BATCH_SIZE:"4"}, "--package=" + pkg).status, 1);
    const single = JSON.parse(fs.readFileSync(path.join(root, "parity/runs/go-test-execution.json")));
    assert.equal(single.complete, complete);
    assert.equal(single.success, false);
  }
});

test("full verification executes independent checks after a failed native run or SDK suite", t => {
  const {root, bin, env, executable} = workspace(t, ["verify.sh"]);
  const stub = `
    const fs=require("node:fs"), path=require("node:path");
    const args=process.argv.slice(2), tool=path.basename(process.argv[1]);
    fs.appendFileSync(process.env.TRACE,JSON.stringify([tool,...args])+"\\n");
    if(args[0]==="tools/go-tests.mjs" && process.env.PONDER_RUN_DIR!==path.join(path.dirname(process.env.TRACE),"parity/runs"))process.exit(4);
    if(args[0]==="tools/sql-original.mjs"){
      const dir=args.find(x=>x.startsWith("--output=")).slice(9);
      fs.mkdirSync(dir,{recursive:true}); fs.writeFileSync(path.join(dir,"result.json"),"{}");
    }
    if(process.env.FAIL_STAGE==="native" && args[0]==="tools/go-tests.mjs" && !args.includes("--race"))process.exit(1);
    if(process.env.FAIL_STAGE==="sdk" && args.includes("--typecheck.only"))process.exit(2);
    if(process.env.FAIL_STAGE==="proof" && args[0]==="tools/config-live-results.mjs")process.exit(3);
  `;
  for (const name of ["go","node","anvil"]) executable(path.join(bin,name),stub);
  for (const base of ["../ponder-upstream/packages/core","sdk/typescript/react","sdk/typescript/client"]) {
    executable(path.join(root,base,"node_modules/.bin/vitest"),stub);
  }
  executable(path.join(root,"sdk/typescript/client/node_modules/.bin/tsup"),stub);
  for (const failure of ["native","sdk","proof","none"]) {
    const trace = path.join(root, "trace-" + failure);
    const captures = path.join(root, "parity/runs/original-captures");
    const earlier = new Set(fs.existsSync(captures) ? fs.readdirSync(captures) : []);
    const r = spawnSync("sh", ["tools/verify.sh"], {cwd:root,env:{...env,TRACE:trace,FAIL_STAGE:failure,
      PONDER_RUN_DIR:"previous-focused-run", PONDER_TEST_POSTGRES:"postgresql://127.0.0.1/test"},encoding:"utf8"});
    assert.equal(r.status, failure === "none" ? 0 : 1, r.stderr);
    const calls = fs.readFileSync(trace,"utf8").trim().split("\n").map(JSON.parse);
    assert(calls.some(args=>args.includes("tools/go-tests.mjs") && args.includes("--race")));
    assert(calls.some(args=>args.includes("--typecheck.only")));
    assert(calls.some(args=>args.includes("tools/differential.mjs")));
    assert.equal(calls.some(args=>args.includes("tools/compiler-cache-cutover.mjs")),failure !== "native");
    assert.equal(calls.some(args=>args.includes("tools/source-realtime-results.mjs")),["proof","none"].includes(failure));
    assert.equal(calls.some(args=>args.includes("tools/record-results.mjs")),failure === "none");
    const [current] = fs.readdirSync(captures).filter(name => !earlier.has(name));
    const rows = fs.readFileSync(path.join(captures, current, "verification.tsv"), "utf8").trim().split("\n");
    assert.equal(rows.shift(), "stage\tstatus");
    const status = Object.fromEntries(rows.map(row => row.split("\t")));
    assert(calls.some(([tool]) => tool === "tsup"));
    assert.equal(status["sdk-client-build"], "0");
    assert.equal(status["browser-original"], "0");
    assert.equal(status["native-normal"], failure === "native" ? "1" : "0");
    assert.equal(status["native-race"], "0");
    assert.equal(status["react-types"], failure === "sdk" ? "2" : "0");
    assert.equal(status["compiler-cache"], failure === "native" ? "normal-run-failed" : "0");
    if (failure === "proof") assert.equal(status["config-results"], "3");
    assert.equal(status["parity-results"], failure === "none" ? "0" : undefined);
  }
});
