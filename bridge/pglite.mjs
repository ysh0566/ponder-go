// Local PGlite adapter. SQL execution remains PostgreSQL, never SQLite.
import { createRequire } from "node:module";
import path from "node:path";
import readline from "node:readline";
const require = createRequire(
	path.resolve(
		process.env.PONDER_UPSTREAM ??
			path.join(import.meta.dirname, "../../ponder-upstream"),
		"packages/core/package.json",
	),
);
const { PGlite } = require("@electric-sql/pglite");
const db = new PGlite(process.argv[2] ?? "memory://");
await db.waitReady;
const output = (x) =>
	process.stdout.write(
		JSON.stringify(x, (_, v) => (typeof v === "bigint" ? v.toString() : v)) +
			"\n",
	);
output({ ready: true, version: "0.2.13" });
for await (const line of readline.createInterface({ input: process.stdin })) {
	try {
		const r = JSON.parse(line);
		if (r.action === "close") {
			await db.close();
			output({ rows: [] });
			break;
		}
		if (r.action === "exec") {
			await db.exec(r.sql);
			output({ rows: [] });
		} else {
			const result = await db.query(r.sql, r.params ?? []);
			output(result);
		}
	} catch (error) {
		output({ error: { message: error.message, code: error.code } });
	}
}
