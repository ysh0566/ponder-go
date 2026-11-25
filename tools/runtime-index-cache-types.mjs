// These compiler controls exercise the single key relationship shared by the
// six upstream Map expressions. The runtime cases separately replay their data.
export const cacheTypePackage = "ponder.local/ponder-go/store";
export const cacheTypeTests = [
  "TestSourceIntervalMapTypes/union",
  "TestSourceIntervalMapTypes/inferred",
  "TestSourceIntervalMapTypes/outside_union",
  "TestSourceIntervalMapIdentity",
];

export function verifyCacheTypeTests(passed) {
  for (const test of cacheTypeTests) {
    if (!passed(cacheTypePackage, test)) throw Error("Missing interval cache control: " + test);
  }
}

export function cacheTypeMapping() {
  return {
    source_verification: "parity/runs/typed-interval-cache/results.json",
    native_type_package: cacheTypePackage,
    native_type_tests: cacheTypeTests,
    assertion_policy: "Replay every source assertion against the filter/factory cache on both backends. Compile an explicit heterogeneous cache, check the inferred homogeneous constructor diagnostic, and verify key identity, order and replacement. All six TypeScript Map diagnostics share this native type relationship.",
  };
}

// Recover the diagnostic locations from the compiler worker reports themselves.
export function verifiedCacheTypeLines(proof, revision, validFile, readFile) {
  if (!proof?.complete || !proof.success || proof.upstream_revision !== revision ||
      !proof.original_source_unchanged || proof.source_file !== "packages/core/src/runtime/index.test.ts" ||
      proof.executions?.length !== 3) return null;

  const modes = ["pristine", "stripped", "explicit-key-union"];
  const lines = new Map();
  for (const [i, execution] of proof.executions.entries()) {
    if (execution.mode !== modes[i] || execution.exit_code !== 0 || !validFile(execution.report)) return null;
    const result = readFile(execution.report.file);
    if (!result.complete || !result.success || result.mode !== modes[i] ||
        result.upstream_revision !== revision || result.source_sha256 !== proof.source_sha256 ||
        result.source_file !== proof.source_file || !result.original_source_unchanged ||
        !result.source_context?.some(ref => ref.file === "tools/source-runtime-index-types.mjs") ||
        !result.source_context.some(ref => ref.file.endsWith("/" + proof.source_file) && ref.sha256 === proof.source_sha256) ||
        !result.source_context.every(validFile) || result.other?.length !== 0 ||
        result.actual?.length !== (result.mode === "stripped" ? 6 : 0)) return null;
    for (const diagnostic of result.actual) {
      if (diagnostic.code !== 2769 || !diagnostic.source_id || !Number.isSafeInteger(diagnostic.directive_line)) return null;
      const sites = lines.get(diagnostic.source_id) ?? [];
      sites.push(diagnostic.directive_line);
      lines.set(diagnostic.source_id, sites);
    }
  }
  return lines;
}
