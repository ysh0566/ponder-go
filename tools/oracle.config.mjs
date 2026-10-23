import path from "node:path";
const upstream = path.resolve(import.meta.dirname, "../../ponder-upstream");
export default {
	root: path.join(upstream, "packages/core"),
	resolve: {
		alias: {
			"@": path.join(upstream, "packages/core/src"),
			"@ponder/utils": path.join(upstream, "packages/utils/src"),
			"@ponder/client": path.join(upstream, "packages/client/src"),
		},
	},
	test: {
		pool: "threads",
		maxWorkers: 4,
		restoreMocks: true,
		testTimeout: 15000,
	},
};
