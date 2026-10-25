// Retain the exact RPC object used as the source's WeakMap/WeakSet key.
// Install once, preserving existing spies and their subsequent wrappers.
export function createHistoricalRPCObserver(start,finish){
	const observed=new WeakSet();
	return function observe(rpc){
		if(observed.has(rpc))return rpc;
		observed.add(rpc);
		const request=rpc.request;
		rpc.request=async function(...args){
			const record=start(args);
			try{const result=await Reflect.apply(request,this,args);finish(record,result,null);return result}
			catch(error){finish(record,undefined,error);throw error}
		};
		return rpc;
	};
}
