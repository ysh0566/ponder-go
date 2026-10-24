// Extract the pinned upstream allow-list and traversal schema without evaluating it.
import fs from "node:fs";
import crypto from "node:crypto";
import {spawnSync} from "node:child_process";
import path from "node:path";
import { createRequire } from "node:module";
const root = process.env.PONDER_PORT_ROOT ?? path.resolve(import.meta.dirname, ".."),
	up = path.resolve(root, "../ponder-upstream"),
	require = createRequire(path.join(up, "package.json")),
	ts = require("typescript");
const file = "packages/core/src/utils/sql-parse.ts",
	text = fs.readFileSync(path.join(up, file), "utf8"),
	sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true),
	vars = new Map();
function declarations(n) {
	if (ts.isVariableDeclaration(n) && ts.isIdentifier(n.name))
		vars.set(n.name.text, n.initializer);
	ts.forEachChild(n, declarations);
}
declarations(sf);
function firstArray(n) {
	if (ts.isArrayLiteralExpression(n)) return n;
	let found;
	ts.forEachChild(n, (c) => {
		found ??= firstArray(c);
	});
	return found;
}
const strings = (name) =>
	firstArray(vars.get(name))
		.elements.filter(ts.isStringLiteral)
		.map((x) => x.text);
const validators = {};
for (const [id, n] of vars) {
	if (!id.endsWith("_VALIDATOR") || !ts.isObjectLiteralExpression(n)) continue;
	const properties = Object.fromEntries(
		n.properties
			.filter(ts.isPropertyAssignment)
			.map((x) => [x.name.getText(sf), x.initializer]),
	);
	const name = properties.node.text,
		fields = new Map();
	function scan(c) {
		if (
			ts.isPropertyAccessExpression(c) &&
			ts.isIdentifier(c.expression) &&
			c.expression.text === "node"
		)
			fields.set(c.name.text, {
				field: c.name.text,
				...fields.get(c.name.text),
			});
		if (ts.isObjectLiteralExpression(c))
			for (const p of c.properties) {
				if (
					ts.isPropertyAssignment(p) &&
					ts.isPropertyAccessExpression(p.initializer) &&
					p.initializer.expression.getText(sf) === "node"
				)
					fields.set(p.initializer.name.text, {
						field: p.initializer.name.text,
						wrap: p.name.getText(sf),
					});
			}
		ts.forEachChild(c, scan);
	}
	scan(properties.children);
	validators[id] = { name, fields: [...fields.values()] };
}
const allow = firstArray(vars.get("ALLOW_LIST"))
	.elements.filter(ts.isIdentifier)
	.map((x) => validators[x.text].name);
const revision=spawnSync("git",["rev-parse","HEAD"],{cwd:up,encoding:"utf8"});
if(revision.status!==0||revision.stdout.trim()!=="17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334")throw Error("Wrong pinned SQL source revision");
const rules = {
	source: file,
 source_sha256:crypto.createHash("sha256").update(text).digest("hex"),
	revision: "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334",
	allow,
	find: [...allow, "UpdateStmt", "DeleteStmt"],
	functions: strings("ALLOWED_FUNCTIONS"),
	system_tables: strings("SYSTEM_TABLES"),
	nodes: Object.fromEntries(
		Object.values(validators).map((x) => [x.name, x.fields]),
	),
};
const destination=path.join(root,"sqlquery/rules.json"),generated=JSON.stringify(rules,null,2)+"\n";
if(process.argv.includes("--write")){
 fs.mkdirSync(path.dirname(destination),{recursive:true});fs.writeFileSync(destination,generated);
}else if(!fs.existsSync(destination)||fs.readFileSync(destination,"utf8")!==generated){
 throw Error("Native SQL rules differ from the actual pinned source; prepare explicitly with --write before Go execution");
}
const report={source:rules.source,source_sha256:rules.source_sha256,revision:rules.revision,rules:{file:"sqlquery/rules.json",sha256:crypto.createHash("sha256").update(generated).digest("hex")},nodes:allow.length,functions:rules.functions.length,system_tables:rules.system_tables.length};
fs.mkdirSync(path.join(root,"parity/runs"),{recursive:true});
fs.writeFileSync(path.join(root,"parity/runs/sql-rules.json"),JSON.stringify(report,null,2)+"\n");
console.log(report);
