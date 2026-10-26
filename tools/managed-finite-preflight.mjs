// Read-only inventory/discovery. This does not run tests, generate sources or start databases.
import fs from 'node:fs';
import path from 'node:path';
import {spawnSync} from 'node:child_process';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {managedFiniteHarness,managedFiniteNativeFiles,managedFiniteCredit} from './managed-finite-acceptance.mjs';
import {check,equal,hash,inventory,scopedPath} from './managed-finite-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..');
const output=process.argv.find(a=>a.startsWith('--output='))?.slice(9);
check(output,'Explicit new preflight output required');
const target=scopedPath(root,output,'parity/runs/managed-finite-m44');
check(!fs.existsSync(target),'Do not overwrite preflight evidence');
const tests=inventory(root);
const r=spawnSync('go',['list','-json','./...'],{cwd:root,encoding:'utf8',timeout:300000,maxBuffer:32*1024*1024});
check(r.status===0&&!r.error,'Actual go list discovery failed: '+r.stderr);
// Parse the concatenated JSON stream without guessing a package count from tests.
const packages=[];let start=0,depth=0,string=false,escape=false;
for(let i=0;i<r.stdout.length;i++) { const c=r.stdout[i]; if(string){if(escape)escape=false;else if(c==='\\')escape=true;else if(c==='"')string=false;continue;} if(c==='"'){string=true;continue;} if(c==='{'){if(depth===0)start=i;depth++;} else if(c==='}'&&--depth===0)packages.push(JSON.parse(r.stdout.slice(start,i+1))); }
check(packages.length===24&&new Set(packages.map(p=>p.ImportPath)).size===24,'Expected unchanged 24-package module discovery');
const inputs=new Set(packages.flatMap(p=>['GoFiles','CgoFiles','TestGoFiles','XTestGoFiles'].flatMap(k=>(p[k]??[]).map(f=>path.relative(root,path.join(p.Dir,f))))));
for(const file of managedFiniteNativeFiles)check(inputs.has(file),'Managed test absent from actual package discovery '+file);
const discoveredManaged=[...inputs].filter(f=>f.endsWith('_test.go')&&path.basename(f).includes('managed')).sort();
check(equal(discoveredManaged,[...managedFiniteNativeFiles].sort()),'Actual managed test file discovery differs from explicit manifest');
const report={complete:true,success:true,scope:'read_only_m44_inventory_not_test_acceptance',...managedFiniteCredit,recorded_utc:new Date().toISOString(),native_inputs:sourceFingerprint(root),harness_inputs:managedFiniteHarness.map(f=>hash(root,f)),native_tests:tests,managed_test_inputs:managedFiniteNativeFiles.map(f=>hash(root,f)),discovery:{command:['go','list','-json','./...'],exit_code:r.status,packages:packages.map(p=>p.ImportPath).sort(),package_count:packages.length,source_file_count:inputs.size},note:'Leaf and batch counts are established only by actual executions, never inferred as passing evidence.'};
fs.mkdirSync(path.dirname(target),{recursive:true});fs.writeFileSync(target,JSON.stringify(report,null,2)+'\n',{flag:'wx'});console.log(JSON.stringify({output:path.relative(root,target),tests:tests.length,packages:packages.length,source_credit:0}));
