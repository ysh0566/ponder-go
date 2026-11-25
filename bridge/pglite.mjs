// Local PGlite adapter. SQL execution remains PostgreSQL, never SQLite.
import { createRequire } from "node:module";
import path from "node:path";
import readline from "node:readline";
const app = createRequire(path.resolve("package.json"));
const upstreamRoot = process.env.PONDER_UPSTREAM ??
 path.resolve(path.dirname(process.argv[1]), "../../ponder-upstream");
const upstream = createRequire(path.resolve(upstreamRoot, "packages/core/package.json"));
const adapter = process.env.PONDER_UPSTREAM
 ? upstream.resolve("@electric-sql/pglite")
 : app.resolve("@electric-sql/pglite", { paths: [process.cwd(), path.resolve(upstreamRoot, "packages/core")] });
const { PGlite } = app(adapter);
const db = new PGlite(process.argv[2] ?? "memory://");
await db.waitReady;
const output = (x) =>
	process.stdout.write(
		JSON.stringify(x, (_, v) => (typeof v === "bigint" ? v.toString() : v)) +
			"\n",
	);
// Keep notifications in distinct frames. Go owns the stdout reader and routes
// them independently of the request/response sequence, including during COMMIT.
db.onNotification((channel, payload) =>
	output({ notification: { channel, payload } }),
);
output({ ready: true, version: "0.2.13" });
for await (const line of readline.createInterface({ input: process.stdin })) {
	try {
		const r = JSON.parse(line);
		// Go JSON encodes []byte as base64. Restore only positions explicitly
		// tagged by the transport, so ordinary text/JSON parameters stay intact.
		for (const index of r.binaryParams ?? []) {
			if (r.params[index] !== null)
				r.params[index] = new Uint8Array(Buffer.from(r.params[index], "base64"));
		}
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
