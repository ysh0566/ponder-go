import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import crypto from "node:crypto";
import { createRequire } from "node:module";
const root = path.resolve(import.meta.dirname, ".."), upstream = path.resolve(root, "../ponder-upstream");
const require = createRequire(path.join(upstream, "packages/core/package.json"));
const ts = require("typescript"), viem = require("viem");
const file = "packages/core/src/utils/decodeAbiParameters.test.ts";
const source = fs.readFileSync(path.join(upstream, file), "utf8");
const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
const hash = crypto.createHash("sha256").update(source).digest("hex");
const ledger = JSON.parse(fs.readFileSync(path.join(root, "parity/test-ledger.json")));
const cases = ledger.tests.filter(x => x.source_file === file);
if (cases.some(x => x.source_sha256 !== hash)) throw Error("Stale source ledger");
function tree(n) {
  if (ts.isTypeOperatorNode(n) || ts.isParenthesizedTypeNode(n)) return tree(n.type);
  if (ts.isTupleTypeNode(n)) return { kind: "tuple", elements: n.elements.map(tree) };
  if (ts.isArrayTypeNode(n)) return { kind: "array", element: tree(n.elementType) };
  if (ts.isTypeLiteralNode(n)) return { kind: "object", fields: n.members.map(x => {
    if (!ts.isPropertySignature(x) || !x.type || x.questionToken) throw Error("Unsupported type member");
    return { name: x.name.getText(sf), type: tree(x.type) };
  }) };
  if ([ts.SyntaxKind.BigIntKeyword, ts.SyntaxKind.NumberKeyword, ts.SyntaxKind.BooleanKeyword, ts.SyntaxKind.StringKeyword].includes(n.kind) || ts.isTypeReferenceNode(n))
    return { kind: "scalar", type: n.getText(sf) };
  throw Error("Unresolved type obligation: " + n.getText(sf));
}
const F = ts.factory;
function literal(v) { return F.createCallExpression(F.createPropertyAccessExpression(F.createIdentifier("JSON"), "parse"), undefined, [F.createStringLiteral(JSON.stringify(v))]); }
const transformed = ts.transform(sf, [ctx => function visit(n) {
  if (ts.isImportDeclaration(n)) return undefined;
  if (ts.isCallExpression(n) && n.expression.getText(sf) === "expectTypeOf") {
    if (n.typeArguments?.length !== 1) throw Error("Unresolved expectTypeOf");
    return F.createCallExpression(F.createIdentifier("captureType"), undefined, [n.arguments[0], literal({
      text: n.typeArguments[0].getText(sf), tree: tree(n.typeArguments[0]), line: sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1,
    })]);
  }
  if (ts.isCallExpression(n) && ["test", "test.skip"].includes(n.expression.getText(sf))) {
    return F.updateCallExpression(n, n.expression, n.typeArguments, [...n.arguments.map(a => ts.visitNode(a, visit)), F.createNumericLiteral(n.getStart(sf))]);
  }
  return ts.visitEachChild(n, visit, ctx);
}]).transformed[0];
const code = ts.transpileModule(ts.createPrinter().printFile(transformed), {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText;
const tagged = value => JSON.parse(JSON.stringify(value, (_, v) => typeof v === "bigint" ? {$bigint:v.toString()} : v));
const output = [], suites = [];
let current;
function input(actual) {
  const v = typeof actual === "function" ? actual() : actual;
  if (!v?.input) throw Error("Unexpected expression in " + current.full_name);
  return tagged(v.input);
}
function run(name, fn, start, skip = false) {
  const original = cases.find(x => x.start === start);
  if (!original || original.full_name !== [...suites,name].join(" > ")) throw Error("Source identity mismatch");
  current = {id:original.id, source_file:file, source_sha256:hash, line:original.line, title:name, full_name:original.full_name, source_state:original.source_state, assertions:[]};
  if (skip) {
    current.retained_source_assertions = original.assertions;
    current.execution_status = "source_skip_not_executed";
  } else {
    const result = fn();
    if (result?.then) throw Error("Async fixture not supported");
    if (current.assertions.length !== original.assertions.length) throw Error("Lost assertions: " + current.full_name);
  }
  output.push(current); current = null;
}
const test = (name, fn, start) => run(name,fn,start);
test.skip = (name,fn,start) => run(name,fn,start,true);
const lower = value => value.toLowerCase();
vm.runInNewContext(code, {
  JSON, encodeAbiParameters: viem.encodeAbiParameters, getAbiItem: viem.getAbiItem, parseAbiParameters:viem.parseAbiParameters, zeroAddress:viem.zeroAddress,
  toLowerCase:lower, decodeAbiParameters:(params,data,options) => ({input:{params,data,format_address: options?.formatAddress === lower ? "lowercase" : "checksum"}}),
  describe:(name,fn) => {suites.push(name);try{fn();}finally{suites.pop();}}, test,
  captureType:(actual,type) => current.assertions.push({matcher:"expectTypeOf", input:input(actual), expected_type:type}),
  expect:actual => Object.fromEntries(["toEqual","toMatchInlineSnapshot","toThrowError","toThrow"].map(matcher => [matcher,expected => {
    const a = {input:input(actual),matcher};
    if (matcher.startsWith("toThrow")) a.error = Object.prototype.toString.call(expected) === "[object RegExp]" ? {regex:expected.source,flags:expected.flags} : {contains:expected ?? null};
    else if (matcher === "toMatchInlineSnapshot") {a.raw_snapshot=expected;a.expected=tagged(vm.runInNewContext("("+expected+")",{}));}
    else a.expected=tagged(expected);
    current.assertions.push(a);
  }]))
}, {filename:file,timeout:10000});
if (output.length !== cases.length) throw Error("Source case count mismatch");
fs.writeFileSync(path.join(root,"testdata/abi/parameter-cases.json"),JSON.stringify(output,null,2)+"\n");
console.log({source_cases:output.length,active:output.filter(x=>x.source_state==="active").length,source_skipped:output.filter(x=>x.source_state==="skip").length, runtime_assertions:output.flatMap(x=>x.assertions).filter(x=>x.matcher!=="expectTypeOf").length,inline_type_obligations:output.flatMap(x=>x.assertions).filter(x=>x.matcher==="expectTypeOf").length,method:"Pinned source callbacks; symbolic decode calls; expected values and type trees come only from source assertions. Source skips retained without execution or pass credit."});
