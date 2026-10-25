// Historical capture provenance only. Never a fallback for current run inputs.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
const root=path.resolve(import.meta.dirname,'..');
export const manifestFile='parity/runs/runtime-index-m35/historical-capture-context/manifest.json';
const frozenFile='parity/runs/runtime-index-m35/historical-capture-context/full-oracle.config.mjs';
const commit='d9b2ae852a735a7e4d7c66a940d55d5f3e924400',blob='53e5fdc3bf23b341f154fe61277ff6bafadb370e';
const oldHash='db4df171639a49711cfcc645e966a4643e543b24dbb69221a9b26e642a40dc3c';
const fixtures={
 'testdata/sql/captured-original-sql.json':'72db2d1df1cb9d483dce133f91a6bf97e822cf9a7b60f7cc4f2ac4471d75250e',
 'testdata/runtime/captured-original-events.json':'e34c93b5f8155a3fa00ad7b5b3015aea156f766913183a4b8dd954302a0b0193',
 'testdata/runtime/captured-original-filters.json':'0bd3cb884a0ad1173a03898a08bd3745524a301099296e34a69f597398f5026a',
 'testdata/bloom/captured-original-blooms.json':'6ea833cbed59a4e5f5df51d41b7e0ca45b79b9dbe563acb3ce13cd5f8757301e',
};
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
export function validateHistoricalFixture(fixtureFile,fixtureBytes) {
 if(!Object.hasOwn(fixtures,fixtureFile)||sha(fixtureBytes)!==fixtures[fixtureFile])throw Error('Unknown or changed historical fixture');
 const fixture=JSON.parse(fixtureBytes);
 const matches=fixture.capture_proof?.source_context?.filter(entry=>entry.file==='tools/full-oracle.config.mjs');
 if(!matches||matches.length!==1||matches[0].sha256!==oldHash)throw Error('Exactly one pinned historical harness reference required');
 return fixture;
}
export function validateHistoricalLink({fixtureFile,fixtureBytes,rawBytes,entry,manifest,frozenBytes}) {
 const fixture=validateHistoricalFixture(fixtureFile,fixtureBytes),proof=fixture.capture_proof;
 if(!proof||!proof.source_context.some(ref=>equal(ref,entry))||entry.file!=='tools/full-oracle.config.mjs'||entry.sha256!==oldHash)throw Error('Not the exact historical harness reference');
 if(sha(rawBytes)!==proof.raw_oracle.sha256)throw Error('Historical raw oracle changed');
 if(manifest.format!=='published-historical-capture-context-v1'||manifest.scope!=='historical_capture_proof_only_not_current_execution'||manifest.commit!==commit||manifest.blob!==blob||manifest.entries.length!==4)throw Error('Wrong published historical provenance');
 const matches=manifest.entries.filter(x=>x.fixture.file===fixtureFile),link=matches[0];
 if(matches.length!==1||link.fixture.sha256!==fixtures[fixtureFile]||!equal(link.original_ref,entry)||!equal(link.raw_oracle,proof.raw_oracle)||link.frozen_ref.file!==frozenFile||link.frozen_ref.sha256!==oldHash)throw Error('Historical archive link differs');
 const object=Buffer.concat([Buffer.from(`blob ${frozenBytes.length}\0`),frozenBytes]);
 if(sha(frozenBytes)!==oldHash||crypto.createHash('sha1').update(object).digest('hex')!==blob)throw Error('Frozen published harness bytes changed');
 return {...link.frozen_ref,historical_original_ref:entry,historical_commit:commit,historical_blob:blob};
}
export function historicalCaptureContext(fixtureFile) {
 if(!Object.hasOwn(fixtures,fixtureFile))throw Error('Only the four pinned historical captures may use this archive');
 const fixtureBytes=fs.readFileSync(path.join(root,fixtureFile)),fixture=validateHistoricalFixture(fixtureFile,fixtureBytes);
 const manifest=JSON.parse(fs.readFileSync(path.join(root,manifestFile))),frozenBytes=fs.readFileSync(path.join(root,frozenFile));
 const rawBytes=fs.readFileSync(path.join(root,fixture.capture_proof.raw_oracle.file));
 return fixture.capture_proof.source_context.map(entry=>entry.file==='tools/full-oracle.config.mjs'?validateHistoricalLink({fixtureFile,fixtureBytes,rawBytes,entry,manifest,frozenBytes}):entry);
}
