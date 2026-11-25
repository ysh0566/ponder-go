import assert from "node:assert/strict";
import test from "node:test";
import { verifiedQueryStorageDirectives } from "./query-storage-evidence.mjs";

test("GraphQL type evidence binds the original access, compiler, native inputs and both backends in each mode", () => {
  const file = "packages/core/src/graphql/index.test.ts";
  const name = "TestSourceGraphQLTablePages/postgres/limit";
  const companion = name.replace("/postgres/", "/pglite/");
  const source = {id: "limit", line: 40, source_file: file, source_sha256: "source",
    test_source: "// @ts-expect-error\nexpect(result.data.persons.items)", type_error_directives: ["// @ts-expect-error"],
    mapping: {target_file: "graphql/source_test.go", go_package: "p/graphql", go_test: name}};
  const modes = new Map([name, companion].map(n => ["p/graphql:" + n, {Action: "pass"}]));
  const run = {complete: true, success: true, source_fingerprint: {sha256: "snapshot"}, finished_source_fingerprint: {sha256: "snapshot"}};
  const inputs = {toolHash: "producer", revision: "upstream", normal: modes, race: new Map(modes), executions: [run, structuredClone(run)],
    proof: {revision: "upstream", files: {[file]: "source"}, tool_sha256: "producer", native_inputs_sha256: "snapshot",
      checks: [{id: "limit", line: 41, typescript: [18046], fields: ["persons", "items"], native: "direct field access rejected"}]}};
  assert(verifiedQueryStorageDirectives(source, inputs));
  for (const mutate of [
    x => x.proof.checks.pop(), x => x.proof.checks[0].line++,
    x => x.proof.checks[0].fields[0] = "other", x => x.proof.checks[0].typescript = [],
    x => x.proof.checks[0].native = "passed", x => x.proof.files[file] = "different",
    x => x.proof.tool_sha256 = "other", x => x.proof.revision = "older",
    x => x.executions[1].finished_source_fingerprint.sha256 = "changed",
    x => x.executions[0].success = false, x => x.race.delete("p/graphql:" + companion),
    x => x.normal.set("p/graphql:" + name, {Action: "skip"}),
  ]) {
    const invalid = structuredClone(inputs);
    mutate(invalid);
    assert.equal(verifiedQueryStorageDirectives(source, invalid), false);
  }
});
