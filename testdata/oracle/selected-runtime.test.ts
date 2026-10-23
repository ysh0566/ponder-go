import fs from 'node:fs';import {beforeEach,afterEach,afterAll,describe,test,expect,vi} from 'vitest';
import * as viem from 'viem';import {mainnet} from 'viem/chains';
import {buildOptions} from '@/internal/options.js';import {createLogger} from '@/internal/logger.js';import {MetricsService} from '@/internal/metrics.js';import {createShutdown} from '@/internal/shutdown.js';
import {toLowerCase} from '@/utils/lowercase.js';import {ALICE,BOB} from '@/_test/constants.js';import {ZERO_CHECKPOINT_STRING} from '@/utils/checkpoint.js';
import * as predicates from '@/runtime/filter.js';import * as events from '@/runtime/events.js';import * as actions from '@/rpc/actions.js';import {createRpc as sourceCreateRpc} from '@/rpc/index.js';
const {createPublicClient,createTestClient,createWalletClient,parseEther,zeroAddress,toHex}=viem;
const endpoint=process.env.PONDER_SELECTED_RPC_URL!;if(!endpoint)throw Error('Explicit task RPC required');
// Endpoint adapter only: preserve source chain metadata, transport/helper bodies,
// no-mining behavior and all test bodies/expectations. Never bind shared8545.
const http=(url,...args)=>viem.http(url??endpoint,...args);
const createRpc=input=>sourceCreateRpc({...input,chain:{...input.chain,rpc:endpoint}});
const {defaultBlockFilterInclude,defaultTransactionFilterInclude,defaultTransferFilterInclude}=predicates;
const {debug_traceBlockByHash,eth_getBlockByNumber}=actions;
const context={};let current;const results=[];
function mark(v){if(v===undefined)return {$undefined:true};if(typeof v==='bigint')return {$bigint:String(v)};if(typeof v==='function')return {$function:true};if(v instanceof Map)return {$map:[...v.entries()].map(mark)};if(Array.isArray(v))return v.map(mark);if(v&&typeof v==='object')return Object.fromEntries(Object.keys(v).sort().map(k=>[k,mark(v[k])]));return v;}
function capture(name,fn){return (...args)=>{const input=mark(name==='decodeEvents'?[{$source_common_fixture:true},...args.slice(1)]:args);try{const result=fn(...args);current.calls.push({operation:name,args:input,result:mark(result)});return result;}catch(e){current.calls.push({operation:name,args:input,error:{name:e.name,message:e.message}});throw e;}};}
const isTransactionFilterMatched=capture('isTransactionFilterMatched',predicates.isTransactionFilterMatched),isTransferFilterMatched=capture('isTransferFilterMatched',predicates.isTransferFilterMatched),decodeEvents=capture('decodeEvents',events.decodeEvents),buildEvents=capture('buildEvents',events.buildEvents);
const metadata=[{"id":"671bbb20047840beee3c","source_file":"packages/core/src/runtime/events.test.ts","source_sha256":"2dcb977ece55d48934dbf6c15cf99f21109860710f5d4a7ee922ba030fad19a2","title":"decodeEvents() block","source_name":"decodeEvents() block","source_state":"active","contexts":[],"inputs":["{ common } = context","{ eventCallbacks } = getBlocksIndexingBuild({\n    interval: 1,\n  })","rawEvent = {\n    chainId: 1,\n    eventCallbackIndex: 0,\n    checkpoint: ZERO_CHECKPOINT_STRING,\n    block: {\n      number: 1n,\n    } as RawEvent[\"block\"],\n    transaction: undefined,\n    log: undefined,\n  } as RawEvent","events = decodeEvents(common, getChain(), eventCallbacks, [\n    rawEvent,\n  ]) as [BlockEvent]"],"assertions":[{"kind":"runtime","line":209,"text":"expect(events).toHaveLength(1)"},{"kind":"runtime","line":210,"text":"expect(events[0].event.block).toMatchObject({\n    number: 1n,\n  })"}],"test_source":"test(\"decodeEvents() block\", async () => {\n  const { common } = context;\n\n  const { eventCallbacks } = getBlocksIndexingBuild({\n    interval: 1,\n  });\n\n  const rawEvent = {\n    chainId: 1,\n    eventCallbackIndex: 0,\n    checkpoint: ZERO_CHECKPOINT_STRING,\n    block: {\n      number: 1n,\n    } as RawEvent[\"block\"],\n    transaction: undefined,\n    log: undefined,\n  } as RawEvent;\n\n  const events = decodeEvents(common, getChain(), eventCallbacks, [\n    rawEvent,\n  ]) as [BlockEvent];\n\n  expect(events).toHaveLength(1);\n  expect(events[0].event.block).toMatchObject({\n    number: 1n,\n  });\n})"},{"id":"f9af94ed6701e6a17149","source_file":"packages/core/src/runtime/events.test.ts","source_sha256":"2dcb977ece55d48934dbf6c15cf99f21109860710f5d4a7ee922ba030fad19a2","title":"decodeEvents() transfer","source_name":"decodeEvents() transfer","source_state":"active","contexts":[],"inputs":["{ common } = context","{ eventCallbacks } = getAccountsIndexingBuild({\n    address: ALICE,\n  })","rawEvent = {\n    chainId: 1,\n    eventCallbackIndex: 3,\n    checkpoint: ZERO_CHECKPOINT_STRING,\n    block: {} as RawEvent[\"block\"],\n    transaction: {} as RawEvent[\"transaction\"],\n    log: undefined,\n    trace: {\n      type: \"CALL\",\n      from: ALICE,\n      to: BOB,\n      gas: 0n,\n      gasUsed: 0n,\n      input: \"0x0\",\n      output: \"0x0\",\n      value: parseEther(\"1\"),\n      traceAddress: [],\n      blockNumber: 0,\n      transactionIndex: 0,\n    },\n  } as RawEvent","events = decodeEvents(common, getChain(), eventCallbacks, [\n    rawEvent,\n  ]) as [TransferEvent]"],"assertions":[{"kind":"runtime","line":248,"text":"expect(events).toHaveLength(1)"},{"kind":"runtime","line":249,"text":"expect(events[0].event.transfer).toMatchObject({\n    from: ALICE,\n    to: BOB,\n    value: parseEther(\"1\"),\n  })"}],"test_source":"test(\"decodeEvents() transfer\", async () => {\n  const { common } = context;\n\n  const { eventCallbacks } = getAccountsIndexingBuild({\n    address: ALICE,\n  });\n\n  const rawEvent = {\n    chainId: 1,\n    eventCallbackIndex: 3,\n    checkpoint: ZERO_CHECKPOINT_STRING,\n    block: {} as RawEvent[\"block\"],\n    transaction: {} as RawEvent[\"transaction\"],\n    log: undefined,\n    trace: {\n      type: \"CALL\",\n      from: ALICE,\n      to: BOB,\n      gas: 0n,\n      gasUsed: 0n,\n      input: \"0x0\",\n      output: \"0x0\",\n      value: parseEther(\"1\"),\n      traceAddress: [],\n      blockNumber: 0,\n      transactionIndex: 0,\n    },\n  } as RawEvent;\n\n  const events = decodeEvents(common, getChain(), eventCallbacks, [\n    rawEvent,\n  ]) as [TransferEvent];\n\n  expect(events).toHaveLength(1);\n  expect(events[0].event.transfer).toMatchObject({\n    from: ALICE,\n    to: BOB,\n    value: parseEther(\"1\"),\n  });\n})"},{"id":"23f4c8c913fdb10d1f9f","source_file":"packages/core/src/runtime/events.test.ts","source_sha256":"2dcb977ece55d48934dbf6c15cf99f21109860710f5d4a7ee922ba030fad19a2","title":"decodeEvents() transaction","source_name":"decodeEvents() transaction","source_state":"active","contexts":[],"inputs":["{ common } = context","{ eventCallbacks } = getAccountsIndexingBuild({\n    address: ALICE,\n  })","rawEvent = {\n    chainId: 1,\n    eventCallbackIndex: 0,\n    checkpoint: ZERO_CHECKPOINT_STRING,\n    block: {} as RawEvent[\"block\"],\n    transaction: {} as RawEvent[\"transaction\"],\n    log: undefined,\n    trace: undefined,\n  } as RawEvent","events = decodeEvents(common, getChain(), eventCallbacks, [\n    rawEvent,\n  ]) as [TransferEvent]"],"assertions":[{"kind":"runtime","line":277,"text":"expect(events).toHaveLength(1)"}],"test_source":"test(\"decodeEvents() transaction\", async () => {\n  const { common } = context;\n\n  const { eventCallbacks } = getAccountsIndexingBuild({\n    address: ALICE,\n  });\n\n  const rawEvent = {\n    chainId: 1,\n    eventCallbackIndex: 0,\n    checkpoint: ZERO_CHECKPOINT_STRING,\n    block: {} as RawEvent[\"block\"],\n    transaction: {} as RawEvent[\"transaction\"],\n    log: undefined,\n    trace: undefined,\n  } as RawEvent;\n\n  const events = decodeEvents(common, getChain(), eventCallbacks, [\n    rawEvent,\n  ]) as [TransferEvent];\n\n  expect(events).toHaveLength(1);\n})"},{"id":"51d9321079f0432f5258","source_file":"packages/core/src/runtime/events.test.ts","source_sha256":"2dcb977ece55d48934dbf6c15cf99f21109860710f5d4a7ee922ba030fad19a2","title":"buildEvents() transaction matches receipt by transaction index","source_name":"buildEvents() transaction matches receipt by transaction index","source_state":"active","contexts":[],"inputs":["{ eventCallbacks } = getAccountsIndexingBuild({ address: ALICE })","block = {\n    number: 1n,\n    timestamp: 1n,\n    hash: toHex(1, { size: 32 }),\n  } as InternalBlock","transaction = (transactionIndex: number) =>\n    ({\n      blockNumber: 1,\n      transactionIndex,\n      hash: toHex(transactionIndex, { size: 32 }),\n      from: BOB,\n      to: ALICE.toLowerCase(),\n      type: \"legacy\",\n    }) as InternalTransaction","receipt = (\n    transactionIndex: number,\n    status: InternalTransactionReceipt[\"status\"],\n  ) =>\n    ({\n      blockNumber: 1,\n      transactionIndex,\n      from: BOB,\n      to: ALICE.toLowerCase(),\n      status,\n    }) as InternalTransactionReceipt","build = (transactionReceipts: InternalTransactionReceipt[]) =>\n    buildEvents({\n      eventCallbacks,\n      blocks: [block],\n      logs: [],\n      transactions: [transaction(0), transaction(1)],\n      transactionReceipts,\n      traces: [],\n      childAddresses: new Map(),\n      chainId: 1,\n    })","receipts = [receipt(0, \"reverted\"), receipt(1, \"success\")]","events = build(receipts)"],"assertions":[{"kind":"runtime","line":459,"text":"expect(events).toHaveLength(1)"},{"kind":"runtime","line":460,"text":"expect(events[0]!.transaction!.transactionIndex).toBe(1)"},{"kind":"runtime","line":461,"text":"expect(events[0]!.transactionReceipt).toBe(receipts[1])"},{"kind":"runtime","line":463,"text":"expect(() => build([receipt(1, \"success\")])).toThrow(\n    \"Missing transaction receipt for block 1 and transaction index 0\",\n  )"},{"kind":"runtime","line":466,"text":"expect(() => build([receipt(0, \"success\")])).toThrow(\n    \"Missing transaction receipt for block 1 and transaction index 1\",\n  )"}],"test_source":"test(\"buildEvents() transaction matches receipt by transaction index\", async () => {\n  const { eventCallbacks } = getAccountsIndexingBuild({ address: ALICE });\n\n  const block = {\n    number: 1n,\n    timestamp: 1n,\n    hash: toHex(1, { size: 32 }),\n  } as InternalBlock;\n  const transaction = (transactionIndex: number) =>\n    ({\n      blockNumber: 1,\n      transactionIndex,\n      hash: toHex(transactionIndex, { size: 32 }),\n      from: BOB,\n      to: ALICE.toLowerCase(),\n      type: \"legacy\",\n    }) as InternalTransaction;\n  const receipt = (\n    transactionIndex: number,\n    status: InternalTransactionReceipt[\"status\"],\n  ) =>\n    ({\n      blockNumber: 1,\n      transactionIndex,\n      from: BOB,\n      to: ALICE.toLowerCase(),\n      status,\n    }) as InternalTransactionReceipt;\n\n  const build = (transactionReceipts: InternalTransactionReceipt[]) =>\n    buildEvents({\n      eventCallbacks,\n      blocks: [block],\n      logs: [],\n      transactions: [transaction(0), transaction(1)],\n      transactionReceipts,\n      traces: [],\n      childAddresses: new Map(),\n      chainId: 1,\n    });\n\n  const receipts = [receipt(0, \"reverted\"), receipt(1, \"success\")];\n  const events = build(receipts);\n  expect(events).toHaveLength(1);\n  expect(events[0]!.transaction!.transactionIndex).toBe(1);\n  expect(events[0]!.transactionReceipt).toBe(receipts[1]);\n\n  expect(() => build([receipt(1, \"success\")])).toThrow(\n    \"Missing transaction receipt for block 1 and transaction index 0\",\n  );\n  expect(() => build([receipt(0, \"success\")])).toThrow(\n    \"Missing transaction receipt for block 1 and transaction index 1\",\n  );\n})"}];
beforeEach(ctx=>{current={...metadata.find(t=>t.title===ctx.task.name),calls:[]};if(!current.id)throw Error('Unmapped selected declaration');});
afterEach(async ctx=>{current.instrumentation_after_each_state=ctx.task.result?.state??'unknown';results.push(current);await context.common?.shutdown.kill();});
afterAll(()=>fs.writeFileSync(process.env.PONDER_SELECTED_FIXTURE!,JSON.stringify({baseline:{"repository":"https://github.com/ponder-sh/ponder","revision":"17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334","core_version":"0.18.2"},method:'Selective unchanged source declarations and actual source helper implementations; loopback endpoint adaptation and existing Anvil0.2.0 explicitly recorded. Not the full original suite or Foundry1.8.5 acceptance.',sources:[{"file":"packages/core/src/_test/setup.ts","sha256":"266fd6d21510c2f90ea7b47d2631c2218eafe149b1f680acbefd07b502932e2d"},{"file":"packages/core/src/_test/utils.ts","sha256":"c82853764e17721ff80d6a669362ac6b6f6a17cfe634dd33fbac4fc97bff4d23"},{"file":"packages/core/src/_test/simulate.ts","sha256":"12df0b2c2c3ddf8353bc4e193de9fe88803742f367469951c27dfda9316fd501"},{"file":"packages/core/src/runtime/events.ts","sha256":"00c6728ed56c9f8a41bd427d5de4488d942e260558a4e4ce6eb98654946d7a8f"},{"file":"packages/core/src/runtime/filter.ts","sha256":"b9fe402a5a857bf0c309af808d7a04b1ac8052cfaacf7230f48425eb07ea4661"},{"file":"packages/core/src/rpc/actions.ts","sha256":"2d1093e132ac37651988322bb2c5407467c61e72daf19acb250d2ecf7533b7db"},{"file":"packages/core/src/rpc/index.ts","sha256":"30a43c79029f02d9cb9ab992dfabfe799be35162d750853b75b0de965aa3fb87"}],cases:results},null,2)+'\n'));
export function setupCommon() {
  const cliOptions = {
    command: "start",
    config: "",
    root: "",
    logLevel: "silent",
    logFormat: "pretty",
    version: "0.0.0",
  } as const;
  const options = buildOptions({ cliOptions });
  const logger = createLogger({ level: cliOptions.logLevel });
  const metrics = new MetricsService();
  const shutdown = createShutdown();
  context.common = {
    options,
    logger,
    metrics,
    shutdown,
    apiShutdown: shutdown,
    buildShutdown: shutdown,
  };
}
export function setupCleanup() {
  if (IS_BUN_TEST) {
    require("bun:test").afterEach(async () => {
      await context.common.shutdown.kill();
    });
    return;
  }

  return context.common.shutdown.kill;
}
export async function setupAnvil() {
  const emptySnapshotId = await testClient.snapshot();
  const cleanup = async () => {
    await testClient.revert({ id: emptySnapshotId });
  };

  if (IS_BUN_TEST) return require("bun:test").afterEach(cleanup);

  return cleanup;
}
export const TEST_POOL_ID = Number(process.env.VITEST_POOL_ID ?? 1);
export const IS_BUN_TEST = "bun" in process.versions;
export const anvil = {
  ...mainnet, // We are using a mainnet fork for testing.
  id: 1, // We configured our anvil instance to use `1` as the chain id (see `globalSetup.ts`);
  rpcUrls: {
    default: {
      http: [`http://127.0.0.1:8545/${TEST_POOL_ID}`],
      webSocket: [`ws://127.0.0.1:8545/${TEST_POOL_ID}`],
    },
    public: {
      http: [`http://127.0.0.1:8545/${TEST_POOL_ID}`],
      webSocket: [`ws://127.0.0.1:8545/${TEST_POOL_ID}`],
    },
  },
} as const satisfies ViemChain;
export const testClient = createTestClient({
  chain: anvil,
  mode: "anvil",
  transport: http(),
});
export const publicClient = createPublicClient({
  chain: anvil,
  transport: http(),
});
export const getBlocksIndexingBuild = (params: {
  interval: number;
}): {
  indexingFunctions: [IndexingFunctions[number]];
  eventCallbacks: [EventCallback];
} => {
  const eventCallbacks = [
    {
      filter: {
        type: "block",
        chainId: 1,
        sourceId: "Blocks",
        interval: params.interval,
        offset: 0,
        fromBlock: undefined,
        toBlock: undefined,
        hasTransactionReceipt: false,
        include: defaultBlockFilterInclude,
      },
      name: "Blocks:block",
      fn: vi.fn(),
      chain: getChain(),
      type: "block",
    },
  ] satisfies [EventCallback];

  const indexingFunctions = [{ name: "Blocks:block", fn: vi.fn() }] satisfies [
    IndexingFunctions[number],
  ];

  return { eventCallbacks, indexingFunctions };
};
export const getAccountsIndexingBuild = (params: {
  address: Address;
}): {
  eventCallbacks: [EventCallback, EventCallback, EventCallback, EventCallback];
  indexingFunctions: IndexingFunctions;
} => {
  const eventCallbacks = [
    {
      filter: {
        type: "transaction",
        chainId: 1,
        sourceId: "Accounts",
        fromAddress: undefined,
        toAddress: toLowerCase(params.address),
        fromBlock: undefined,
        toBlock: undefined,
        hasTransactionReceipt: true,
        include: defaultTransactionFilterInclude,
      } satisfies TransactionFilter,
      name: "Accounts:transaction:to",
      fn: vi.fn(),
      chain: getChain(),
      type: "account",
      direction: "to",
    },
    {
      filter: {
        type: "transaction",
        chainId: 1,
        sourceId: "Accounts",
        fromAddress: toLowerCase(params.address),
        toAddress: undefined,
        fromBlock: undefined,
        toBlock: undefined,
        hasTransactionReceipt: true,
        include: defaultTransactionFilterInclude,
      } satisfies TransactionFilter,
      name: "Accounts:transaction:from",
      fn: vi.fn(),
      chain: getChain(),
      type: "account",
      direction: "from",
    },
    {
      filter: {
        type: "transfer",
        chainId: 1,
        sourceId: "Accounts",
        fromAddress: undefined,
        toAddress: toLowerCase(params.address),
        fromBlock: undefined,
        toBlock: undefined,
        hasTransactionReceipt: false,
        include: defaultTransferFilterInclude,
      },
      name: "Accounts:transfer:to",
      fn: vi.fn(),
      chain: getChain(),
      type: "account",
      direction: "to",
    },
    {
      filter: {
        type: "transfer",
        chainId: 1,
        sourceId: "Accounts",
        fromAddress: toLowerCase(params.address),
        toAddress: undefined,
        fromBlock: undefined,
        toBlock: undefined,
        hasTransactionReceipt: false,
        include: defaultTransferFilterInclude,
      },
      name: "Accounts:transfer:from",
      fn: vi.fn(),
      chain: getChain(),
      type: "account",
      direction: "from",
    },
  ] satisfies [EventCallback, EventCallback, EventCallback, EventCallback];

  const indexingFunctions = [
    { name: "Accounts:transaction:from", fn: vi.fn() },
    { name: "Accounts:transaction:to", fn: vi.fn() },
    { name: "Accounts:transfer:from", fn: vi.fn() },
    { name: "Accounts:transfer:to", fn: vi.fn() },
  ] satisfies [
    IndexingFunctions[number],
    IndexingFunctions[number],
    IndexingFunctions[number],
    IndexingFunctions[number],
  ];

  return { eventCallbacks, indexingFunctions };
};
export const getChain = (params?: {
  reorgWindow?: number;
  cacheRpcRequests?: boolean;
}) => {
  return {
    name: "mainnet",
    id: 1,
    rpc: `http://127.0.0.1:8545/${TEST_POOL_ID}`,
    ws: undefined,
    pollingInterval: 1_000,
    reorgWindow: params?.reorgWindow ?? 1,
    cacheRpcRequests: params?.cacheRpcRequests ?? true,
    ethGetLogsBlockRange: undefined,
    viemChain: anvil,
  } satisfies Chain;
};
export const transferEth = async (params: {
  to: Address;
  amount: bigint;
  sender: Address;
}): Promise<{
  block: SyncBlock;
  transaction: SyncTransaction;
  transactionReceipt: SyncTransactionReceipt;
  trace: SyncTrace;
}> => {
  const walletClient = createWalletClient({
    chain: anvil,
    transport: http(),
    account: params.sender,
  });

  const hash = await walletClient.sendTransaction({
    to: params.to,
    value: params.amount,
  });

  await testClient.mine({ blocks: 1 });

  const block = await publicClient.request({
    method: "eth_getBlockByNumber",
    params: ["latest", true],
  });
  const receipt = await publicClient.request({
    method: "eth_getTransactionReceipt",
    params: [hash],
  });

  const [trace] = await debug_traceBlockByHash(publicClient as unknown as Rpc, [
    block!.hash!,
    { tracer: "callTracer" },
  ]);

  return {
    block: block! as SyncBlock,
    transaction: block!.transactions[0]! as SyncTransaction,
    transactionReceipt: receipt! as SyncTransactionReceipt,
    trace: trace!,
  };
};
describe('selected events',()=>{beforeEach(setupCommon);
test("decodeEvents() block", async () => {
  const { common } = context;

  const { eventCallbacks } = getBlocksIndexingBuild({
    interval: 1,
  });

  const rawEvent = {
    chainId: 1,
    eventCallbackIndex: 0,
    checkpoint: ZERO_CHECKPOINT_STRING,
    block: {
      number: 1n,
    } as RawEvent["block"],
    transaction: undefined,
    log: undefined,
  } as RawEvent;

  const events = decodeEvents(common, getChain(), eventCallbacks, [
    rawEvent,
  ]) as [BlockEvent];

  expect(events).toHaveLength(1);
  expect(events[0].event.block).toMatchObject({
    number: 1n,
  });
})
test("decodeEvents() transfer", async () => {
  const { common } = context;

  const { eventCallbacks } = getAccountsIndexingBuild({
    address: ALICE,
  });

  const rawEvent = {
    chainId: 1,
    eventCallbackIndex: 3,
    checkpoint: ZERO_CHECKPOINT_STRING,
    block: {} as RawEvent["block"],
    transaction: {} as RawEvent["transaction"],
    log: undefined,
    trace: {
      type: "CALL",
      from: ALICE,
      to: BOB,
      gas: 0n,
      gasUsed: 0n,
      input: "0x0",
      output: "0x0",
      value: parseEther("1"),
      traceAddress: [],
      blockNumber: 0,
      transactionIndex: 0,
    },
  } as RawEvent;

  const events = decodeEvents(common, getChain(), eventCallbacks, [
    rawEvent,
  ]) as [TransferEvent];

  expect(events).toHaveLength(1);
  expect(events[0].event.transfer).toMatchObject({
    from: ALICE,
    to: BOB,
    value: parseEther("1"),
  });
})
test("decodeEvents() transaction", async () => {
  const { common } = context;

  const { eventCallbacks } = getAccountsIndexingBuild({
    address: ALICE,
  });

  const rawEvent = {
    chainId: 1,
    eventCallbackIndex: 0,
    checkpoint: ZERO_CHECKPOINT_STRING,
    block: {} as RawEvent["block"],
    transaction: {} as RawEvent["transaction"],
    log: undefined,
    trace: undefined,
  } as RawEvent;

  const events = decodeEvents(common, getChain(), eventCallbacks, [
    rawEvent,
  ]) as [TransferEvent];

  expect(events).toHaveLength(1);
})
test("buildEvents() transaction matches receipt by transaction index", async () => {
  const { eventCallbacks } = getAccountsIndexingBuild({ address: ALICE });

  const block = {
    number: 1n,
    timestamp: 1n,
    hash: toHex(1, { size: 32 }),
  } as InternalBlock;
  const transaction = (transactionIndex: number) =>
    ({
      blockNumber: 1,
      transactionIndex,
      hash: toHex(transactionIndex, { size: 32 }),
      from: BOB,
      to: ALICE.toLowerCase(),
      type: "legacy",
    }) as InternalTransaction;
  const receipt = (
    transactionIndex: number,
    status: InternalTransactionReceipt["status"],
  ) =>
    ({
      blockNumber: 1,
      transactionIndex,
      from: BOB,
      to: ALICE.toLowerCase(),
      status,
    }) as InternalTransactionReceipt;

  const build = (transactionReceipts: InternalTransactionReceipt[]) =>
    buildEvents({
      eventCallbacks,
      blocks: [block],
      logs: [],
      transactions: [transaction(0), transaction(1)],
      transactionReceipts,
      traces: [],
      childAddresses: new Map(),
      chainId: 1,
    });

  const receipts = [receipt(0, "reverted"), receipt(1, "success")];
  const events = build(receipts);
  expect(events).toHaveLength(1);
  expect(events[0]!.transaction!.transactionIndex).toBe(1);
  expect(events[0]!.transactionReceipt).toBe(receipts[1]);

  expect(() => build([receipt(1, "success")])).toThrow(
    "Missing transaction receipt for block 1 and transaction index 0",
  );
  expect(() => build([receipt(0, "success")])).toThrow(
    "Missing transaction receipt for block 1 and transaction index 1",
  );
})
});
