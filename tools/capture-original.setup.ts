// Record calls while the complete original file runs its unchanged assertions.
// This capture is separate from the pristine original backend acceptance.
import fs from "node:fs";
import path from "node:path";
import { beforeEach, afterEach, afterAll, vi } from "vitest";
import * as sourceEvents from "../../ponder-upstream/packages/core/src/runtime/events.ts";
import * as sourceFilters from "../../ponder-upstream/packages/core/src/runtime/filter.ts";
const root = path.resolve(import.meta.dirname, "..");
const ledger = JSON.parse(fs.readFileSync(`${root}/parity/test-ledger.json`, "utf8"));
const mode = process.env.PONDER_CAPTURE_MODE;
if (!["events", "filters"].includes(mode ?? "")) throw Error("Explicit capture mode required");
const file = `packages/core/src/runtime/${mode === "events" ? "events" : "filter"}.test.ts`;
const ids = mode === "events" ? ["a79cdf7b715b85a9f407", "a15552820b679aef4e63", "1ec7507f442eaaf13b93", "865433ec410487c54e12", "8300d175fc036ad366e1", "65aa38de0638bd7a31c5"] : ["d4a44fd18de58d842e60", "123f7747ecf98637dc07", "53007c4c2fcd23a21915", "8aa0ec9cda98fa1dccb6", "d36b571984e648e371c4", "f07738a89b1c2efd1eb0", "2b549b12673f747c4eeb", "1d570bf765cbf80ff44f", "ae73867bea12d4576440"];
const selected = ledger.tests.filter((t: any) => t.source_file === file && ids.includes(t.id));
if (selected.length !== (mode === "events" ? 6 : 9)) throw Error("Unexpected exact source selection");
const result: any[] = [];
let current: any;
function mark(value: any): any {
 if (value === undefined) return {$undefined:true};
 if (typeof value === "bigint") return {$bigint:String(value)};
 if (typeof value === "function") return {$function:true};
 if (value instanceof Map) return {$map:[...value.entries()].map(mark)};
 if (Array.isArray(value)) return value.map(mark);
 if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map(key => [key, mark(value[key])]));
 return value;
}
const target = mode === "events" ? sourceEvents : sourceFilters;
const names = mode === "events" ? ["decodeEvents", "buildEvents"] : ["isLogFactoryMatched", "isLogFilterMatched", "isTransactionFilterMatched", "isTransferFilterMatched", "isTraceFilterMatched", "mergeLogFiltersToRequests"];
const originals = Object.fromEntries(names.map(name => [name, (target as any)[name]]));
beforeEach(ctx => {
 const original = selected.find((t: any) => t.title === ctx.task.name);
 current = original ? Object.fromEntries(["id", "title", "source_file", "source_sha256", "full_name", "contexts", "test_source", "inputs", "assertions", "type_error_directives", "source_state", "modifiers", "parameter", "expansion_status"].map(key => [key, original[key]])) : undefined;
 if (current) current.calls = [];
 // restoreMocks is retained from the original config. Install fresh recorders
 // for each test, after automatic restoration, preserving the original calls.
 for (const name of names) vi.spyOn(target as any, name as any).mockImplementation((...args: any[]) => {
  if (!current) return originals[name](...args);
  const input = mark(name === "decodeEvents" ? [{$source_common_fixture:true}, ...args.slice(1)] : args);
  try {
   const value = originals[name](...args);
   current.calls.push({operation:name,args:input,args_after:mark(name === "decodeEvents" ? [{$source_common_fixture:true}, ...args.slice(1)] : args),result:mark(value)});
   return value;
  } catch (error: any) {
   current.calls.push({operation:name,args:input,error:{name:error.name,message:error.message}});
   throw error;
  }
 });
});
afterEach(() => {if (current) result.push(current); current = undefined;});
afterAll(() => {
 const destination = process.env.PONDER_CAPTURE_OUTPUT;
 if (!destination) throw Error("Explicit output required");
 if (result.length !== selected.length) throw Error("Missing complete source declarations");
 fs.writeFileSync(destination, JSON.stringify({baseline:ledger.baseline,method:"Complete original file, original globalSetup/helpers/generated ABI and original Vitest assertions; call-through instrumentation only. Captured fixtures are separate from pristine original acceptance. Every operation recorded before invocation; bigint/undefined/function/map representations explicit.",source_file:file,cases:result},null,2)+"\n");
});
