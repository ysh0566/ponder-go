// Drive the deployed contracts and read the Go application with the pinned
// TypeScript SDK and the original project's Drizzle schema.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createRequire } = require("node:module");
const { setTimeout: delay } = require("node:timers/promises");

const [kind, core, rpc, url] = process.argv.slice(2);
process.chdir(core);
process.env.TSX_TSCONFIG_PATH = path.join(core, "tsconfig.json");
const source = createRequire(path.join(core, "package.json"));
source("tsx/cjs");
const { createClient } = source("../client/src/index.ts");
const schema = source(`./src/_test/e2e/${kind}/ponder.schema.ts`);
const { ALICE } = source("./src/_test/constants.ts");
const { createPublicClient, createTestClient, createWalletClient, http, parseEther, zeroAddress } = source("viem");
const { mainnet } = source("viem/chains");
const transport = http(rpc);
const wallet = createWalletClient({ account: ALICE, chain: mainnet, transport });
const chain = createPublicClient({ chain: mainnet, transport });
const anvil = createTestClient({ chain: mainnet, mode: "anvil", transport });
const client = createClient(url + "/sql", { schema });
function artifact(name) {
  return JSON.parse(fs.readFileSync(path.join(core, "src/_test/contracts/out", name + ".sol", name + ".json")));
}
async function mined(hash) {
  await anvil.mine({ blocks: 1 });
  const receipt = await chain.getTransactionReceipt({ hash });
  assert.equal(receipt.status, "success");
  return receipt;
}
async function deploy(contract, args = []) {
  const hash = await wallet.deployContract({ abi: contract.abi, bytecode: contract.bytecode.object, args });
  const { contractAddress } = await mined(hash);
  assert.equal(contractAddress.toLowerCase(), "0x5fbdb2315678afecb367f032d93f642f64180aa3");
  return contractAddress;
}
async function waitForBlock(number) {
  const end = Date.now() + 5000;
  while (Date.now() < end) {
    const status = await client.getStatus();
    if (status.mainnet?.block?.number >= number) return;
    await delay(20);
  }
  throw Error("Timed out waiting for indexed block " + number);
}
async function erc20() {
  const token = artifact("ERC20");
  const address = await deploy(token, ["name", "symbol", 18]);
  await mined(await wallet.writeContract({ address, abi: token.abi, functionName: "mint", args: [ALICE, parseEther("1")] }));
  await waitForBlock(2);
  const result = await client.db.select().from(schema.account);
  assert.deepEqual(result[0], { address: zeroAddress, balance: -(10n ** 18n) });
  assert.deepEqual(result[1], { address: ALICE.toLowerCase(), balance: 10n ** 18n });
}
async function factory() {
  const parent = artifact("Factory");
  const address = await deploy(parent);
  const { result: pair, request } = await chain.simulateContract({ address, abi: parent.abi, functionName: "createPair", account: ALICE });
  await mined(await wallet.writeContract(request));
  const swap = () => wallet.writeContract({ address: pair, abi: artifact("Pair").abi, functionName: "swap", args: [1n, 1n, ALICE] }).then(mined);
  await swap();
  await waitForBlock(3);
  const first = await client.db.select().from(schema.swapEvent);
  assert.equal(first.length, 1);
  assert.equal(typeof first[0].id, "string");
  assert.deepEqual(first[0], { id: first[0].id, from: ALICE.toLowerCase(), to: ALICE.toLowerCase(), pair: pair.toLowerCase() });
  await swap();
  await waitForBlock(4);
  const second = await client.db.select().from(schema.swapEvent);
  assert.equal(second.length, 2);
  assert.equal(new Set(second.map(row => row.id)).size, 2);
}
(kind === "erc20" ? erc20() : factory()).catch(err => { console.error(err); process.exitCode = 1; });
