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
