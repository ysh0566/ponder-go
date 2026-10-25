// Actual TypeScript compiler audit of the six pinned runtime/index annotations.
// Original files are never rewritten. Each compiler program gets a fresh process.
// This is source-language obligation evidence, not Go or pristine-runtime credit.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";

const root = path.resolve(import.meta.dirname, "..");
const upstream = path.resolve(process.env.PONDER_UPSTREAM_ROOT ?? path.join(root, "../ponder-upstream"));
const sourceFile = "packages/core/src/runtime/index.test.ts";
const file = path.join(upstream, sourceFile);
const revision = "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334";
const sourceHash = "57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be";
const expectedLines = [309, 322, 564, 655, 703, 727];
const modes = ["pristine", "stripped", "explicit-key-union"];
const runs = path.join(root, "parity/runs");
const arg = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const sha = (value) => crypto.createHash("sha256").update(value).digest("hex");
const hash = (filename) => ({ file: path.relative(root, filename), sha256: sha(fs.readFileSync(filename)) });
const equal = (left, right) => JSON.stringify(left) === JSON.stringify(right);
const original = fs.readFileSync(file, "utf8");
const ledger = JSON.parse(fs.readFileSync(path.join(root, "parity/test-ledger.json"), "utf8"));
const cases = ledger.tests.filter((entry) => entry.source_file === sourceFile);
const selected = cases.filter((entry) => entry.type_error_directives.length);
const lines = [...original.matchAll(/@ts-expect-error/g)].map((match) => original.slice(0, match.index).split("\n").length);

if (ledger.baseline.revision !== revision || sha(original) !== sourceHash ||
    cases.length !== 15 || cases.reduce((sum, entry) => sum + entry.assertions.length, 0) !== 30 ||
    selected.length !== 4 || selected.reduce((sum, entry) => sum + entry.type_error_directives.length, 0) !== 6 ||
    !equal(lines, expectedLines) || cases.some((entry) => entry.source_sha256 !== sourceHash)) {
  throw new Error("Pinned runtime/index census or source hash changed; expected 15 cases, 30 assertions, and six annotations at the exact original lines.");
}

function pinnedRevision() {
  const result = spawnSync("git", ["rev-parse", "HEAD"], { cwd: upstream, encoding: "utf8" });
  if (result.status !== 0 || result.stdout.trim() !== revision) throw new Error("Upstream HEAD is not the pinned revision.");
  return result.stdout.trim();
}

function goObligationTreatment() {
  return {
    classification: "TypeScript heterogeneous Map constructor inference, not rejection of Factory cache keys",
    upstream_contract: "CachedIntervals = Map<Filter | Factory, { fragment: Fragment; intervals: Interval[] }[]>",
    source_language_obligations: 6,
    translated_go_negative_compile_tests: 0,
    source_credit: 0,
    current_go_representation: "store.SourceIntervalEntry.Key is filter.Object (map[string]any); both filter and factory objects are represented, with reference-identity lookup. This is not a closed static Filter | Factory contract.",
    semantic_obligation: "The same cache accepts both the exact filter object and its factory object, retaining object identity, fragment values, interval values, and insertion/replacement semantics. Verify these through the source-mapped runtime cases, separately from this compiler audit.",
    typed_boundary_proposal: "If a closed Go boundary is required, use a package-sealed CacheKey interface implemented by pointer-backed FilterKey and FactoryKey adapters, retaining each original object's identity. Keep the positive heterogeneous cache construction; do not introduce unrelated invalid Go values as substitutes for these six inference diagnostics.",
    go_static_contract_status: "not established by this audit",
    runtime_parity_status: "not established by this audit",
    inspected_files: ["filter/values.go", "store/source_intervals.go", "runtime/source_required_intervals.go", "runtime/source_sync_cache.go"]
      .map((relative) => path.join(root, relative)).filter(fs.existsSync).map(hash),
  };
}

