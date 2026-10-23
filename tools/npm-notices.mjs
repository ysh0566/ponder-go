import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
const root = path.resolve(import.meta.dirname, ".."),
	up = path.resolve(root, "../ponder-upstream"),
	seen = new Set(),
	records = [];
function visit(name, from) {
	const require = createRequire(from);
	let entry;
	try {
		entry = require.resolve(name);
	} catch {
		return;
	}
	let directory = path.dirname(entry),
		pkg;
	while (directory !== path.dirname(directory)) {
		const file = path.join(directory, "package.json");
		if (fs.existsSync(file)) {
			const value = JSON.parse(fs.readFileSync(file, "utf8"));
			if (value.name === name) {
				pkg = value;
				break;
			}
		}
		directory = path.dirname(directory);
	}
	if (!pkg) return;
	const key = `${pkg.name}@${pkg.version}`;
	if (seen.has(key)) return;
	seen.add(key);
	const record = {
		package: pkg.name,
		version: pkg.version,
		declared_license: pkg.license ?? null,
		files: [],
	};
	const dest = path.join(root, "licenses/npm", key.replaceAll("/", "__"));
	for (const file of fs.readdirSync(directory)) {
		if (
			/^(LICENSE|LICENCE|COPYING|NOTICE)/i.test(file) &&
			fs.statSync(path.join(directory, file)).isFile()
		) {
			fs.mkdirSync(dest, { recursive: true });
			fs.copyFileSync(path.join(directory, file), path.join(dest, file));
			record.files.push(path.relative(root, path.join(dest, file)));
		}
	}
	const standard = path.join(dest, "APACHE-2.0.txt");
	if (
		!record.files.length &&
		pkg.license === "Apache-2.0" &&
		fs.existsSync(standard)
	) {
		record.files.push(path.relative(root, standard));
		record.provenance =
			"Installed package omitted root license; standard license text from https://www.apache.org/licenses/LICENSE-2.0.txt";
	}
	record.status = record.files.length
		? "preserved"
		: "no_root_license_file_in_installed_package";
	records.push(record);
	for (const dependency of Object.keys(pkg.dependencies ?? {}))
		visit(dependency, path.join(directory, "package.json"));
}
for (const name of ["@electric-sql/pglite"])
	visit(name, path.join(up, "packages/core/package.json"));
for (const name of ["drizzle-orm", "eventsource", "superjson"])
	visit(name, path.join(root, "sdk/typescript/client/package.json"));
for (const name of ["react", "@tanstack/react-query"])
	visit(name, path.join(root, "sdk/typescript/react/package.json"));
fs.writeFileSync(
	path.join(root, "licenses/npm-manifest.json"),
	JSON.stringify(records, null, 2) + "\n",
);
console.log({
	node_license_records: records.length,
	missing_root_files: records.filter((x) => x.status !== "preserved").length,
});
