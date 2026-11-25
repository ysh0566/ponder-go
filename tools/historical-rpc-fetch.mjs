// Issue one archive collection request with the same Fetch API as upstream viem.
import { readFileSync } from "node:fs";
import { pipeline } from "node:stream/promises";

const { url, body } = JSON.parse(readFileSync(0, "utf8"));
try {
	const response = await fetch(url, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body,
		signal: AbortSignal.timeout(45_000),
	});
	process.stdout.write(JSON.stringify({
		status: response.status,
		retryAfter: response.headers.get("Retry-After"),
	}) + "\n");
	if (response.ok && response.body) {
		await pipeline(response.body, process.stdout);
	} else {
		await response.body?.cancel();
	}
} catch (error) {
	if (error.name === "TimeoutError" || error.cause?.code === "UND_ERR_CONNECT_TIMEOUT") {
		process.exitCode = 124;
	} else if (["ECONNRESET", "UND_ERR_SOCKET"].includes(error.cause?.code)) {
		process.exitCode = 75;
	} else {
		process.exitCode = 1;
	}
}
