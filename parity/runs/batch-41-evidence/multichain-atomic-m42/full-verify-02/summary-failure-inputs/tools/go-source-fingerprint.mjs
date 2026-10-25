import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { spawnSync } from "node:child_process";

const prefixes = ["abi/", "bloom/", "bridge/", "checkpoint/", "cmd/", "config/", "events/", "examples/", "factory/", "filter/", "finality/", "indexing/", "internal/", "interval/", "orm/", "query/", "rpc/", "runtime/", "schema/", "sdk/", "sqlquery/", "store/", "testdata/", "third_party/"];
export function sourceFingerprint(root) {
	const listed = spawnSync("git", ["ls-files", "-z", "--cached", "--others", "--exclude-standard"], { cwd: root, encoding: "utf8", maxBuffer: 8 * 1024 * 1024 });
	if (listed.error || listed.status !== 0) throw Error(`Cannot inventory native sources: ${listed.error?.message ?? listed.stderr}`);
	const files = [...new Set(listed.stdout.split("\0").filter(x => x && (prefixes.some(p => x.startsWith(p)) || ["go.mod", "go.sum", "parity/sync-migration-assets.json"].includes(x))))].sort();
	const entries = files.map(file => [file, crypto.createHash("sha256").update(fs.readFileSync(path.join(root, file))).digest("hex")]);
	return { files: entries.length, sha256: crypto.createHash("sha256").update(JSON.stringify(entries)).digest("hex") };
}
