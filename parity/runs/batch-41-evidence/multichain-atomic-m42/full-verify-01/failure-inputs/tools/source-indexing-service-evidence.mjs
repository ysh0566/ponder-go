import {sameOriginalDeclaration} from './source-runtime-historical-evidence.mjs';
export {sameOriginalDeclaration} from './source-runtime-historical-evidence.mjs';
export const indexingServiceTargets=Object.freeze(['8095ab8a2c6db72422f9','d30405de5e5c29732267','9bcd2ee96096cf0baae4','9ae03f6604b78323700e','b6127c0d47d2bb8fd1da','0fae55812c46f74f1566','d7d1c6e399576ff940b3','968bb8a7d0476d79c8fb','d75ae6d953a7bb8ea73c','584d162581ce27033006','71d417e2bb1b5285447b','26932124c42fde4c8e04','afb6150d1f928a58bf24','772c4096d1ac5227fc45','8a08a48e2a99b23fa016']);
export const indexingServiceTypeGaps=Object.freeze(['d75ae6d953a7bb8ea73c','afb6150d1f928a58bf24','8a08a48e2a99b23fa016']);
export const object=value=>value?.$object?Object.fromEntries(value.$object):value;
export function assertIndexingServiceCapture(capture,declarations){
 const check=(value,message)=>{if(!value)throw Error(message)};
 check(declarations.length===23&&declarations.reduce((n,c)=>n+c.assertions.length,0)===42,'Pinned complete source census changed');
 check(capture?.cases?.length===23&&new Set(capture.cases.map(c=>c.id)).size===23,'Complete unique original capture required');
 let assertions=0,types=0,methods=0,callbacks=0,providers=0;
 for(const declaration of declarations){
  const item=capture.cases.find(c=>c.id===declaration.id),targeted=indexingServiceTargets.includes(declaration.id);
  check(sameOriginalDeclaration(item,declaration),'Immutable source declaration changed: '+declaration.id);
  check(item.targeted===targeted,'Exact source target selection required');
  if(!targeted){check(item.calls.length===0,'Client facade cases must not claim service capture');continue}
  assertions+=declaration.assertions.length;types+=declaration.type_error_directives.length;
  check(item.calls.length===1&&item.calls[0].operation==='createIndexing'&&item.calls[0].args&&item.calls[0].result_defined===true,'One actual createIndexing call required');
  for(const method of item.calls[0].methods){
   check(['processSetupEvents','processRealtimeEvents','processHistoricalEvents'].includes(method.operation)&&method.args&&method.before&&method.after,'Complete source method required');
   check(Object.hasOwn(method,'result')!==Object.hasOwn(method,'error'),'One original result or error required');
   check(Object.keys(method.physical_before??{}).length===9&&Object.keys(method.physical_after??{}).length===9,'Nine physical tables required');
   check(method.providers.every(p=>p.request&&Object.hasOwn(p,'response')&&!p.error),'Real raw RPC completion required');
   check(method.callbacks.every(c=>typeof c.fn_source==='string'&&c.context&&Number.isInteger(c.argument_identity)&&Number.isInteger(c.context_identity)&&Number.isInteger(c.client_identity)&&Number.isInteger(c.db_identity)&&Array.isArray(c.operations)),'Actual callback source/identity/context/operations required');
   methods++;callbacks+=method.callbacks.length;providers+=method.providers.length;
  }
 }
 check(assertions===33&&types===3&&methods===15&&callbacks===1014&&providers===2,'Pinned service execution census changed');
 const historical=capture.cases.find(c=>c.id===indexingServiceTargets[14]).calls[0].methods;
 check(historical.length===2&&historical.every(m=>m.operation==='processHistoricalEvents')&&historical[0].callbacks.length===1001&&historical[1].callbacks.length===1,'Actual historical 1001+1 callback execution required');
 for(const callback of historical[0].callbacks)check(JSON.stringify(callback.operations.map(o=>[o.operation,object(o.args).kind,object(o.args).property]))===JSON.stringify([['event.access','transaction','gas'],['event.access','transaction','maxFeePerGas']]),'Original sampled property access order changed');
 check(JSON.stringify(historical[1].callbacks[0].operations.map(o=>object(o.args).property))===JSON.stringify(['gas','maxFeePerGas','maxPriorityFeePerGas']),'Actual missing included field access required');
 check(historical[1].callbacks[0].error?.$error?.name==='InvalidEventAccessError','Genuine source invalid-access callback rejection required');
 return {targeted:15,assertions,type_directives:types,methods,callbacks,providers,maximum_formal_credit_without_type_counterpart:12};
}

