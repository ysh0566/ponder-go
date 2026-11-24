import { execFileSync } from "node:child_process";
import path from "node:path";
import { pathToFileURL } from "node:url";

const upstream = process.env.PONDER_UPSTREAM_ROOT;
if (!upstream)
	throw new Error(
		"PONDER_UPSTREAM_ROOT must point to the pinned Ponder checkout",
	);
const git = (...args) =>
	execFileSync("git", ["-C", upstream, ...args], { encoding: "utf8" }).trim();
if (
	git("rev-parse", "HEAD") !== "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334" ||
	git("status", "--porcelain", "--untracked-files=no")
) {
	throw new Error("The pinned Ponder checkout must be clean");
}

const root = path.join(upstream, "packages/core");
const { default: base } = await import(
	pathToFileURL(path.join(root, "vite.config.ts")).href
);

export default {
	...base,
	root,
	test: {
		...base.test,
		globalSetup: [],
		maxWorkers: 1,
		include: [
			path.join(import.meta.dirname, "source-runtime-omnichain-sparse.test.ts"),
		],
	},
};
