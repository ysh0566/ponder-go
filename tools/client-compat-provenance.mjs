import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import {execFileSync} from 'node:child_process';
export const pinnedClientCommit='17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334';
export const sha256=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
export function assertClientProvenance({head,status,sdk,original}) {
 if(head!==pinnedClientCommit)throw Error('Original client checkout is not pinned');
 if(status!=='')throw Error('Original client checkout has tracked changes');
 if(!Buffer.from(sdk).equals(Buffer.from(original)))throw Error('SDK differs from pinned original client');
}
export function verifyClientProvenance(root) {
 const upstream=path.resolve(root,'../ponder-upstream');
 const git=(...args)=>execFileSync('git',['-C',upstream,...args],{encoding:'utf8'}).trim();
 const head=git('rev-parse','HEAD'),status=git('status','--porcelain','--untracked-files=no');
 const sdkFile=path.join(root,'sdk/typescript/client/src/index.ts'),originalFile=path.join(upstream,'packages/client/src/index.ts');
 const sdk=fs.readFileSync(sdkFile),original=fs.readFileSync(originalFile);
 assertClientProvenance({head,status,sdk,original});
 return {pinned_commit:head,tracked_clean:true,sdk_matches_original:true,sdk_file:path.relative(root,sdkFile),original_file:path.relative(root,originalFile),sha256:sha256(sdk)};
}
export function verifyFingerprints(entries) {
 for(const entry of entries)if(sha256(fs.readFileSync(entry.file))!==entry.sha256)throw Error('Execution input changed: '+entry.file);
}
