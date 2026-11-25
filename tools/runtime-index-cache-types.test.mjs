import assert from "node:assert/strict";
import test from "node:test";
import { verifiedCacheTypeLines } from "./runtime-index-cache-types.mjs";

test("interval cache diagnostics are read from the current compiler reports", () => {
  const source = "packages/core/src/runtime/index.test.ts";
  const metadata = {
    complete: true, success: true, upstream_revision: "pinned", original_source_unchanged: true,
    source_file: source, source_sha256: "original",
  };
  const modes = ["pristine", "stripped", "explicit-key-union"];
  const locations = [309, 322, 564, 655, 703, 727];
  const proof = {
    ...metadata,
    executions: modes.map(mode => ({ mode, exit_code: 0, report: { file: mode, sha256: mode } })),
  };
  const workers = Object.fromEntries(modes.map(mode => [mode, {
    ...metadata, mode, other: [], source_context: [
      { file: "tools/source-runtime-index-types.mjs", sha256: "tool" },
      { file: "../pinned-oracle/" + source, sha256: "original" },
    ],
    actual: mode === "stripped"
      ? locations.map(line => ({ code: 2769, source_id: "case", directive_line: line }))
      : [],
  }]));
  const hashes = new Map([...modes.map(mode => [mode, mode]), ...workers.pristine.source_context.map(ref => [ref.file, ref.sha256])]);
  const check = (meta, reports) => verifiedCacheTypeLines(meta, "pinned", ref => hashes.get(ref.file) === ref.sha256, file => reports[file]);
  assert.deepEqual(check(proof, workers).get("case"), locations);
  for (const mutate of [
    meta => meta.executions.pop(),
    meta => { meta.executions[0].exit_code = 1; },
    meta => { meta.executions[1].report.sha256 = "stale"; },
    (_, reports) => { reports.stripped.actual[0].code = 2339; },
    (_, reports) => { reports.pristine.source_context.pop(); },
    (_, reports) => { reports.stripped.source_sha256 = "different"; },
    (_, reports) => { reports["explicit-key-union"].other = [{ code: 2322 }]; },
  ]) {
    const meta = structuredClone(proof), reports = structuredClone(workers);
    mutate(meta, reports);
    assert.equal(check(meta, reports), null);
  }
});
