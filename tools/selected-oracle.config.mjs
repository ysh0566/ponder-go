import path from 'node:path';
const root=path.resolve(import.meta.dirname,'..'),up=path.resolve(root,'../ponder-upstream');
export default {root,resolve:{alias:{'@':path.join(up,'packages/core/src'),'@ponder/utils':path.join(up,'packages/utils/src')}},test:{pool:'threads',maxWorkers:1,restoreMocks:true,testTimeout:15000,hookTimeout:15000}};
