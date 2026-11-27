const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { createRequire } = require("node:module");
const upstream = createRequire(path.resolve("package.json"));
const ts = upstream("typescript");
const { MetricsService } = upstream("./src/internal/metrics.ts");
const { buildOptions } = upstream("./src/internal/options.ts");

async function compare(command, database, ordering) {
  const filename = path.resolve("src/bin/commands", command + ".ts");
  const file = ts.createSourceFile(
    filename,
    fs.readFileSync(filename, "utf8"),
    ts.ScriptTarget.Latest,
    true,
  );
  const metrics = new MetricsService();
  const cliOptions = {
    command,
    version: "0.18.2-go.0",
    config: "ponder.config.ts",
  };
  const options = buildOptions({ cliOptions });
  const preCompileResult = {
    result: { ordering, databaseConfig: { kind: database } },
  };
  const calls = [];
  function visit(node) {
    if (
      ts.isCallExpression(node) &&
      [
        "metrics.ponder_version_info.set",
        "metrics.ponder_settings_info.set",
      ].includes(node.expression.getText(file))
    ) {
      calls.push(node.getText(file));
    }
    ts.forEachChild(node, visit);
  }
  visit(file);
  assert.equal(calls.length, 2);
  for (const call of calls)
    vm.runInNewContext(call, {
      metrics,
      options,
      preCompileResult,
      cliOptions,
    });
  const values = (await metrics.getMetrics())
    .split("\n")
    .filter((line) => /^ponder_(settings|version)_info\{/.test(line));
  assert.equal(values.length, 2);
  const result = Object.fromEntries(
    values.map((line) => {
      const match = line.match(/^(ponder_\w+)\{(.*)\} 1$/);
      assert.ok(match, line);
      return [
        match[1],
        Object.fromEntries(
          [...match[2].matchAll(/(\w+)="([^"]*)"/g)].map((m) => [m[1], m[2]]),
        ),
      ];
    }),
  );
  assert.deepEqual(result.ponder_version_info, {
    version: "0.18.2-go.0",
    major: "0",
    minor: "18",
    patch: "2",
  });
  assert.deepEqual(
    result.ponder_settings_info,
    command === "serve"
      ? { database, command }
      : { ordering, database, command },
  );
  metrics.resetIndexingMetrics();
  const after = await metrics.getMetrics();
  assert.match(after, /^ponder_version_info\{/m);
  assert.doesNotMatch(after, /^ponder_settings_info\{/m);
  console.log(command, database, ordering, JSON.stringify(result));
}
(async () => {
  for (const command of ["dev", "start"]) {
    for (const database of ["postgres", "pglite"]) {
      for (const ordering of [
        "multichain",
        "experimental_isolated",
        "omnichain",
      ])
        await compare(command, database, ordering);
    }
  }
  await compare("serve", "postgres", "multichain");
})().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
