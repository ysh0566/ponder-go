// Check diagnostics suppressed by the GraphQL, row-copy and typed database tests.
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import { sourceFingerprint } from "./go-source-fingerprint.mjs";

const root = path.resolve(import.meta.dirname, "..");
const peer = path.resolve(process.env.PONDER_UPSTREAM ?? path.join(root, "../ponder-upstream"));
const core = path.join(peer, "packages/core");
const ts = createRequire(path.join(core, "package.json"))("typescript");
const ledger = JSON.parse(fs.readFileSync(path.join(root, "parity/test-ledger.json")));
const revision = execFileSync("git", ["-C", peer, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
assert.equal(revision, ledger.baseline.revision);

const graphql = "packages/core/src/graphql/index.test.ts";
const copies = "packages/core/src/utils/copy.test.ts";
const database = "packages/core/src/types/db.test-d.ts";
const config = ts.readConfigFile(path.join(core, "tsconfig.json"), ts.sys.readFile);
const project = ts.parseJsonConfigFileContent(config.config, ts.sys, core);
const options = { ...project.options, noEmit: true, skipLibCheck: true, incremental: false, composite: false };
const files = {};
const checks = [];

function checkTypeScript(source) {
  const file = path.join(peer, source);
  const text = fs.readFileSync(file, "utf8");
  files[source] = createHash("sha256").update(text).digest("hex");
  const records = ledger.tests.filter(test => test.source_file === source && test.type_error_directives.length);
  const sites = [];
  for (const test of records) {
    assert.equal(test.source_sha256, files[source]);
    assert(text.includes(test.test_source));
    const lines = test.test_source.split("\n");
    const before = sites.length;
    for (const [i, line] of lines.entries()) {
      if (!line.includes("@ts-expect-error")) continue;
      const site = { id: test.id, line: test.line + i + 1 };
      if (source === graphql) {
        const access = lines[i + 1].match(/\bresult\.data((?:\.\w+)+)/);
        assert(access, `Unexpected GraphQL access at ${site.line}`);
        site.fields = access[1].slice(1).split(".");
      }
      sites.push(site);
    }
    assert.equal(sites.length - before, test.type_error_directives.length);
  }

  const roots = [file, ...project.fileNames.filter(f => f.endsWith(".d.ts") && !f.endsWith("test-d.ts"))];
  function diagnostics(remove) {
    const host = ts.createCompilerHost(options);
    const readFile = host.readFile;
    if (remove) host.readFile = name => name === file
      ? text.replace(/\/\/ @ts-expect-error[^\n]*/g, match => " ".repeat(match.length))
      : readFile(name);
    const program = ts.createProgram(roots, options, host);
    const tree = program.getSourceFile(file);
    return [...program.getSyntacticDiagnostics(tree), ...program.getSemanticDiagnostics(tree)].map(d => ({
      line: tree.getLineAndCharacterOfPosition(d.start).line + 1,
      code: d.code,
      message: ts.flattenDiagnosticMessageText(d.messageText, "\n"),
    }));
  }
  assert.deepEqual(diagnostics(false), []);
  const removed = diagnostics(true);
  assert.deepEqual([...new Set(removed.map(d => d.line))], sites.map(site => site.line));
  if (source === graphql) {
    assert.equal(sites.length, 11);
    assert(removed.every(d => [18046, 18048, 18049].includes(d.code) && d.message.includes("result.data")));
  } else {
    const expected = source === copies ? [2339, 2339, 2339, 2345]
      : [6196, 2322, 6133, 2322, 6133, 6133, 6133, 6133, 6133, 2322, 6133, 2322, 6133, 2345];
    assert.deepEqual(removed.map(d => d.code), expected);
  }
  for (const site of sites) {
    site.typescript = removed.filter(d => d.line === site.line).map(d => d.code);
  }
  return sites;
}

function checkGraphQLSelectors(sites) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "ponder-graphql-types-"));
  const probe = path.join(directory, "check.go");
  const header = `package main
import (
    "context"
    gql "ponder.local/ponder-go/graphql"
)
func main() {}
func check(server *gql.Server) {
    result := server.Execute(context.Background(), gql.Request{})
`;
  try {
    const paths = sites.map(site => "{" + site.fields.map(f => JSON.stringify(f)).join(", ") + "}");
    fs.writeFileSync(probe, header + `
    fields, ok := result.Data.(map[string]any)
    if !ok { return }
    for _, keys := range [][]string{${paths.join(", ")}} {
        var value any = fields
        for _, key := range keys {
            object, ok := value.(map[string]any)
            if !ok { return }
            value = object[key]
        }
    }
}
`);
    const args = ["build", "-gcflags=-e", "-o", path.join(directory, "check"), probe];
    execFileSync("go", args, { cwd: root, stdio: "pipe" });

    const selectors = sites.map(site => site.fields.map(f => f[0].toUpperCase() + f.slice(1)).join("."));
    fs.writeFileSync(probe, header + selectors.map(s => "    _ = result.Data." + s).join("\n") + "\n}\n");
    const rejected = spawnSync("go", args, { cwd: root, encoding: "utf8" });
    assert.equal(rejected.error, undefined);
    assert.equal(rejected.status, 1, rejected.stderr);
    const errors = rejected.stderr.split("\n").filter(line => line.includes("check.go:"));
    assert.equal(errors.length, sites.length, rejected.stderr);
    const firstLine = header.split("\n").length;
    for (const [i, site] of sites.entries()) {
      const first = selectors[i].split(".")[0];
      assert(errors[i].includes(`check.go:${firstLine + i}:`), errors[i]);
      assert(errors[i].includes(`result.Data.${first} undefined`) && errors[i].includes(`no field or method ${first}`), errors[i]);
      site.native = "direct field access rejected";
    }
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
}

for (const source of [graphql, copies, database]) {
  const sites = checkTypeScript(source);
  if (source === graphql) checkGraphQLSelectors(sites);
  checks.push(...sites);
}
const tool = createHash("sha256").update(fs.readFileSync(import.meta.filename)).digest("hex");
console.log(JSON.stringify({
  revision, files, typescript: ts.version, tool_sha256: tool,
  native_inputs_sha256: sourceFingerprint(root).sha256, checks,
}, null, 2));
