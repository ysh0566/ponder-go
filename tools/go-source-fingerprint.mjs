import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { spawnSync } from "node:child_process";

const prefixes = ["abi/", "bloom/", "bridge/", "checkpoint/", "cmd/", "config/", "events/", "examples/", "factory/", "filter/", "finality/", "historical/", "indexing/", "internal/", "interval/", "orm/", "query/", "rpc/", "runtime/", "schema/", "sdk/", "sqlquery/", "store/", "testdata/", "third_party/"];
const individualInputs = new Set([
	"go.mod", "go.sum", "parity/sync-migration-assets.json",
	"tools/provider_archive_nodes.py", "tools/historical_log_rpc.py",
	"tools/rpcnode-fixtures/celo-10000000-10001000.json.gz",
	"tools/rpcnode-fixtures/historical-blast-zkevm.tar.gz",
	"tools/rpcnode-fixtures/moonriver-error.json",
	"tools/rpcnode-fixtures/tron-and-base-indexes.tar.gz", "tools/rpcnode-fixtures/tron-error.json",
]);

export function isNativeInput(file) {
	return individualInputs.has(file) || prefixes.some(prefix => file.startsWith(prefix));
}

export function sourceFingerprint(root) {
	const listed = spawnSync("git", ["ls-files", "-z", "--cached", "--others", "--exclude-standard"], { cwd: root, encoding: "utf8", maxBuffer: 8 * 1024 * 1024 });
	if (listed.error || listed.status !== 0) throw Error(`Cannot inventory native sources: ${listed.error?.message ?? listed.stderr}`);
	const files = [...new Set(listed.stdout.split("\0").filter(isNativeInput))].sort();
	const entries = files.map(file => [file, crypto.createHash("sha256").update(fs.readFileSync(path.join(root, file))).digest("hex")]);
	return { files: entries.length, sha256: crypto.createHash("sha256").update(JSON.stringify(entries)).digest("hex") };
}
