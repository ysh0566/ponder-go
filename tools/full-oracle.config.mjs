import path from "node:path";
import {pathToFileURL} from 'node:url';
import {resolveOracleRoot} from './loopback-adapter.mjs';
const upstream = resolveOracleRoot();
const {default: base} = await import(pathToFileURL(path.join(upstream,'packages/core/vite.config.ts')));

// Preserve original setup, aliases and hook order. Limit worker concurrency
// for the 2 GiB cloud executor; test bodies and timeouts are unchanged.
export default {
	...base,
	root: path.join(upstream, "packages/core"),
	test: { ...base.test, maxWorkers: 1 },
};
