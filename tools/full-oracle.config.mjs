import path from "node:path";
import base from "../../ponder-upstream/packages/core/vite.config.ts";

// Preserve original setup, aliases and hook order. Limit worker concurrency
// for the 2 GiB cloud executor; test bodies and timeouts are unchanged.
export default {
	...base,
	root: path.resolve(import.meta.dirname, "../../ponder-upstream/packages/core"),
	test: { ...base.test, maxWorkers: 1 },
};
