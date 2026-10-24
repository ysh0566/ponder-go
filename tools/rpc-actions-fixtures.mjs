import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import vm from "node:vm";
import { createRequire } from "node:module";

const root = path.resolve(import.meta.dirname, ".."), upstream = path.resolve(root, "../ponder-upstream");
const require = createRequire(path.join(upstream, "packages/core/package.json"));
const ts = require("typescript"), viem = require("viem"), sources = [];
function load(file, dependencies = {}, names) {
 let source = fs.readFileSync(path.join(upstream, file), "utf8");
 sources.push({ file, sha256: crypto.createHash("sha256").update(source).digest("hex") });
 if (names) {
  const ast = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
  source = ast.statements.filter(n => ts.isVariableStatement(n) && n.declarationList.declarations.some(d => names.includes(d.name.getText(ast)))).map(n => n.getText(ast)).join("\n");
 }
 const exports = {};
 vm.runInNewContext(ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText, { exports, require(name) { if (!(name in dependencies)) throw Error("Unexpected import " + name); return dependencies[name]; } }, { filename: file, timeout: 5000 });
 return exports;
}
const helper = load("packages/utils/src/getLogsRetryHelper.ts", { viem });
const errors = load("packages/core/src/internal/errors.ts");
const bloom = load("packages/core/src/sync-realtime/bloom.ts", {}, ["zeroLogsBloom"]);
const pg = load("packages/core/src/utils/pg.ts", {}, ["PG_BIGINT_MAX", "PG_INTEGER_MAX"]);
const chunks = load("packages/core/src/utils/chunk.ts");
const actions = load("packages/core/src/rpc/actions.ts", { viem, "@ponder/utils": helper, "@/internal/errors.js": errors, "@/sync-realtime/bloom.js": bloom, "@/utils/chunk.js": chunks, "@/utils/pg.js": pg });
const cases = [];
function add(name, operation, input, fn) {
 const before = structuredClone(input);
 try { cases.push({ name, operation, input: before, result: fn(input) ?? null }); }
 catch (error) { cases.push({ name, operation, input: before, error: { name: error.name, message: error.message, meta: error.meta ?? [] } }); }
}
const messages = [
 "requested too many blocks from 0 to 20000, maximum is set to 20", "Max range: 20",
 "Maximum allowed number of requested blocks is 20", "Try with this block range [0xa, 0x1e]",
 "block range is too wide", "this block range should work: [0xa, 0x1e]",
 "limited to a 1,000", "limited to 1.000 block", "exceeds the range allowed for your plan (20000 > 1000)",
 "Block range limit exceeded.", "please limit the query to at most 1,000 blocks", "maximum block range: 1,000",
 "query exceeds max block range 1000", "block range greater than 1000 max", "block range exceeds 1000",
 "eth_getLogs range is too large, max is 10k blocks", "eth_getLogs range is too large, max is 1k blocks",
 "query must be smaller than size 1000", "block range is too wide (maximum 1000)", "up to a 1000 block range",
 "maximum [from, to] blocks distance: 1000", "exceed max block range: 1000",
 "allowed block range threshold exceeded", "exceeded max allowed", "range threshold exceeded",
 "no backend is currently healthy to serve traffic", "block range too large", "Block range is too large",
 "backend response too large", "query exceeds max results", "logs matched by query exceeds limit of 20",
 "query returned more than 20 results", "response size should not greater than 20 bytes",
 "ranges over 20 blocks are not supported on freetier", "unrecognized provider message",
];
for (const [index, message] of messages.entries()) {
 const query = { fromBlock: "0x1", toBlock: "0x4e20" }, error = { code: -32600, message };
 add(`retry/provider_${index}`, "retry", { query, error }, ({ query, error }) => helper.getLogsRetryHelper({ params: [query], error }));
}
for (const [name, query, error] of [
 ["single_exhausted", { fromBlock: "0x1", toBlock: "0x1" }, { message: "query exceeds max results" }],
 ["unchanged_hint", { fromBlock: "0x1", toBlock: "0xa" }, { message: "Max range: 10" }],
 ["reversed", { fromBlock: "0xa", toBlock: "0x1" }, { message: "Max range: 10" }],
 ["noncanonical_hex", { fromBlock: "0x01", toBlock: "0x0a" }, { message: "Max range: 10" }],
 ["code_sensitive", { fromBlock: "0x1", toBlock: "0x4e20" }, { message: "block range is too wide", code: -32000 }],
 ["chainstack_free", { fromBlock: "0x1", toBlock: "0x3e8" }, { message: "Block range limit exceeded." }],
 ["big_integer", { fromBlock: "0x100000000000000000001", toBlock: "0x10000000000000000001f" }, { message: "Max range: 10" }],
 ["nested_error", { fromBlock: "0x1", toBlock: "0x1f" }, { cause: { message: "Max range: 10" } }],
 ["suggestion_then_fallback", { fromBlock: "0x1", toBlock: "0xa" }, { message: "Max range: 10; query exceeds max results" }],
]) add(`retry/${name}`, "retry", { query, error }, ({ query, error }) => helper.getLogsRetryHelper({ params: [query], error }));
const hash = "0x" + "11".repeat(32), address = "0x" + "22".repeat(20);
const request = { method: "eth_getBlockReceipts", params: [hash] };
const receipt = { blockHash: hash, blockNumber: "0x1", transactionHash: hash, transactionIndex: "0x0", from: address, status: "0x1" };
const standardize = input => actions.standardizeTransactionReceipts(input.receipts, input.request);
add("receipts/defaults", "receipts", { receipts: [receipt], request }, standardize);
for (const field of ["blockHash", "blockNumber", "transactionHash", "transactionIndex", "from", "status"]) {
 const row = { ...receipt }; delete row[field];
 add(`receipts/missing_${field}`, "receipts", { receipts: [row], request }, standardize);
}
for (const [field, value] of [["blockNumber", "0x8000000000000000"], ["transactionIndex", "0x80000000"]]) {
 add(`receipts/overflow_${field}`, "receipts", { receipts: [{ ...receipt, [field]: value }], request }, standardize);
}
add("receipts/duplicate", "receipts", { receipts: [{ ...receipt }, { ...receipt }], request }, standardize);
add("receipts/sorted", "receipts", { receipts: [{ ...receipt, transactionIndex: "0x2", transactionHash: "0x" + "33".repeat(32) }, { ...receipt, transactionIndex: "0x1" }], request }, standardize);
const log = { blockHash: hash, blockNumber: "0x1", logIndex: "0x0", transactionIndex: "0x0", transactionHash: hash };
const block = { hash, number: "0x1", transactions: [{ transactionIndex: "0x0", hash }] };
const logsRequest = { method: "eth_getLogs", params: [{ blockHash: hash }] }, blockRequest = { method: "eth_getBlockByHash", params: [hash, true] };
for (const [name, logs, b] of [
 ["valid", [log], block], ["empty", [], block],
 ["hash", [{ ...log, blockHash: "other" }], block],
 ["number", [{ ...log, blockNumber: "0x2" }], block],
 ["missing_transaction", [log], { ...block, transactions: [] }],
 ["transaction_hash", [{ ...log, transactionHash: "other" }], block],
 ["system_log", [{ ...log, transactionHash: viem.zeroHash }], { ...block, transactions: [] }],
]) add(`validate/${name}`, "validate", { logs, block: b, logsRequest, blockRequest }, input => actions.validateLogsAndBlock(input.logs, input.block, input.logsRequest, input.blockRequest));
const output = { baseline: "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334", counting: "Supplementary vectors executed by unchanged pinned source functions; no original test record credit. Provider live-RPC tests remain separate.", sources, cases };
fs.mkdirSync(path.join(root, "testdata/rpc"), { recursive: true });
fs.writeFileSync(path.join(root, "testdata/rpc/actions-differential.json"), JSON.stringify(output, null, 2) + "\n");
console.log({ vectors: cases.length, errors: cases.filter(x => x.error).length });
