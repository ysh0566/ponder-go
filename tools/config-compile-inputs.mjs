// Compile controls use the configuration supplied to the original safe builder.
// Keep those four small inputs in the checkout so a new run can create its own
// capture before it has produced a config-live-verification report.
import fs from "node:fs";

export const configCompileInputsFile = "config/testdata/compile/source-inputs.json";
const inputs = JSON.parse(fs.readFileSync(new URL("../" + configCompileInputsFile, import.meta.url), "utf8"));

export function configCompileInput(source) {
  const entry = inputs.cases.find(row => row.id === source?.id);
  if (!entry || source.source_file !== inputs.source_file ||
      entry.source_sha256 !== source.source_sha256 || entry.test_source !== source.test_source) {
    throw Error("Missing or stale original configuration for compiler control " + source?.id);
  }
  return entry.input;
}
