// Prepare an observed copy in the disposable upstream checkout. The unmodified
// original is run separately. The added reads surround the original mutation.
import fs from "node:fs";
import path from "node:path";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
const here = path.resolve(import.meta.dirname, ".."),
	peer = path.resolve(process.env.PONDER_UPSTREAM);
const file = "packages/core/src/indexing-store/cache.test.ts",
	source = fs.readFileSync(path.join(peer, file), "utf8");
const ledger = JSON.parse(
	fs.readFileSync(path.join(here, "parity/test-ledger.json")),
);
const test = ledger.tests.find((t) => t.id === "07156f3c04010167ced9");
assert.equal(test.source_file, file);
assert.equal(
	createHash("sha256").update(source).digest("hex"),
	test.source_sha256,
);
let body = test.test_source;
function inject(anchor, insert) {
	assert.equal(body.split(anchor).length, 2);
	body = body.replace(anchor, insert);
}
inject(
	"indexingCache.event = event;",
	`
  const snapshot = (v) => JSON.parse(JSON.stringify(v,(_,value) => typeof value === "bigint" ? {$bigint:value.toString()} : value === undefined ? {$undefined:true} : value));
  const observed = {source_id:"07156f3c04010167ced9", type:event.type, checkpoint:event.checkpoint, callback:event.eventCallback.name, chain_id:event.chain.id, count:getEventCount(indexingFunctions)[event.eventCallback.name], address, alice:ALICE, bob:BOB, balance:parseEther("1").toString(), metadata:snapshot(event.eventCallback.abiItem), raw_log:snapshot(blockData.log), complete_before:indexingCache.has({table:schema.account,key:{address:BOB}}), before:snapshot(event.event), field_order:Object.keys(event.event.args)};
  indexingCache.event = event;`,
);
inject(
	"event.event.args.to = BOB;",
	`event.event.args.to = BOB;
    observed.after = snapshot(event.event);`,
);
inject(
	"expect(result).toBe(true);",
	`expect(result).toBe(true);
    observed.has = result;
    writeObserved(process.env.PONDER_CACHE_CAPTURE, JSON.stringify(observed,null,2)+"\\n", {flag:"wx"});`,
);
assert.equal(source.split(test.test_source).length, 2);
const out = path.join(
	path.dirname(path.join(peer, file)),
	"transfer-observation.test.ts",
);
fs.writeFileSync(
	out,
	'import {writeFileSync as writeObserved} from "node:fs";\n' +
		source.replace(test.test_source, body),
	{ flag: "wx" },
);
console.log(out);
