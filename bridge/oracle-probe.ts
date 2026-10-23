import readline from "node:readline";
import {
	encodeCheckpoint,
	decodeCheckpoint,
} from "../../ponder-upstream/packages/core/src/utils/checkpoint.ts";
import {
	intervalUnion,
	intervalIntersection,
	intervalDifference,
	getChunks,
} from "../../ponder-upstream/packages/core/src/utils/interval.ts";
for await (const line of readline.createInterface({ input: process.stdin })) {
	try {
		const r = JSON.parse(line);
		let value;
		switch (r.op) {
			case "encode": {
				const c: any = {};
				for (const [key, v] of Object.entries(r.checkpoint))
					c[key] = key === "eventType" ? Number(v) : BigInt(v as string);
				value = encodeCheckpoint(c);
				break;
			}
			case "decode":
				value = decodeCheckpoint(r.encoded);
				break;
			case "union":
				value = intervalUnion(r.A);
				break;
			case "intersection":
				value = intervalIntersection(r.A, r.B);
				break;
			case "difference":
				value = intervalDifference(r.A, r.B);
				break;
			case "chunks":
				value = getChunks({ interval: r.Interval, maxChunkSize: r.Size });
				break;
			default:
				throw Error("unknown probe operation");
		}
		process.stdout.write(
			JSON.stringify({ value }, (_, v) =>
				typeof v === "bigint" ? v.toString() : v,
			) + "\n",
		);
	} catch (error) {
		process.stdout.write(
			JSON.stringify({ error: (error as Error).message }) + "\n",
		);
	}
}
