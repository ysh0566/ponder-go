import path from 'node:path';
import base from './full-oracle.config.mjs';
export default {...base,test:{...base.test,setupFiles:[...(base.test.setupFiles??[]),path.join(import.meta.dirname,'source-sync-live.setup.ts')]}};
