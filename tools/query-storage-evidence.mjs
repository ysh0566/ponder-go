const targets = new Map([
  ["packages/core/src/graphql/index.test.ts", "graphql/source_test.go"],
  ["packages/core/src/utils/copy.test.ts", "store/row_copy_source_test.go"],
  ["packages/core/src/types/db.test-d.ts", "orm/db_types_source_test.go"],
]);

export function hasQueryStorageDirectives(test) {
  const target = targets.get(test.source_file);
  return target !== undefined && test.mapping?.target_file === target && test.type_error_directives.length > 0;
}

export function verifiedQueryStorageDirectives(test, { proof, toolHash, revision, executions, normal, race }) {
  if (!hasQueryStorageDirectives(test) || !proof || proof.revision !== revision ||
      proof.tool_sha256 !== toolHash || proof.files?.[test.source_file] !== test.source_sha256 ||
      !proof.native_inputs_sha256 || executions.length !== 2 ||
      !executions.every(run => run?.complete && run.success && run.source_fingerprint?.sha256 === proof.native_inputs_sha256 &&
        run.finished_source_fingerprint?.sha256 === proof.native_inputs_sha256)) return false;

  const passed = name => [normal, race].every(results =>
    results.get(test.mapping.go_package + ":" + name)?.Action === "pass");
  const name = test.mapping.go_test;
  if (!passed(name)) return false;
  const graph = test.mapping.target_file === "graphql/source_test.go";
  if (graph) {
    const companion = name.replace("TestSourceGraphQLTablePages/postgres/", "TestSourceGraphQLTablePages/pglite/");
    if (name === companion || !passed(companion)) return false;
  }
  const lines = test.test_source.split("\n");
  const expected = lines.flatMap((line, i) => line.includes("@ts-expect-error") ? [test.line + i + 1] : []);
  const sites = proof.checks?.filter(site => site.id === test.id) ?? [];
  if (expected.length !== test.type_error_directives.length || sites.length !== expected.length) return false;
  return sites.every((site, i) => {
    if (site.line !== expected[i] || !Array.isArray(site.typescript) || !site.typescript.length ||
        !site.typescript.every(code => Number.isSafeInteger(code) && code > 0)) return false;
    if (!graph) return true;
    const fields = lines[site.line - test.line].match(/\bresult\.data((?:\.\w+)+)/)?.[1];
    return site.native === "direct field access rejected" && Array.isArray(site.fields) &&
      fields === "." + site.fields.join(".") && site.typescript.every(code => [18046, 18048, 18049].includes(code));
  });
}
