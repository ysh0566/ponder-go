// Capture the operands of the original bidirectional ABI type assertions.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const peer = path.resolve(process.env.PONDER_UPSTREAM ?? path.join(root, "../ponder-upstream"));
const core = path.join(peer, "packages/core");
const require = createRequire(path.join(core, "package.json"));
const ts = require("typescript");
const { parseAbiItem } = require("abitype");
const file = path.join(core, "src/config/utilityTypes.test-d.ts");
const config = ts.readConfigFile(path.join(core, "tsconfig.json"), ts.sys.readFile);
const project = ts.parseJsonConfigFileContent(config.config, ts.sys, core);
const program = ts.createProgram([file], { ...project.options, noEmit: true, skipLibCheck: true });
const tree = program.getSourceFile(file);
const diagnostics = [...program.getSyntacticDiagnostics(tree), ...program.getSemanticDiagnostics(tree)];
if (diagnostics.length) throw Error(diagnostics.map(d => ts.flattenDiagnosticMessageText(d.messageText,"\n")).join("\n"));
const checker = program.getTypeChecker();
const declarations = {};
const cases = [];

function operands(type) {
  if (ts.isTypeOperatorNode(type) || ts.isParenthesizedTypeNode(type)) return operands(type.type);
  if (ts.isTupleTypeNode(type)) return type.elements.flatMap(operands);
  if (ts.isArrayTypeNode(type)) return operands(type.elementType);
  if (ts.isUnionTypeNode(type)) return type.types.flatMap(operands);
  if (ts.isLiteralTypeNode(type)) return [type.literal.text];
  return [type.getText(tree)];
}

for (const node of tree.statements) {
  if (ts.isTypeAliasDeclaration(node)) {
    const spec = node.type.typeArguments[0].literal.text;
    declarations[node.name.text] = parseAbiItem(spec);
    continue;
  }
  if (!ts.isExpressionStatement(node) || !ts.isCallExpression(node.expression)) continue;
  const [name, body] = node.expression.arguments;
  const [alias, ...statements] = body.body.statements;
  const calls = statements.map(s => s.expression);
  if (calls.length !== 2 || calls.some(c => c.expression.getText(tree) !== "assertType")) throw Error(name.text);
  const expected = calls[0].typeArguments[0];
  if (calls[0].arguments[0].type.getText(tree) !== alias.name.text ||
      calls[1].typeArguments[0].getText(tree) !== alias.name.text ||
      JSON.stringify(operands(calls[1].arguments[0].type)) !== JSON.stringify(operands(expected))) throw Error("Changed assertion directions in " + name.text);
  for (const call of calls) {
    const from = checker.getTypeAtLocation(call.arguments[0]);
    const to = checker.getTypeFromTypeNode(call.typeArguments[0]);
    if ((from.flags | to.flags) & ts.TypeFlags.Any || !checker.isTypeAssignableTo(from, to)) throw Error("Unverified " + name.text);
  }
  const [input, argument] = alias.type.typeArguments;
  const layout = input.getText(tree) === "Abi" ? "weak" : input.getText(tree).includes("[]") ? "array" : "tuple";
  cases.push({name: name.text, operation: alias.type.typeName.text, layout, items: layout === "weak" ? [] : operands(input),
    argument: argument ? operands(argument)[0] : "", expected: operands(expected)});
}
if (Object.keys(declarations).length !== 6 || cases.length !== 34) throw Error("Changed original ABI type inventory");
const ledger = JSON.parse(fs.readFileSync(path.join(root,"parity/test-ledger.json"),"utf8"));
const revision = execFileSync("git",["rev-parse","HEAD"],{cwd:peer,encoding:"utf8"}).trim();
const hash = createHash("sha256").update(tree.text).digest("hex");
const records = ledger.tests.filter(t => t.source_file === "packages/core/src/config/utilityTypes.test-d.ts");
if (revision !== ledger.baseline.revision || records.length !== cases.length ||
    records.some((t,i) => t.source_sha256 !== hash || t.title !== cases[i].name || t.assertions.length !== 2)) {
  throw Error("Pinned ABI source inventory changed");
}
const data = {source_sha256: hash, declarations, cases};
const output = path.join(root, "abi/testdata/catalog-utilities.json");
const serialized = JSON.stringify(data,null,2) + "\n";
if (process.argv.includes("--write")) fs.writeFileSync(output, serialized);
else if (fs.readFileSync(output,"utf8") !== serialized) throw Error("Go ABI test inputs differ from the original compiler program");
console.log(JSON.stringify({typescript: ts.version, cases: cases.length, assertions: cases.length * 2, source_sha256: data.source_sha256}));