export function assertIndexingServiceProxyCapture(capture){
 const check=(value,message)=>{if(!value)throw Error(message)};
 const operations=['get-present','get-null','get-present-undefined','get-untracked','has-present','has-missing-included','has-untracked','ownKeys','objectKeys','set-present','delete-present','get-missing','set-missing','delete-missing','set-untracked','delete-untracked','inspect-missing','identity-after-underlying-replacement','inspect-complete','disabled-get-missing','disabled-set-missing-included','disabled-delete-included','disabled-has-missing-included'];
 check(capture?.source_credit===0&&capture.real_source===true&&capture.initial_underlying&&capture.replacement_underlying,'Genuine zero-credit source proxy inputs required');
 check(JSON.stringify(capture.steps?.map(s=>s.operation))===JSON.stringify(operations),'Exact actual proxy operation sequence required');
 const step=name=>capture.steps.find(s=>s.operation===name);
 check(step('has-missing-included').value===true&&step('has-untracked').value===false&&step('disabled-has-missing-included').value===true,'Actual default-include membership required');
 check(JSON.stringify(step('ownKeys').value)===JSON.stringify(capture.default_include)&&JSON.stringify(step('objectKeys').value)==='[]','OwnKeys/enumeration distinction required');
 check(step('get-null').value===null&&step('get-present-undefined').value?.$undefined===true,'Present null/undefined distinctions required');
 for(const name of ['get-missing','set-missing','delete-missing','inspect-missing'])check(step(name).error?.name==='InvalidEventAccessError'&&step(name).resets.length===1&&step(name).profile.resolved===false&&step(name).sticky?.name==='InvalidEventAccessError','Real invalidation and sticky error required: '+name);
 check(step('identity-after-underlying-replacement').value.sameProxy===true&&step('identity-after-underlying-replacement').value.stickyPreserved===true,'Stable proxy and sticky error required');
 check(JSON.stringify(step('inspect-complete').reads)===JSON.stringify(capture.default_include),'Inspect must read actual default fields in order');
 for(const name of operations.filter(n=>n.startsWith('has-')||n.startsWith('disabled-')||['ownKeys','objectKeys'].includes(n)))check(step(name).reads.length===0,'Reflection/bypass must not invent reads');
 return {operations:operations.length,source_credit:0};
}

export function assertIndexingServiceCacheCapture(capture){
 const check=(value,message)=>{if(!value)throw Error(message)};
 const names=['delayed-cache-insert-success','delayed-cache-insert-failure','user-error-name-is-not-brand','actual-base-error-identity'];
 check(capture?.source_credit===0&&JSON.stringify(capture.cases?.map(c=>c.name))===JSON.stringify(names),'Exact cache/error supplemental cases required');
 for(const [index,c] of capture.cases.slice(0,2).entries()){
  check(c.source_credit===0&&c.real_rpc===true&&c.input?.event?.block?.$bigint==='0'&&c.input.lateFailure===(index===1),'Genuine block-zero client inputs required');
  check(JSON.stringify(c.timeline)===JSON.stringify(['cache_lookup','rpc_begin','rpc_return','insert_begin','getBalance_return','release_insert',index===1?'insert_reject':'insert_resolve']),'Balance must return before gated cache completion');
  check(c.providers?.length===1&&c.providers[0].request.method==='eth_getBalance'&&JSON.stringify(c.providers[0].request.params)===JSON.stringify([c.input.address,'0x0'])&&typeof c.providers[0].response==='string'&&!c.providers[0].error,'Actual scoped Anvil balance provider required');
  check(c.result?.$bigint===BigInt(c.providers[0].response).toString()&&c.settled===true,'Actual decoded provider balance and settled write required');
  check(c.cacheCalls?.length===2&&c.cacheCalls[0].operation==='get'&&c.cacheCalls[1].operation==='insert'&&c.cacheCalls[1].args.requests[0].result===JSON.stringify(c.providers[0].response),'Actual cache boundary input required');
 }
 const user=capture.cases[2],base=capture.cases[3];
 check(user.input.name==='InvalidEventAccessError'&&user.input.isBaseError===false&&user.result.name==='IndexingFunctionError'&&user.result.isIndexingFunctionError===true&&user.result.sameIdentity===false,'User error name must not confer source BaseError identity');
 check(base.input.isBaseError===true&&base.result.name==='InvalidEventAccessError'&&base.result.isIndexingFunctionError===false&&base.result.sameIdentity===true,'Genuine BaseError identity must be preserved');
 return {source_credit:0,cases:4};
}
