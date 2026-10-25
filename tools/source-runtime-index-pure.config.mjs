import path from 'node:path';
import base from './selected-oracle.config.mjs';
export default {...base,resolve:{...base.resolve,alias:{...base.resolve.alias,viem:path.resolve(import.meta.dirname,'../../ponder-upstream/node_modules/viem')}}};
