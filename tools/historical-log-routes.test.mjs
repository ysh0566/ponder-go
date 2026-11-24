import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import fs from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

test("route only configured requests and preserve viem's HTTP payload", async t => {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), "ponder-log-routes-"));
  t.after(() => fs.rm(temp, {recursive: true, force: true}));
  const seen = [];
  const listener = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    seen.push([req.method, req.headers["content-type"], Buffer.concat(chunks).toString()]);
    res.writeHead(200, {"Content-Type": "application/json"});
    res.end(JSON.stringify({jsonrpc: "2.0", id: 1, result: []}));
  });
  listener.listen(0, "127.0.0.1");
  await once(listener, "listening");
  t.after(() => new Promise(resolve => listener.close(resolve)));
  const routes = path.join(temp, "routes.json");
  const source = "https://upstream.invalid/test";
  await fs.writeFile(routes, JSON.stringify({[source]: "http://127.0.0.1:" + listener.address().port}));
  const body = JSON.stringify({jsonrpc:"2.0", id:1, method:"eth_getLogs", params:[{fromBlock:"0x0", toBlock:"0x1"}]});
  const program = `
    import assert from "node:assert/strict";
    const url = process.argv[1], opts = {method:"POST", headers:{"Content-Type":"application/json"}, body:process.argv[2]};
    for (const response of [await fetch(url, opts), await fetch(new Request(url, opts))]) {
      assert.deepEqual((await response.json()).result, []);
    }
    await assert.rejects(async () => fetch(url + "/other", opts), /No historical RPC route/);
  `;
  const child = spawn(process.execPath, ["--import", fileURLToPath(new URL("./historical-log-routes.mjs", import.meta.url)),
    "--input-type=module", "-e", program, source, body], {env:{...process.env, PONDER_LOG_RPC_ROUTES:routes}, stdio:"inherit"});
  const [code] = await once(child, "exit");
  assert.equal(code, 0);
  assert.deepEqual(seen, [["POST","application/json",body], ["POST","application/json",body]]);
});
