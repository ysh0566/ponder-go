// Transport isolation for the unmodified upstream oracle. Its Anvil proxy
// defaults to an IPv6 wildcard; bind wildcard Node listeners to loopback here.
import net from "node:net";
import fs from "node:fs";

const listen = net.Server.prototype.listen;
net.Server.prototype.listen = function (...args) {
	let original;
	if (args[0] && typeof args[0] === "object" && "port" in args[0]) {
		original = args[0].host;
		if (original === undefined || ["::", "0.0.0.0"].includes(original)) {
			args[0] = { ...args[0], host: "127.0.0.1" };
		}
	} else if (typeof args[0] === "number") {
		original = typeof args[1] === "string" ? args[1] : undefined;
		if (original === undefined) args.splice(1, 0, "127.0.0.1");
		else if (["::", "0.0.0.0"].includes(original)) args[1] = "127.0.0.1";
	} else {
		return listen.apply(this, args); // Unix-domain socket or handle.
	}
	const host = typeof args[0] === "object" ? args[0].host : args[1];
	if (!["127.0.0.1", "::1", "localhost"].includes(host)) {
		throw new Error(`Oracle listener must use loopback: ${host}`);
	}
	if (process.env.PONDER_BIND_AUDIT) {
		fs.appendFileSync(process.env.PONDER_BIND_AUDIT, `${JSON.stringify({ pid: process.pid, original: original ?? null, host })}\n`);
	}
	return listen.apply(this, args);
};
