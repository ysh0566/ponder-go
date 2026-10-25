import path from 'node:path';
import {pathToFileURL} from 'node:url';
const upstream=process.env.PONDER_UPSTREAM_ROOT;
if(!upstream)throw Error('Explicit verified adapted copy required');
const {default:base}=await import(pathToFileURL(path.join(upstream,'packages/core/vite.config.ts')).href);
export default {...base,root:path.join(upstream,'packages/core'),test:{...base.test,maxWorkers:1,include:[path.join(import.meta.dirname,'source-multichain-atomic.test.ts')]}};