if (process.argv.includes("--worker")) {
  const mode = arg("mode");
  const output = path.resolve(root, arg("output") ?? "");
  const relativeOutput = path.relative(runs, output);
  if (!modes.includes(mode) || relativeOutput.startsWith("..") || path.isAbsolute(relativeOutput) ||
      !/^runtime-index-types-[^/]+\/(pristine|stripped|explicit-key-union)\.json$/.test(relativeOutput)) {
    throw new Error("Explicit audit mode and a scoped parity/runs output are required.");
  }
  pinnedRevision();
  const require = createRequire(path.join(upstream, "packages/core/package.json"));
  const ts = require("typescript");
  const tree = ts.createSourceFile(file, original, ts.ScriptTarget.Latest, true);
  const maps = [];
  function visit(node) {
    if (ts.isNewExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "Map") {
      const line = tree.getLineAndCharacterOfPosition(node.getStart(tree)).line + 1;
      if (expectedLines.includes(line - 1)) maps.push(node);
    }
    ts.forEachChild(node, visit);
  }
  visit(tree);
  if (maps.length !== 6 || maps.some((node) => node.typeArguments?.length || node.arguments?.length !== 1 || !ts.isArrayLiteralExpression(node.arguments[0]))) {
    throw new Error("The six source obligations must remain unparameterized Map constructor expressions.");
  }
  const obligations = maps.map((node) => {
    const source = selected.find((entry) => node.getStart(tree) >= entry.start && node.end <= entry.end);
    if (!source || source.test_source !== original.slice(source.start, source.end)) throw new Error("Original source declaration does not match the ledger.");
    const directiveLine = tree.getLineAndCharacterOfPosition(node.getStart(tree)).line;
    return {
      source_id: source.id, title: source.title, directive_line: directiveLine,
      expression_line: directiveLine + 1, start: node.getStart(tree), end: node.end,
      constructor_sha256: sha(node.getText(tree)),
      key_expressions: node.arguments[0].elements.map((entry) => {
        if (!ts.isArrayLiteralExpression(entry) || entry.elements.length !== 2) throw new Error("Expected exact original key/value tuples.");
        return entry.elements[0].getText(tree);
      }),
    };
  });
  if (!equal(obligations.map((entry) => entry.directive_line), expectedLines) ||
      obligations.some((entry) => !equal(entry.key_expressions, entry.directive_line === 703 ? ["filter", "filter.address as Factory"] : ["filter", "filter.address"]))) {
    throw new Error("The six original heterogeneous filter/factory key pairs changed.");
  }

  // Equal-length replacement preserves original source positions in stripped mode.
  let virtual = mode === "pristine" ? original : original.replaceAll("@ts-expect-error", "@port-type-audit");
  if (virtual.length !== original.length) throw new Error("Annotation substitution changed source positions.");
  if (mode === "explicit-key-union") {
    // Supplemental positive witness only: the same key/value expressions compile
    // when the intended exported Map key/value types are explicit. Never persisted.
    for (const node of [...maps].reverse()) {
      const position = node.expression.end;
      virtual = virtual.slice(0, position) + "<Filter | Factory, { fragment: Fragment; intervals: Interval[] }[]>" + virtual.slice(position);
    }
  }
  const parsed = ts.getParsedCommandLineOfConfigFile(path.join(upstream, "packages/core/tsconfig.json"),
    { noEmit: true, incremental: false }, {
      ...ts.sys,
      onUnRecoverableConfigFileDiagnostic: (diagnostic) => { throw new Error(ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n")); },
    });
  if (!parsed) throw new Error("Could not read original compiler configuration.");
  const options = {
    ...parsed.options, rootDir: upstream,
    paths: { ...parsed.options.paths, "@ponder/utils": [path.join(upstream, "packages/utils/src/index.ts")] },
  };
  const host = ts.createCompilerHost(options);
  const readFile = host.readFile;
  host.readFile = (filename) => path.resolve(filename) === file ? virtual : readFile(filename);
  const program = ts.createProgram({ rootNames: [file], options, host });
  const diagnostics = [...parsed.errors, ...ts.getPreEmitDiagnostics(program)].map((diagnostic) => {
    const filename = diagnostic.file ? path.resolve(diagnostic.file.fileName) : null;
    const position = diagnostic.file && diagnostic.start !== undefined ? diagnostic.file.getLineAndCharacterOfPosition(diagnostic.start) : null;
    const obligation = filename === file ? obligations.find((entry) => entry.expression_line === position?.line + 1) : null;
    return {
      file: filename ? path.relative(root, filename) : null,
      start: diagnostic.start ?? null, length: diagnostic.length ?? null,
      line: position ? position.line + 1 : null, column: position ? position.character + 1 : null,
      category: ts.DiagnosticCategory[diagnostic.category], code: diagnostic.code,
      message: ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n"),
      source_id: obligation?.source_id ?? null, directive_line: obligation?.directive_line ?? null,
    };
  });
  const actual = diagnostics.filter((diagnostic) => diagnostic.file === path.relative(root, file));
  const other = diagnostics.filter((diagnostic) => diagnostic.file !== path.relative(root, file));
  const unchanged = sha(fs.readFileSync(file)) === sourceHash;
  const valid = unchanged && other.length === 0 && (mode === "stripped"
    ? actual.length === 6 && actual.every((diagnostic) => diagnostic.code === 2769 && diagnostic.source_id && diagnostic.message.startsWith("No overload matches this call.")) &&
      obligations.every((entry) => actual.filter((diagnostic) => diagnostic.directive_line === entry.directive_line && diagnostic.start >= entry.start && diagnostic.start < entry.end).length === 1)
    : actual.length === 0);
  const compilerSources = program.getSourceFiles()
    .map((source) => path.resolve(source.fileName))
    .filter((filename) => filename.startsWith(upstream + path.sep) && !filename.includes(path.sep + "node_modules" + path.sep));
  const provenance = [...new Set([
    import.meta.filename, file, path.join(upstream, "packages/core/tsconfig.json"),
    path.join(upstream, "packages/core/tsconfig.build.json"), path.join(upstream, "tsconfig.base.json"),
    path.join(upstream, "pnpm-lock.yaml"), require.resolve("typescript"), ...compilerSources,
  ])].sort().map(hash);
  const report = {
    complete: true, success: valid, mode, typescript: ts.version, upstream_revision: revision,
    source_file: sourceFile, source_sha256: sourceHash, original_source_unchanged: unchanged,
    source_credit: 0, obligations, actual, other, source_context: provenance,
    effective_options: {
      noEmit: options.noEmit, incremental: options.incremental, rootDir: options.rootDir,
      utils_source_entry: options.paths["@ponder/utils"][0],
      config: path.relative(root, path.join(upstream, "packages/core/tsconfig.json")),
    },
    method: mode === "explicit-key-union"
      ? "Supplemental positive witness: remove the six annotations and add explicit Filter | Factory and cache-value type arguments to those same Map constructors, in memory only."
      : "Original whole test-file compiler program and imports. Only the six expect-error comment tokens are replaced in stripped mode, without changing source positions; no original files, imports, or values are changed.",
  };
  fs.writeFileSync(output, JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify({ mode, actual: actual.length, other: other.length, success: valid }));
  if (!valid) process.exitCode = 1;
} else {
  pinnedRevision();
  fs.mkdirSync(runs, { recursive: true });
  const directory = path.join(runs, "runtime-index-types-" + new Date().toISOString().replaceAll(/[:.]/g, "-") + "-" + process.pid);
  fs.mkdirSync(directory);
  const executions = [];
  for (const mode of modes) {
    const output = path.join(directory, mode + ".json");
    const log = path.join(directory, mode + ".txt");
    const fd = fs.openSync(log, "w");
    let result;
    try {
      result = spawnSync(process.execPath, [import.meta.filename, "--worker", "--mode=" + mode, "--output=" + output], {
        cwd: root, env: process.env, stdio: ["ignore", fd, fd],
      });
    } finally {
      fs.closeSync(fd);
    }
    const report = fs.existsSync(output) ? JSON.parse(fs.readFileSync(output, "utf8")) : null;
    executions.push({ mode, exit_code: result.status, signal: result.signal, error: result.error?.message ?? null, report: report ? hash(output) : null, log: hash(log), result: report });
    console.log(JSON.stringify({ mode, exit_code: result.status, actual: report?.actual.length ?? null, other: report?.other.length ?? null, success: report?.success ?? false }));
    if (result.status !== 0) break;
  }
  const unchanged = sha(fs.readFileSync(file)) === sourceHash;
  const complete = unchanged && executions.length === modes.length && executions.every((entry) => entry.exit_code === 0 && entry.result?.complete && entry.result.success);
  const pristine = executions.find((entry) => entry.mode === "pristine")?.result;
  const stripped = executions.find((entry) => entry.mode === "stripped")?.result;
  const explicit = executions.find((entry) => entry.mode === "explicit-key-union")?.result;
  const report = {
    complete, success: complete, upstream_revision: pinnedRevision(), source_file: sourceFile,
    source_sha256: sourceHash, original_source_unchanged: unchanged, source_credit: 0,
    source_census: { cases: 15, assertions: 30, type_error_directives: 6, cases_with_directives: 4 },
    pristine_diagnostics: pristine?.actual.length ?? null,
    stripped_diagnostics: stripped?.actual.length ?? null,
    explicit_key_union_diagnostics: explicit?.actual.length ?? null,
    obligations: stripped?.obligations ?? pristine?.obligations ?? [],
    go_obligation_treatment: goObligationTreatment(), executions,
  };
  fs.writeFileSync(path.join(runs, "source-runtime-index-types.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify({ complete, pristine: report.pristine_diagnostics, stripped: report.stripped_diagnostics, explicit_key_union: report.explicit_key_union_diagnostics, source_credit: 0 }));
  if (!complete) process.exitCode = 1;
}
