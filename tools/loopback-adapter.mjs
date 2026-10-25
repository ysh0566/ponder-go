// Environment-adapted startup harness. NEVER represents a pristine source run.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import {execFileSync, spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
export const pinnedCommit = '17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334';
export const setupPath = 'packages/core/src/_test/globalSetup.ts';
const root = path.resolve(import.meta.dirname, '..');
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const git = (upstream, ...args) => execFileSync('git', ['-C', upstream, ...args], {encoding:'utf8'}).trim();
export function validatePinnedCheckout(commit, status) {
  if(commit!==pinnedCommit||status!=='')throw Error('Pinned upstream must remain clean');
}
export function adaptSetup(original) {
  const from = 'import { startProxy } from "@viem/anvil";';
  const start = '  const shutdownProxy = await startProxy({\n    options: {';
  const end = '      stopTimeout: 100,\n    },\n  });';
  for (const needle of [from, start, end]) if (original.split(needle).length !== 2) throw Error('Pinned startup shape changed');
  return original.replace(from, 'import { createPool, startProxy } from "@viem/anvil";')
    .replace(start, `  // Environment-adapted startup: explicit loopback ports, no interface discovery.
  const shutdownProxy = await startProxy({
    host: "127.0.0.1",
    port: 8545,
    pool: createPool({ autoPort: false }),
    options: (id) => {
      if (!Number.isSafeInteger(id) || id < 1 || id > 64) {
        throw new Error("Invalid isolated Anvil worker ID: " + id);
      }
      return {
      host: "127.0.0.1",
      port: 18545 + id,`)
    .replace(end, '      stopTimeout: 100,\n      };\n    },\n  });');
}
export function createAdaptedUpstream({upstream=path.resolve(root, '../ponder-upstream'), parent=os.tmpdir(), artifactsFrom=upstream}={}) {
  validatePinnedCheckout(git(upstream, 'rev-parse', 'HEAD'),git(upstream, 'status', '--porcelain', '--untracked-files=no'));
  for (const dir of [upstream, path.join(upstream,'packages/core')]) if(fs.existsSync(path.join(dir,'.env.local'))) throw Error('Unexpected upstream dotenv');
  if (fs.statfsSync(parent).bavail * fs.statfsSync(parent).bsize < 5 * 1024 ** 3) throw Error('Less than 5 GiB free; refusing adaptation');
  const directory=fs.mkdtempSync(path.join(parent,'ponder-loopback-adapted-'));
  const sources=[];
  try {
    const files=git(upstream,'ls-files','-z').split('\0').filter(Boolean);
    for (const file of files) {
      const from=path.join(upstream,file), to=path.join(directory,file);
      fs.mkdirSync(path.dirname(to),{recursive:true}); fs.copyFileSync(from,to);
      sources.push({file,original_sha256:hash(fs.readFileSync(from)),adapted_sha256:hash(fs.readFileSync(to))});
    }
    if (git(artifactsFrom, 'rev-parse', 'HEAD') !== pinnedCommit) throw Error('Artifact checkout commit mismatch');
    for (const item of sources) if(hash(fs.readFileSync(path.join(artifactsFrom,item.file)))!==item.original_sha256) throw Error('Artifact source mismatch: '+item.file);
    const artifacts=[];
    function copyArtifact(file) {
      const from=path.join(artifactsFrom,file), to=path.join(directory,file);
      if(fs.statSync(from).isDirectory()){ fs.mkdirSync(to,{recursive:true}); for(const entry of fs.readdirSync(from))copyArtifact(path.join(file,entry)); }
      else {fs.copyFileSync(from,to); artifacts.push({file,sha256:hash(fs.readFileSync(from))});}
    }
    // Build artifacts are required by unchanged contract fixtures. Copy, never regenerate upstream.
    for(const file of ['packages/core/src/_test/generated.ts','packages/core/src/_test/contracts/out']) {
      if(!fs.existsSync(path.join(artifactsFrom,file))) throw Error('Missing original generated fixture: '+file);
      copyArtifact(file);
    }
    // Immutable installed dependencies only; test sources themselves are copied, not linked.
    const moduleDirs=new Set(['node_modules',...files.filter(f=>f.endsWith('/package.json')).map(f=>path.join(path.dirname(f),'node_modules'))]);
    for(const file of moduleDirs) if(fs.existsSync(path.join(upstream,file))) fs.symlinkSync(path.join(upstream,file),path.join(directory,file),'dir');
    const original=fs.readFileSync(path.join(upstream,setupPath),'utf8');
    const adapted=adaptSetup(original); fs.writeFileSync(path.join(directory,setupPath),adapted);
    for(const item of sources) item.adapted_sha256=hash(fs.readFileSync(path.join(directory,item.file)));
    const changed=sources.filter(x=>x.original_sha256!==x.adapted_sha256).map(x=>x.file);
    if(JSON.stringify(changed)!==JSON.stringify([setupPath])) throw Error('Unexpected adapted source difference');
    const diff=spawnSync('diff',['-u','--label','original/'+setupPath,'--label','adapted/'+setupPath,path.join(upstream,setupPath),path.join(directory,setupPath)],{encoding:'utf8'});
    if(diff.status!==1) throw Error('Expected startup-only diff');
    const manifest={method:'Environment-adapted startup, NOT pristine original execution',pristine_file:false,source_credit:0,pinnedCommit,changed_files:changed,sources,dependency_links:'Read-only use of existing pinned installation',generated_artifacts:{from:artifactsFrom,files:artifacts},proxy:{host:'127.0.0.1',port:8545},anvil:{host:'127.0.0.1',port:'18545 + validated worker ID (1..64)',autoPort:false}};
    fs.writeFileSync(path.join(directory,'.ponder-loopback-manifest.json'),JSON.stringify(manifest));
    return {directory,manifest,diff:diff.stdout,verify(){for(const item of sources){if(hash(fs.readFileSync(path.join(upstream,item.file)))!==item.original_sha256)throw Error('Original source changed: '+item.file);if(hash(fs.readFileSync(path.join(directory,item.file)))!==item.adapted_sha256)throw Error('Adapted source changed: '+item.file);}for(const item of artifacts)if(hash(fs.readFileSync(path.join(directory,item.file)))!==item.sha256)throw Error('Adapted artifact changed: '+item.file);},dispose(){fs.rmSync(directory,{recursive:true,force:true});}};
  } catch(error) {fs.rmSync(directory,{recursive:true,force:true});throw error;}
}
export function resolveOracleRoot() {
  const upstream=path.resolve(root,'../ponder-upstream');
  if(!process.env.PONDER_UPSTREAM_ROOT)return upstream;
  validatePinnedCheckout(git(upstream, 'rev-parse', 'HEAD'),git(upstream, 'status', '--porcelain', '--untracked-files=no'));
  const selected=path.resolve(process.env.PONDER_UPSTREAM_ROOT);
  if(selected===upstream)throw Error('Adapted root must be separate from pinned original');
  const manifest=JSON.parse(fs.readFileSync(path.join(selected,'.ponder-loopback-manifest.json'),'utf8'));
  if(manifest.pinnedCommit!==pinnedCommit||manifest.pristine_file!==false||JSON.stringify(manifest.changed_files)!==JSON.stringify([setupPath]))throw Error('Invalid startup adaptation manifest');
  const files=git(upstream,'ls-files','-z').split('\0').filter(Boolean);
  if(manifest.sources.length!==files.length)throw Error('Incomplete adaptation manifest');
  for(let i=0;i<files.length;i++) {
    const file=files[i],entry=manifest.sources[i];
    if(entry.file!==file)throw Error('Unmapped source in adaptation');
    const original=fs.readFileSync(path.join(upstream,file));
    const expected=file===setupPath?adaptSetup(original.toString()):original;
    if(hash(original)!==entry.original_sha256||hash(expected)!==entry.adapted_sha256||hash(fs.readFileSync(path.join(selected,file)))!==entry.adapted_sha256)throw Error('Adapted source hash mismatch: '+file);
  }
  for(const entry of manifest.generated_artifacts.files)if(hash(fs.readFileSync(path.join(selected,entry.file)))!==entry.sha256)throw Error('Generated artifact changed: '+entry.file);
  return selected;
}

if(process.argv[1] && path.resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  throw Error('Import createAdaptedUpstream from this module; always dispose the returned task-owned copy in finally. Use loopback-adapter-check.mjs for verification.');
}
