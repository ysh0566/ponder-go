// Durable source-context references without modifying raw execution reports.
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import {execFileSync} from 'node:child_process';
import {pinnedCommit,setupPath,adaptSetup,validatePinnedCheckout} from './loopback-adapter.mjs';
const projectRoot=path.resolve(import.meta.dirname,'..');
const upstreamRoot=path.resolve(projectRoot,'../ponder-upstream');
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const digest=file=>sha(fs.readFileSync(file));
const inside=(root,file)=>{const rel=path.relative(root,file);return rel!==''&&!rel.startsWith('../')&&!path.isAbsolute(rel);};
const validSHA=value=>typeof value==='string'&&/^[a-f0-9]{64}$/.test(value);
function relativeFile(file){if(typeof file!=='string'||file===''||file.includes('\\')||file.includes('\0')||path.isAbsolute(file)||path.normalize(file)!==file||file.split('/').some(x=>x==='..'||x==='.'||x===''))throw Error('Unsafe relative context path');return file;}
function pinned(){const git=(...args)=>execFileSync('git',['-C',upstreamRoot,...args],{encoding:'utf8'}).trim();validatePinnedCheckout(git('rev-parse','HEAD'),git('status','--porcelain','--untracked-files=no'));return git('ls-files','-z').split('\0').filter(Boolean);}
export function persistAdaptedContext(copy,evidenceDir,{verificationPhase="before-dispose"}={}){
 if(!["before-dispose","post-interruption"].includes(verificationPhase))throw Error("Unknown verification phase");
 const files=pinned();copy.verify();
 const manifest=copy.manifest;
 if(manifest.pinnedCommit!==pinnedCommit||manifest.pristine_file!==false||JSON.stringify(manifest.changed_files)!==JSON.stringify([setupPath]))throw Error('Invalid adapted context provenance');
 if(manifest.sources.length!==files.length||manifest.sources.some((item,i)=>item.file!==files[i]))throw Error('Incomplete or reordered source inventory');
 const executionRoot=path.resolve(copy.directory),evidenceRoot=path.resolve(evidenceDir);
 if(executionRoot===projectRoot||executionRoot===upstreamRoot||inside(executionRoot,evidenceRoot))throw Error('Evidence must survive deletion of the separate execution copy');
 const archivePath=path.join(evidenceRoot,'context-archive.json'),contextRoot=path.join(evidenceRoot,'context');
 if(fs.existsSync(archivePath)||fs.existsSync(contextRoot))throw Error('Context archive must be new; do not overwrite evidence');
 fs.mkdirSync(contextRoot,{recursive:true});
 const entries={};
 const add=(file,kind,expected)=>{
  relativeFile(file);if(entries[file])throw Error('Duplicate context path');
  const executed=path.join(executionRoot,file);
  if(!validSHA(expected)||digest(executed)!==expected)throw Error('Execution context hash mismatch: '+file);
  let persistent;
  if(kind==='unchanged-tracked'){
   persistent=path.join(upstreamRoot,file);if(digest(persistent)!==expected)throw Error('Original context hash mismatch: '+file);
  }else{
   persistent=path.join(contextRoot,file);fs.mkdirSync(path.dirname(persistent),{recursive:true});fs.copyFileSync(executed,persistent);if(digest(persistent)!==expected)throw Error('Copied context hash mismatch');
  }
  entries[file]={executed_file:executed,persistent_file:persistent,sha256:expected,kind};
 };
 for(const item of manifest.sources){
  relativeFile(item.file);
  const original=fs.readFileSync(path.join(upstreamRoot,item.file));
  if(sha(original)!==item.original_sha256)throw Error('Original tracked bytes changed');
  if(item.file===setupPath){if(sha(adaptSetup(original.toString()))!==item.adapted_sha256)throw Error('Unexpected startup transformation');add(item.file,'adapted-startup',item.adapted_sha256);}
  else {if(item.original_sha256!==item.adapted_sha256)throw Error('Unexpected business source change');add(item.file,'unchanged-tracked',item.original_sha256);}
 }
 for(const item of manifest.generated_artifacts.files)add(item.file,'generated-artifact',item.sha256);
 const archive={format:'ponder-loopback-context-v1',verification_phase:verificationPhase,pinned_commit:pinnedCommit,project_root:projectRoot,upstream_root:upstreamRoot,execution_root:executionRoot,evidence_root:evidenceRoot,manifest_sha256:sha(JSON.stringify(manifest)),environment_adapted:true,pristine_file:false,entries};
 const bytes=JSON.stringify(archive,null,2)+'\n';fs.writeFileSync(archivePath,bytes,{flag:'wx'});
 return {archive,path:archivePath,sha256:sha(bytes)};
}
export function validateContextEntry(entry,archive){
 try {
  if(archive?.format!=='ponder-loopback-context-v1'||archive.pinned_commit!==pinnedCommit||archive.project_root!==projectRoot||archive.upstream_root!==upstreamRoot||archive.environment_adapted!==true||archive.pristine_file!==false)return false;
  if(!entry||typeof entry.file!=='string'||!validSHA(entry.sha256)||entry.file.includes('\\')||entry.file.includes('\0'))return false;
  const actual=path.resolve(projectRoot,entry.file);
  // Canonical leading '..' is valid for a sibling checkout; internal traversal aliases are not.
  if(path.isAbsolute(entry.file)?path.normalize(entry.file)!==entry.file:path.relative(projectRoot,actual)!==entry.file)return false;
  const executionRoot=path.resolve(archive.execution_root);
  if(executionRoot===projectRoot||executionRoot===upstreamRoot)return false;
  if(inside(executionRoot,actual)){
   const relative=relativeFile(path.relative(executionRoot,actual));
   if(!Object.hasOwn(archive.entries,relative))return false;
   const mapped=archive.entries[relative];
   if(mapped.executed_file!==actual||mapped.sha256!==entry.sha256)return false;
   let expected;
   if(mapped.kind==='unchanged-tracked')expected=path.join(upstreamRoot,relative);
   else if((mapped.kind==='adapted-startup'&&relative===setupPath)||mapped.kind==='generated-artifact')expected=path.join(path.resolve(archive.evidence_root),'context',relative);
   else return false;
   if(mapped.persistent_file!==expected||digest(expected)!==entry.sha256)return false;
   // When the execution copy still exists, never use the archive to hide changed bytes.
   if(fs.existsSync(actual)&&digest(actual)!==entry.sha256)return false;
   return true;
  }
  if(!inside(projectRoot,actual)&&!inside(upstreamRoot,actual))return false;
  return digest(actual)===entry.sha256;
 }catch{return false;}
}

export function persistInterruptedContext(directory,evidenceDir){
 const manifest=JSON.parse(fs.readFileSync(path.join(directory,'.ponder-loopback-manifest.json'),'utf8'));
 const copy={directory,manifest,verify(){
  for(const item of manifest.sources){relativeFile(item.file);if(digest(path.join(directory,item.file))!==item.adapted_sha256||digest(path.join(upstreamRoot,item.file))!==item.original_sha256)throw Error('Interrupted context changed: '+item.file);}
  for(const item of manifest.generated_artifacts.files){relativeFile(item.file);if(digest(path.join(directory,item.file))!==item.sha256)throw Error('Interrupted generated artifact changed');}
 }};
 return persistAdaptedContext(copy,evidenceDir,{verificationPhase:'post-interruption'});
}
