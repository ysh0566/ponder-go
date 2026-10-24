import path from "node:path";
import base from "./full-oracle.config.mjs";

// Retain original globalSetup/helpers/assertions; record calls separately.
export default {...base, test:{...base.test, setupFiles:[...(base.test.setupFiles??[]), path.join(import.meta.dirname,"capture-original.setup.ts")]}};
