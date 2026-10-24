// External draft. Do not apply to candidate until M29 has been accepted.
import fs from "node:fs";
import path from "node:path";
import { beforeEach, afterEach, afterAll, vi } from "vitest";
import * as source from "@/utils/sql-parse.js";

const root = process.env.PONDER_PORT_ROOT ?? path.resolve(import.meta.dirname, "..");
const ledger = JSON.parse(fs.readFileSync(path.join(root, "parity/test-ledger.json"), "utf8"));
const file = "packages/core/src/utils/sql-parse.test.ts";
const selected = ledger.tests.filter((t: any) => t.source_file === file);
if (selected.length !== 14 || selected.some((t: any) => t.type_error_directives.length)) {
  throw Error("Unexpected original SQL declarations/type obligations");
}
const names = ["parseSQLQuery", "getSQLQueryRelations", "validateAllowableSQLQuery"] as const;
const originals = Object.fromEntries(names.map(name => [name, source[name]]));
const cases: any[] = [];
let current: any;

beforeEach(ctx => {
  const record = selected.find((t: any) => t.title === ctx.task.name);
  if (!record || current) throw Error("Unexpected or concurrent source SQL test");
  current = Object.fromEntries(["id", "title", "source_file", "source_sha256", "full_name", "contexts", "test_source", "inputs", "assertions", "type_error_directives", "source_state", "modifiers", "parameter", "expansion_status"].map(key => [key, record[key]]));
  current.calls = [];
  for (const name of names) {
    vi.spyOn(source, name).mockImplementation(async (sql: string) => {
      const call: any = { operation: name, sql };
      current.calls.push(call);
      try {
        const value = await originals[name](sql);
        call.result = value === undefined ? { $undefined: true } : value instanceof Set ? { $set: [...value] } : value;
        return value;
      } catch (error: any) {
        call.error = { name: error.name, message: error.message };
        throw error;
      }
    });
  }
});

afterEach(() => {
  if (!current || !current.calls.length || current.calls.some((c: any) => !("result" in c) && !("error" in c))) {
    throw Error("Incomplete original SQL capture");
  }
  cases.push(current);
  current = undefined;
});

afterAll(() => {
  if (!process.env.PONDER_SQL_CAPTURE_OUTPUT || cases.length !== selected.length) throw Error("Missing complete source SQL capture");
  fs.writeFileSync(process.env.PONDER_SQL_CAPTURE_OUTPUT, JSON.stringify({
    baseline: ledger.baseline,
    source_file: file,
    method: "Complete original SQL test file, original setup/imports/Drizzle queries/assertions. Call-through export instrumentation records original SQL, result sets and errors without replacing behavior. Separate pristine original acceptance is required.",
    cases,
  }, null, 2) + "\n");
});
