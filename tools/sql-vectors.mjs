// Supplementary calls into the actual pinned source. Zero original test credit.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { pathToFileURL } from "node:url";

const root=process.env.PONDER_PORT_ROOT??path.resolve(import.meta.dirname,"..");
const upstream=path.resolve(root,"../ponder-upstream");
const output=process.argv.find(a=>a.startsWith("--output="))?.slice(9);
if(!output)throw Error("Explicit --output is required before native input snapshot");
const source=await import(pathToFileURL(path.join(upstream,"packages/core/src/utils/sql-parse.ts")));
const inputs=[
  "", ";", "/* empty */", "SELECT 1; SELECT 2;", "SELECT", "SELEKT 1", "SELECT 'unterminated",
  "SELECT * FROM users", "SELECT u.name FROM users AS u", "SELECT * FROM users AS u JOIN metadata AS m ON u.id=m.id",
  "SELECT * FROM \"offchain\".\"metadata\"", "SELECT * FROM pg_stat_activity", "SELECT * FROM _ponder_checkpoint",
  "SELECT * INTO users", "SELECT * FROM users FOR UPDATE", "SELECT * FROM users FOR SHARE",
  "WITH cte AS (SELECT * FROM users) SELECT * FROM cte",
  "WITH cte AS MATERIALIZED (SELECT * FROM users) SELECT * FROM cte",
  "WITH cte AS NOT MATERIALIZED (SELECT * FROM users) SELECT * FROM cte",
  "WITH RECURSIVE cte AS (SELECT 1 AS num UNION ALL SELECT num+1 FROM cte) SELECT * FROM cte",
  "SELECT * FROM users UNION ALL SELECT * FROM admins",
  "SELECT * FROM (SELECT * FROM users) AS nested",
  "SELECT EXISTS(SELECT * FROM users)",
  "SELECT (SELECT private_task.bump_cached_balance()) AS value",
  "SELECT count(*) OVER (PARTITION BY id ORDER BY id) FROM users",
  "SELECT sum(balance) FILTER (WHERE balance>0) FROM account",
  "SELECT json_agg(id) FROM users", "SELECT json_build_array(1, 'x')",
  "SELECT pg_catalog.count(*) FROM users", "SELECT pg_sleep(1)", "SELECT blow_up()",
  "SELECT * FROM unnest(ARRAY[1,2,3]) AS n",
  "SELECT a[1] FROM users", "SELECT ARRAY[1,2,3]", "SELECT balance::numeric FROM users",
  "SELECT CASE WHEN balance>0 THEN 1 ELSE 0 END FROM users", "SELECT COALESCE(balance,0) FROM users",
  "SELECT * FROM users GROUP BY ROLLUP(id) ORDER BY id LIMIT $1 OFFSET $2",
  "SELECT DISTINCT ON(id) id FROM users ORDER BY id",
  "VALUES(1),(2)", "SELECT 'x' COLLATE \"C\"",
  "UPDATE users SET col=$1", "DELETE FROM user_schema.table WHERE id=$1", "INSERT INTO users(id)VALUES(1)",
  "WITH removed AS (DELETE FROM users RETURNING id) SELECT * FROM removed",
  "COMMENT ON TABLE users IS 'description'", "SET statement_timeout='1s'", "DROP TABLE users", "CREATE TABLE users(id int)",
  "SELECT 1 /*"+"😀".repeat(2000)+"*/", "SELECT 1 /*"+"😀".repeat(2500)+"*/",
];
for(const name of ["lower","upper","length","trim","replace","substring","concat","abs","round","sqrt"]){
  const args=name==="replace"?"'x','x','y'":name==="substring"?"'xyz',1,1":name==="concat"?"'x','y'": ["abs","round","sqrt"].includes(name)?"1":"'x'";
  inputs.push(`SELECT ${name}(${args}) FROM users`);
}
const {default:createParser}=await import(pathToFileURL(path.join(upstream,"packages/core/node_modules/pg-query-emscripten/pg_query.js")));
const parser=await createParser();
const parserVersion=parser.parse("SELECT 1").parse_tree.version;
if(parserVersion!==160001)throw Error("Unexpected actual source parser version");
const result=[];
const attempt=async f=>{try{return{ok:true,value:await f()}}catch(e){return{ok:false,error:{name:e.name,message:e.message}}}};
for(const [index,sql]of inputs.entries()){
  const readonly=await source.isReadonlySQLQuery(sql);
  const first=await attempt(async()=>{await source.validateAllowableSQLQuery(sql);return null;});
  const cached=await attempt(async()=>{await source.validateAllowableSQLQuery(sql);return null;});
  const relations=await attempt(async()=>[...await source.getSQLQueryRelations(sql)]);
  result.push({id:`sql-source-${String(index+1).padStart(3,"0")}`,sql,source_utf16_length:sql.length,readonly,first_validation:first,cached_validation:cached,relations});
}
const digest=file=>({file,sha256:crypto.createHash("sha256").update(fs.readFileSync(path.resolve(root,file))).digest("hex")});
const report={baseline:"17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334",source_parser_version:parserVersion,counting:"Supplementary actual source calls, zero original mappings. Native tests compare readonly, first and cached validation, ordered relation sets and exact helper diagnostics.",sources:["../ponder-upstream/packages/core/src/utils/sql-parse.ts","../ponder-upstream/pnpm-lock.yaml","../ponder-upstream/packages/core/node_modules/pg-query-emscripten/pg_query.js","../ponder-upstream/packages/core/node_modules/pg-query-emscripten/package.json",path.relative(root,import.meta.filename)].map(digest),cases:result};
fs.mkdirSync(path.dirname(path.resolve(root,output)),{recursive:true});
fs.writeFileSync(path.resolve(root,output),JSON.stringify(report,null,2)+"\n");
console.log({vectors:result.length,readonly_true:result.filter(c=>c.readonly).length,validation_rejections:result.filter(c=>!c.first_validation.ok).length,source_credit:0});
