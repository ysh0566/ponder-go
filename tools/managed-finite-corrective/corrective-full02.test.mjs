import test from 'node:test';import assert from 'node:assert/strict';
import {classifyNative,validateDiscovery,packages,emptyPackages} from './corrective-full02.mjs';
function fixture(mode='normal') {
 const Time='2026-10-26T12:00:01Z',manifest={complete:true,success:true,scope:'all_module_packages',race:mode==='race',started_utc:'2026-10-26T12:00:00Z',finished_utc:'2026-10-26T12:00:02Z',packages:[]},events=[];
 for(const pkg of packages) {
  const names=emptyPackages.includes(pkg)?[]:['TestControl'];
  manifest.packages.push({package:pkg,listed_functions:names,batches:[{functions:names,complete:true,exit_code:0,signal:null,error:null,command:['go','test','-count=1',...(mode==='race'?['-race']:[]),'-json',...(names.length?['-run','^(TestControl)$']:[]),pkg]}]});
  if(names.length)events.push({Time,Package:pkg,Test:'TestControl',Action:'run'},{Time,Package:pkg,Test:'TestControl',Action:'pass'},{Time,Package:pkg,Action:'pass'});
  else events.push({Time,Package:pkg,Action:'skip'});
 }
 return{manifest,events,discovery:[...packages]};
}
for(const mode of ['normal','race'])test('only exact proven no-Test package skips admitted '+mode,()=>{const f=fixture(mode),r=classifyNative(f.manifest,f.events,f.discovery,mode);assert.equal(r.package_skips.length,6);assert.equal(r.leaf_count,18);assert.equal(r.filtered.some(e=>e.Action==='skip'),false);});
const negative={
 'Test skip':f=>f.events.push({Time:f.events[0].Time,Package:packages[0],Test:'TestControl',Action:'skip'}),
 'empty Test field still test-scoped':f=>f.events.find(e=>e.Action==='skip').Test='',
 'package fail':f=>f.events.push({Time:f.events[0].Time,Package:emptyPackages[0],Action:'fail'}),
 'test fail':f=>f.events.push({Time:f.events[0].Time,Package:packages[0],Test:'TestControl',Action:'fail'}),
 'nonempty package skip':f=>f.events.push({Time:f.events[0].Time,Package:packages[0],Action:'skip'}),
 'duplicate empty package skip':f=>f.events.push({...f.events.find(e=>e.Action==='skip')}),
 'missing empty package skip':f=>f.events.splice(f.events.findIndex(e=>e.Action==='skip'),1),
 'unknown package event':f=>f.events.push({Time:f.events[0].Time,Package:'ponder.local/ponder-go/unknown',Action:'skip'}),
 'missing independent package':f=>f.discovery.pop(),
 'unknown independent package':f=>f.discovery.push('ponder.local/ponder-go/unknown'),
 'missing manifest package':f=>f.manifest.packages.pop(),
 'false empty function list':f=>f.manifest.packages.find(p=>emptyPackages.includes(p.package)).listed_functions.push('TestHidden'),
 'failed empty package batch':f=>f.manifest.packages.find(p=>emptyPackages.includes(p.package)).batches[0].exit_code=1,
 'missing actual test pass':f=>f.events.splice(f.events.findIndex(e=>e.Action==='pass'&&e.Test),1),
 'duplicate actual test pass':f=>f.events.push({...f.events.find(e=>e.Action==='pass'&&e.Test)}),
 'changed native command':f=>f.manifest.packages[0].batches[0].command.push('-run','NoTests'),
};
for(const [name,mutate]of Object.entries(negative))test('reject '+name,()=>{const f=fixture();mutate(f);assert.throws(()=>classifyNative(f.manifest,f.events,f.discovery,'normal'));});

test('independent raw discovery rejects TestGoFiles in purported empty package',()=>{const p=packages.map(ImportPath=>({ImportPath}));p.find(x=>emptyPackages.includes(x.ImportPath)).TestGoFiles=['hidden_test.go'];assert.throws(()=>validateDiscovery(p));});
test('independent raw discovery rejects XTestGoFiles in purported empty package',()=>{const p=packages.map(ImportPath=>({ImportPath}));p.find(x=>emptyPackages.includes(x.ImportPath)).XTestGoFiles=['hidden_test.go'];assert.throws(()=>validateDiscovery(p));});
