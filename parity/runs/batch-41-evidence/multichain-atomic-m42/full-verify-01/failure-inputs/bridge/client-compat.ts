// Exercise the ORIGINAL client source against the Go server, without changing it.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { createClient, eq } from "../sdk/typescript/client/src/index.ts";
const require = createRequire(
	new URL(
		"../../ponder-upstream/packages/client/package.json",
		import.meta.url,
	),
);
const { pgTable, text, customType } = require("drizzle-orm/pg-core");
const bigint = customType({
	dataType: () => "numeric(78)",
	fromDriver: (v: string) => BigInt(v),
});
const account = pgTable(process.argv[3] ?? "account", {
	id: text("id").primaryKey(),
	balance: bigint("balance"),
});
const base = process.argv[2];
const client = createClient(base + "/sql", { schema: { account } });
assert.deepEqual(await client.db.select().from(account), [
	{ id: "alice", balance: 7n },
]);
assert.deepEqual(
	await client.db.select().from(account).where(eq(account.balance, 7n)),
	[{ id: "alice", balance: 7n }],
);
assert.deepEqual(await client.getStatus(), {
	"1": { id: 1, block: { number: 1, timestamp: 1 } },
});
let count = 0,
	resolveData: (v: unknown) => void = () => {},
	rejectData: (e: unknown) => void = () => {};
const received = new Promise((resolve, reject) => {
	resolveData = resolve;
	rejectData = reject;
});
const live = client.live(
	(db) => db.select().from(account),
	(rows) => {
		count++;
		resolveData(rows);
	},
	rejectData,
);
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
await wait(100);
assert.equal(
	count,
	0,
	"initial evaluation must establish hash without emitting",
);
await fetch(base + "/test/change?balance=7");
await wait(100);
assert.equal(count, 0, "unchanged result must not emit");
await fetch(base + "/test/change?balance=9");
const timeout = setTimeout(() => rejectData(Error("SSE timeout")), 5000);
try {
	assert.deepEqual(await received, [{ id: "alice", balance: 9n }]);
	assert.equal(count, 1);
} finally {
	clearTimeout(timeout);
	live.unsubscribe();
}
console.log(
	JSON.stringify({
		client_source: "packages/client/src/index.ts",
		assertions: 7,
		result: "passed",
	}),
);
