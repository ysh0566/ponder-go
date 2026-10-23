import fs from "node:fs";
import path from "node:path";
const root = path.resolve(import.meta.dirname, ".."),
	upstream = path.resolve(root, "../ponder-upstream");
for (const packageName of ["client", "react"]) {
	const destination = path.join(
			root,
			"sdk/typescript",
			packageName,
			"node_modules",
		),
		source = path.join(upstream, "packages", packageName, "node_modules");
	if (!fs.existsSync(source))
		throw Error("Install the pinned baseline dependencies first.");
	if (fs.lstatSync(path.dirname(destination)).isSymbolicLink())
		throw Error("SDK directory must be local");
	if (
		fs.existsSync(destination) &&
		fs.lstatSync(destination).isSymbolicLink()
	) {
		if (
			path.resolve(path.dirname(destination), fs.readlinkSync(destination)) !==
			source
		)
			throw Error("Unexpected SDK dependency link; preserve it for review.");
		if (packageName === "client") continue;
		fs.unlinkSync(destination);
	}
	fs.mkdirSync(destination, { recursive: true });
	const link = (from, to) => {
		if (fs.existsSync(to)) return;
		fs.symlinkSync(from, to, "dir");
	};
	for (const entry of fs.readdirSync(source, { withFileTypes: true })) {
		const from = path.join(source, entry.name),
			to = path.join(destination, entry.name);
		if (entry.name.startsWith("@")) {
			fs.mkdirSync(to, { recursive: true });
			for (const name of fs.readdirSync(from))
				link(
					entry.name === "@ponder" && name === "client"
						? path.join(root, "sdk/typescript/client")
						: path.join(from, name),
					path.join(to, name),
				);
		} else link(from, to);
	}
}
console.log("SDK dependency links ready; tracked source files unchanged.");
