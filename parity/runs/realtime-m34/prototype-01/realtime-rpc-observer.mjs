import {AsyncLocalStorage} from 'node:async_hooks';

// Source RPC object identity remains the WeakMap/WeakSet metadata key. Refresh
// the outer request wrapper after later spy replacement. Async request scope
// suppresses duplicate observation through old forwarding wrappers while
// preserving independent concurrent top-level requests.
export function createRealtimeRPCObserver(start, finish) {
 const installed = new WeakMap();
 const requestScope = new AsyncLocalStorage();
 return function observe(rpc) {
  const request = rpc.request;
  if (request === installed.get(rpc)) return rpc;
  const wrapper = function(...args) {
   if (requestScope.getStore()?.owner === rpc) return Reflect.apply(request, this, args);
   const record = start(args);
   return requestScope.run({owner:rpc}, async () => {
    try {const value=await Reflect.apply(request,this,args);finish(record,value,null);return value;}
    catch(error){finish(record,undefined,error);throw error;}
   });
  };
  rpc.request=wrapper;
  installed.set(rpc,wrapper);
  return rpc;
 };
}
