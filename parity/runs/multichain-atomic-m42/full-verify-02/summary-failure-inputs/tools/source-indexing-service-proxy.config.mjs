import path from 'node:path';
import {pathToFileURL} from 'node:url';
const upstream=process.env.PONDER_UPSTREAM_ROOT;
if(!upstream)throw Error('Explicit pinned upstream required');
const {default:base}=await import(pathToFileURL(path.join(upstream,'packages/core/vite.config.ts')).href);
export default {...base,root:path.join(upstream,'packages/core'),test:{...base.test,globalSetup:[],maxWorkers:1,include:[path.join(import.meta.dirname,'source-indexing-service-proxy.test.ts')]}};
