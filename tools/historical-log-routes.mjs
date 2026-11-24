// Load only in a replay of upstream tests whose RPC URLs are literal constants.
// The mapping changes the HTTP destination. Viem still encodes the request and
// handles the actual HTTP response from the selected loopback index.
import fs from "node:fs";

const routes = new Map();
for (const [source, endpoint] of Object.entries(JSON.parse(fs.readFileSync(process.env.PONDER_LOG_RPC_ROUTES, "utf8")))) {
  const from = new URL(source);
  const to = new URL(endpoint);
  if (from.protocol !== "https:" || to.protocol !== "http:" || to.hostname !== "127.0.0.1" ||
      to.username || to.password || !to.port || to.search || to.hash || routes.has(from.href)) {
    throw Error("Expected a unique source HTTPS URL and a loopback HTTP listener");
  }
  routes.set(from.href, to.href);
}

const fetchOriginal = globalThis.fetch;
globalThis.fetch = (input, init) => {
  const url = new URL(input instanceof Request ? input.url : input);
  const target = routes.get(url.href);
  if (target) return fetchOriginal(input instanceof Request ? new Request(target, input) : target, init);
  if (url.protocol === "http:" && url.hostname === "127.0.0.1") return fetchOriginal(input, init);
  throw Error("No historical RPC route for " + url.origin);
};
