import path from "node:path";
import base from "./full-oracle.config.mjs";
export default {
  ...base,
  test: {
    ...base.test,
    setupFiles: [...(base.test.setupFiles ?? []), path.join(import.meta.dirname, "sql-capture.setup.ts")],
  },
};
