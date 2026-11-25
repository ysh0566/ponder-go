import { spawnSync } from "node:child_process";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import { build } from "vite";
import dependencies from "./external-dependencies.json" with { type: "json" };

const sdk = import.meta.dirname;
const repo = path.resolve(sdk, "../../..");
const retained = path.join(repo, "parity/source-support/packages");
const testFile = "packages/core/src/index.test.ts";
const temp = fs.mkdtempSync(path.join(os.tmpdir(), "ponder-browser-"));
const core = path.join(temp, "core");
const require = createRequire(import.meta.url);
const [mode, report] = process.argv.slice(2);
try {
  // Keep the pinned package layout for relative imports and for the original
  // browser test. The published ES bundle has no dependency on this checkout.
  for (const name of ["core", "utils", "client"]) {
    const source = name === "client" ? path.join(sdk, "../client") : path.join(retained, name);
    fs.cpSync(path.join(source, "src"), path.join(temp, name, "src"), { recursive: true });
  }
  fs.symlinkSync(path.join(sdk, "node_modules"), path.join(temp, "node_modules"), "dir");
  fs.writeFileSync(path.join(core, "package.json"), JSON.stringify({ type: "module", dependencies }));
  if (mode === "--test") {
    fs.copyFileSync(path.join(repo, "parity/source-tests", testFile), path.join(core, "src/index.test.ts"));
    const reporter = report ? ["--reporter=json", "--outputFile=" + path.resolve(report)] : [];
    const vitest = path.join(path.dirname(require.resolve("vitest/package.json")), "vitest.mjs");
    const run = spawnSync(process.execPath, [vitest, "run", "--maxWorkers=1", ...reporter, "src/index.test.ts"], { cwd: core, stdio: "inherit" });
    if (run.error) throw run.error;
    process.exitCode = run.status ?? 1;
  } else if (mode === undefined) {
    await build({
      root: core,
      configFile: false,
      resolve: { alias: {
        "@": path.join(core, "src"),
        "@ponder/client": path.join(temp, "client/src"),
        "@ponder/utils": path.join(temp, "utils/src"),
      } },
      build: {
        lib: { entry: path.join(core, "src/index.ts"), name: "ponder", formats: ["es"], fileName: () => "index.js" },
        outDir: path.join(sdk, "dist"),
        emptyOutDir: true,
        minify: false,
        reportCompressedSize: false,
        rollupOptions: { external: Object.keys(dependencies).filter(name => !["@ponder/client", "@ponder/utils"].includes(name)) },
      },
    });
  } else {
    throw new Error("Usage: node build.mjs [--test [report.json]]");
  }
} finally {
  fs.rmSync(temp, { recursive: true, force: true });
}
