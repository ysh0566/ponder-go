import { execFileSync } from "node:child_process";
import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";

const upstream = path.resolve(process.env.PONDER_UPSTREAM);
const core = path.join(upstream, "packages/core");
const app = path.join(upstream, "simulation-test/apps/reference-erc20");
const git = (...args) => execFileSync("git", ["-C", upstream, ...args], { encoding: "utf8" }).trim();
if (git("rev-parse", "HEAD") !== "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334") {
  throw new Error("Use the pinned Ponder 0.18.2 checkout");
}
git("diff", "--quiet", "HEAD", "--", "packages/core/src", "simulation-test/apps/reference-erc20");
const { vitePluginPonder } = await import(pathToFileURL(path.join(core, "src/build/plugin.ts")));
const requireCore = createRequire(path.join(core, "package.json"));

export default {
  root: import.meta.dirname,
  plugins: [vitePluginPonder({ schemaFile: path.join(app, "ponder.schema.ts") })],
  resolve: { alias: [
    { find: /^ponder$/, replacement: path.join(core, "src/index.ts") },
    { find: "@", replacement: path.join(core, "src") },
    { find: "@app", replacement: app },
    { find: "@ponder/client", replacement: path.join(upstream, "packages/client/src") },
    { find: "@ponder/utils", replacement: path.join(upstream, "packages/utils/src") },
    { find: /^graphql$/, replacement: requireCore.resolve("graphql") },
  ] },
  test: { include: ["oracle.test.mjs"], globals: true, maxWorkers: 1, testTimeout: 180_000 },
};
