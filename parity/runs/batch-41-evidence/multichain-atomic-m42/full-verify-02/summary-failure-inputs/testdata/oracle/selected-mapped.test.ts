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
const metadata=[{"id":"a196548313391b06c82d","source_file":"packages/core/src/runtime/events.test.ts","source_sha256":"2dcb977ece55d48934dbf6c15cf99f21109860710f5d4a7ee922ba030fad19a2","title":"splitEvents()","source_name":"splitEvents()","source_state":"active","contexts":[],"inputs":["events = [\n    {\n      chain: { id: 1 },\n      checkpoint: \"0\",\n      event: {\n        block: {\n          hash: \"0x1\",\n          timestamp: 1,\n          number: 1n,\n        },\n      },\n    },\n    {\n      chain: { id: 1 },\n      checkpoint: \"0\",\n      event: {\n        block: {\n          hash: \"0x2\",\n          timestamp: 2,\n          number: 2n,\n        },\n      },\n    },\n  ] as unknown as Event[]","result = splitEvents(events)"],"assertions":[{"kind":"runtime","line":68,"text":"expect(result).toMatchInlineSnapshot(`\n    [\n      {\n        \"chainId\": 1,\n        \"checkpoint\": \"000000000100000000000000010000000000000001999999999999999999999999999999999\",\n        \"events\": [\n          {\n            \"chain\": {\n              \"id\": 1,\n            },\n            \"checkpoint\": \"0\",\n            \"event\": {\n              \"block\": {\n                \"hash\": \"0x1\",\n                \"number\": 1n,\n                \"timestamp\": 1,\n              },\n            },\n          },\n        ],\n      },\n      {\n        \"chainId\": 1,\n        \"checkpoint\": \"000000000200000000000000010000000000000002999999999999999999999999999999999\",\n        \"events\": [\n          {\n            \"chain\": {\n              \"id\": 1,\n            },\n            \"checkpoint\": \"0\",\n            \"event\": {\n              \"block\": {\n                \"hash\": \"0x2\",\n                \"number\": 2n,\n                \"timestamp\": 2,\n              },\n            },\n          },\n        ],\n      },\n    ]\n  `)"}],"test_source":"test(\"splitEvents()\", async () => {\n  const events = [\n    {\n      chain: { id: 1 },\n      checkpoint: \"0\",\n      event: {\n        block: {\n          hash: \"0x1\",\n          timestamp: 1,\n          number: 1n,\n        },\n      },\n    },\n    {\n      chain: { id: 1 },\n      checkpoint: \"0\",\n      event: {\n        block: {\n          hash: \"0x2\",\n          timestamp: 2,\n          number: 2n,\n        },\n      },\n    },\n  ] as unknown as Event[];\n\n  const result = splitEvents(events);\n\n  expect(result).toMatchInlineSnapshot(`\n    [\n      {\n        \"chainId\": 1,\n        \"checkpoint\": \"000000000100000000000000010000000000000001999999999999999999999999999999999\",\n        \"events\": [\n          {\n            \"chain\": {\n              \"id\": 1,\n            },\n            \"checkpoint\": \"0\",\n            \"event\": {\n              \"block\": {\n                \"hash\": \"0x1\",\n                \"number\": 1n,\n                \"timestamp\": 1,\n              },\n            },\n          },\n        ],\n      },\n      {\n        \"chainId\": 1,\n        \"checkpoint\": \"000000000200000000000000010000000000000002999999999999999999999999999999999\",\n        \"events\": [\n          {\n            \"chain\": {\n              \"id\": 1,\n            },\n            \"checkpoint\": \"0\",\n            \"event\": {\n              \"block\": {\n                \"hash\": \"0x2\",\n                \"number\": 2n,\n                \"timestamp\": 2,\n              },\n            },\n          },\n        ],\n      },\n    ]\n  `);\n})"},{"id":"dcb5e73908e894a2b815","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"getChildAddress() topics","source_name":"getChildAddress() topics","source_state":"active","contexts":[],"inputs":["factory = {\n    type: \"log\",\n    childAddressLocation: \"topic1\",\n  } as unknown as LogFactory","log = {\n    topics: [\n      null,\n      \"0x000000000000000000000000a21a16ec22a940990922220e4ab5bf4c2310f556\",\n    ],\n  } as unknown as SyncLog"],"assertions":[{"kind":"runtime","line":71,"text":"expect(getChildAddress({ log, factory })).toBe(\n    \"0xa21a16ec22a940990922220e4ab5bf4c2310f556\",\n  )"}],"test_source":"test(\"getChildAddress() topics\", () => {\n  const factory = {\n    type: \"log\",\n    childAddressLocation: \"topic1\",\n  } as unknown as LogFactory;\n  const log = {\n    topics: [\n      null,\n      \"0x000000000000000000000000a21a16ec22a940990922220e4ab5bf4c2310f556\",\n    ],\n  } as unknown as SyncLog;\n\n  expect(getChildAddress({ log, factory })).toBe(\n    \"0xa21a16ec22a940990922220e4ab5bf4c2310f556\",\n  );\n})"},{"id":"8b843d8b055a534d8600","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"getChildAddress() offset","source_name":"getChildAddress() offset","source_state":"active","contexts":[],"inputs":["factory = {\n    type: \"log\",\n    childAddressLocation: \"offset32\",\n  } as unknown as LogFactory","log = {\n    data: \"0x0000000000000000000000000000000000000000000000000000000017d435c9000000000000000000000000a21a16ec22a940990922220e4ab5bf4c2310f556\",\n  } as unknown as SyncLog"],"assertions":[{"kind":"runtime","line":85,"text":"expect(getChildAddress({ log, factory })).toBe(\n    \"0xa21a16ec22a940990922220e4ab5bf4c2310f556\",\n  )"}],"test_source":"test(\"getChildAddress() offset\", () => {\n  const factory = {\n    type: \"log\",\n    childAddressLocation: \"offset32\",\n  } as unknown as LogFactory;\n  const log = {\n    data: \"0x0000000000000000000000000000000000000000000000000000000017d435c9000000000000000000000000a21a16ec22a940990922220e4ab5bf4c2310f556\",\n  } as unknown as SyncLog;\n\n  expect(getChildAddress({ log, factory })).toBe(\n    \"0xa21a16ec22a940990922220e4ab5bf4c2310f556\",\n  );\n})"},{"id":"ad3050b801ac9f9e4a4b","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"isBlockFilterMatched","source_name":"isBlockFilterMatched","source_state":"active","contexts":[],"inputs":["chain = getChain()","rpc = createRpc({\n    chain,\n    common: context.common,\n  })","{ eventCallbacks } = getBlocksIndexingBuild({\n    interval: 1,\n  })","filter = eventCallbacks[0]!.filter as BlockFilter","rpcBlock = await eth_getBlockByNumber(rpc, [\"0x0\", true])","isMatched = isBlockFilterMatched({\n    filter,\n    block: rpcBlock,\n  })"],"assertions":[{"kind":"runtime","line":177,"text":"expect(isMatched).toBe(true)"},{"kind":"runtime","line":186,"text":"expect(isMatched).toBe(false)"}],"test_source":"test(\"isBlockFilterMatched\", async () => {\n  const chain = getChain();\n  const rpc = createRpc({\n    chain,\n    common: context.common,\n  });\n\n  const { eventCallbacks } = getBlocksIndexingBuild({\n    interval: 1,\n  });\n\n  const filter = eventCallbacks[0]!.filter as BlockFilter;\n\n  const rpcBlock = await eth_getBlockByNumber(rpc, [\"0x0\", true]);\n\n  let isMatched = isBlockFilterMatched({\n    filter,\n    block: rpcBlock,\n  });\n  expect(isMatched).toBe(true);\n\n  filter.interval = 2;\n  filter.offset = 1;\n\n  isMatched = isBlockFilterMatched({\n    filter,\n    block: rpcBlock,\n  });\n  expect(isMatched).toBe(false);\n})"},{"id":"81d93c6efbaaa64aa4e8","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest returns undefined for no inputs","source_name":"mergeLogFiltersToBlockRequest returns undefined for no inputs","source_state":"active","contexts":[],"inputs":[],"assertions":[{"kind":"runtime","line":548,"text":"expect(mergeLogFiltersToBlockRequest({ logFilters: [], factories: [] })).toBe(\n    undefined,\n  )"}],"test_source":"test(\"mergeLogFiltersToBlockRequest returns undefined for no inputs\", () => {\n  expect(mergeLogFiltersToBlockRequest({ logFilters: [], factories: [] })).toBe(\n    undefined,\n  );\n})"},{"id":"297ff86a54e9010cbb83","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest returns one log filter","source_name":"mergeLogFiltersToBlockRequest returns one log filter","source_state":"active","contexts":[],"inputs":["filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic2: REQUEST_TOPIC_B,\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":561,"text":"expect(\n    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_A],\n    topics: [[REQUEST_TOPIC_A], null, [REQUEST_TOPIC_B]],\n  })"}],"test_source":"test(\"mergeLogFiltersToBlockRequest returns one log filter\", () => {\n  const filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic2: REQUEST_TOPIC_B,\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_A],\n    topics: [[REQUEST_TOPIC_A], null, [REQUEST_TOPIC_B]],\n  });\n})"},{"id":"bc00255fef95d9272b16","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest unions addresses and topics","source_name":"mergeLogFiltersToBlockRequest unions addresses and topics","source_state":"active","contexts":[],"inputs":["filterA = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic1: REQUEST_TOPIC_C,\n  } satisfies LogFilter","filterB = {\n    ...EMPTY_LOG_FILTER,\n    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],\n    topic0: REQUEST_TOPIC_B,\n    topic1: [REQUEST_TOPIC_C, REQUEST_TOPIC_A],\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":583,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filterA, filterB],\n      factories: [],\n    }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],\n    topics: [\n      [REQUEST_TOPIC_A, REQUEST_TOPIC_B],\n      [REQUEST_TOPIC_C, REQUEST_TOPIC_A],\n    ],\n  })"}],"test_source":"test(\"mergeLogFiltersToBlockRequest unions addresses and topics\", () => {\n  const filterA = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic1: REQUEST_TOPIC_C,\n  } satisfies LogFilter;\n  const filterB = {\n    ...EMPTY_LOG_FILTER,\n    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],\n    topic0: REQUEST_TOPIC_B,\n    topic1: [REQUEST_TOPIC_C, REQUEST_TOPIC_A],\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filterA, filterB],\n      factories: [],\n    }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],\n    topics: [\n      [REQUEST_TOPIC_A, REQUEST_TOPIC_B],\n      [REQUEST_TOPIC_C, REQUEST_TOPIC_A],\n    ],\n  });\n})"},{"id":"3c2b8cc0a9fc66f15c4a","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest matches all values if one input matches all values","source_name":"mergeLogFiltersToBlockRequest matches all values if one input matches all values","source_state":"active","contexts":[],"inputs":["filterA = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic1: REQUEST_TOPIC_C,\n  } satisfies LogFilter","filterB = {\n    ...EMPTY_LOG_FILTER,\n    address: undefined,\n    topic0: REQUEST_TOPIC_B,\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":610,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filterA, filterB],\n      factories: [],\n    }),\n  ).toStrictEqual({\n    address: undefined,\n    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_B]],\n  })"}],"test_source":"test(\"mergeLogFiltersToBlockRequest matches all values if one input matches all values\", () => {\n  const filterA = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic1: REQUEST_TOPIC_C,\n  } satisfies LogFilter;\n  const filterB = {\n    ...EMPTY_LOG_FILTER,\n    address: undefined,\n    topic0: REQUEST_TOPIC_B,\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filterA, filterB],\n      factories: [],\n    }),\n  ).toStrictEqual({\n    address: undefined,\n    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_B]],\n  });\n})"},{"id":"87aa9c053835c17a0bdf","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest matches all addresses for a factory log filter","source_name":"mergeLogFiltersToBlockRequest matches all addresses for a factory log filter","source_state":"active","contexts":[],"inputs":["filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_FACTORY,\n    topic0: REQUEST_TOPIC_A,\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":628,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filter],\n      factories: [REQUEST_FACTORY],\n    }),\n  ).toStrictEqual({\n    address: undefined,\n    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],\n  })"}],"test_source":"test(\"mergeLogFiltersToBlockRequest matches all addresses for a factory log filter\", () => {\n  const filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_FACTORY,\n    topic0: REQUEST_TOPIC_A,\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filter],\n      factories: [REQUEST_FACTORY],\n    }),\n  ).toStrictEqual({\n    address: undefined,\n    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],\n  });\n})"},{"id":"063b2ef6ce1710b5e907","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest includes factories without log filters","source_name":"mergeLogFiltersToBlockRequest includes factories without log filters","source_state":"active","contexts":[],"inputs":[],"assertions":[{"kind":"runtime","line":640,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [],\n      factories: [REQUEST_FACTORY],\n    }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_B],\n    topics: [[REQUEST_TOPIC_C]],\n  })"}],"test_source":"test(\"mergeLogFiltersToBlockRequest includes factories without log filters\", () => {\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [],\n      factories: [REQUEST_FACTORY],\n    }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_B],\n    topics: [[REQUEST_TOPIC_C]],\n  });\n})"},{"id":"10f0068f97080c079808","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest unions factory and log filter addresses","source_name":"mergeLogFiltersToBlockRequest unions factory and log filter addresses","source_state":"active","contexts":[],"inputs":["filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic1: REQUEST_TOPIC_B,\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":659,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filter],\n      factories: [REQUEST_FACTORY],\n    }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],\n    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],\n  })"}],"test_source":"test(\"mergeLogFiltersToBlockRequest unions factory and log filter addresses\", () => {\n  const filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic1: REQUEST_TOPIC_B,\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filter],\n      factories: [REQUEST_FACTORY],\n    }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],\n    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],\n  });\n})"},{"id":"60183027e2d8b2084d58","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest matches all addresses for a factory without an address","source_name":"mergeLogFiltersToBlockRequest matches all addresses for a factory without an address","source_state":"active","contexts":[],"inputs":["filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":677,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filter],\n      factories: [{ ...REQUEST_FACTORY, address: undefined }],\n    }),\n  ).toStrictEqual({\n    address: undefined,\n    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],\n  })"}],"test_source":"test(\"mergeLogFiltersToBlockRequest matches all addresses for a factory without an address\", () => {\n  const filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filter],\n      factories: [{ ...REQUEST_FACTORY, address: undefined }],\n    }),\n  ).toStrictEqual({\n    address: undefined,\n    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],\n  });\n})"},{"id":"163687e53f937f38bdc3","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest ignores inputs with an empty address list","source_name":"mergeLogFiltersToBlockRequest ignores inputs with an empty address list","source_state":"active","contexts":[],"inputs":["filter = {\n    ...EMPTY_LOG_FILTER,\n    address: [],\n    topic0: REQUEST_TOPIC_A,\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":695,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filter],\n      factories: [{ ...REQUEST_FACTORY, address: [] }],\n    }),\n  ).toBe(undefined)"}],"test_source":"test(\"mergeLogFiltersToBlockRequest ignores inputs with an empty address list\", () => {\n  const filter = {\n    ...EMPTY_LOG_FILTER,\n    address: [],\n    topic0: REQUEST_TOPIC_A,\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [filter],\n      factories: [{ ...REQUEST_FACTORY, address: [] }],\n    }),\n  ).toBe(undefined);\n})"},{"id":"2f13a001ab8416f18cce","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest matches all addresses above ADDRESS_CHUNK_SIZE","source_name":"mergeLogFiltersToBlockRequest matches all addresses above ADDRESS_CHUNK_SIZE","source_state":"active","contexts":[],"inputs":["addresses = Array.from(\n    { length: ADDRESS_CHUNK_SIZE + 1 },\n    (_, i) => `0x${i.toString(16).padStart(40, \"0\")}` as Address,\n  )","filter = {\n    ...EMPTY_LOG_FILTER,\n    address: addresses,\n    topic0: REQUEST_TOPIC_A,\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":714,"text":"expect(\n    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),\n  ).toStrictEqual({ address: undefined, topics: [[REQUEST_TOPIC_A]] })"},{"kind":"runtime","line":718,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [{ ...filter, address: addresses.slice(1) }],\n      factories: [],\n    })?.address,\n  ).toHaveLength(ADDRESS_CHUNK_SIZE)"}],"test_source":"test(\"mergeLogFiltersToBlockRequest matches all addresses above ADDRESS_CHUNK_SIZE\", () => {\n  const addresses = Array.from(\n    { length: ADDRESS_CHUNK_SIZE + 1 },\n    (_, i) => `0x${i.toString(16).padStart(40, \"0\")}` as Address,\n  );\n  const filter = {\n    ...EMPTY_LOG_FILTER,\n    address: addresses,\n    topic0: REQUEST_TOPIC_A,\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),\n  ).toStrictEqual({ address: undefined, topics: [[REQUEST_TOPIC_A]] });\n\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: [{ ...filter, address: addresses.slice(1) }],\n      factories: [],\n    })?.address,\n  ).toHaveLength(ADDRESS_CHUNK_SIZE);\n})"},{"id":"68d640b52be58334d6d2","source_file":"packages/core/src/runtime/filter.test.ts","source_sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b","title":"mergeLogFiltersToBlockRequest matches all topics above ADDRESS_CHUNK_SIZE","source_name":"mergeLogFiltersToBlockRequest matches all topics above ADDRESS_CHUNK_SIZE","source_state":"active","contexts":[],"inputs":["topics = Array.from(\n    { length: ADDRESS_CHUNK_SIZE + 1 },\n    (_, i) => `0x${i.toString(16).padStart(64, \"0\")}` as Hex,\n  )","filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic1: topics,\n  } satisfies LogFilter"],"assertions":[{"kind":"runtime","line":738,"text":"expect(\n    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_A],\n    topics: [[REQUEST_TOPIC_A]],\n  })"},{"kind":"runtime","line":745,"text":"expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: topics.map((topic0) => ({ ...filter, topic0, topic1: null })),\n      factories: [],\n    }),\n  ).toStrictEqual({ address: [REQUEST_ADDRESS_A], topics: undefined })"}],"test_source":"test(\"mergeLogFiltersToBlockRequest matches all topics above ADDRESS_CHUNK_SIZE\", () => {\n  const topics = Array.from(\n    { length: ADDRESS_CHUNK_SIZE + 1 },\n    (_, i) => `0x${i.toString(16).padStart(64, \"0\")}` as Hex,\n  );\n  const filter = {\n    ...EMPTY_LOG_FILTER,\n    address: REQUEST_ADDRESS_A,\n    topic0: REQUEST_TOPIC_A,\n    topic1: topics,\n  } satisfies LogFilter;\n\n  expect(\n    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),\n  ).toStrictEqual({\n    address: [REQUEST_ADDRESS_A],\n    topics: [[REQUEST_TOPIC_A]],\n  });\n\n  expect(\n    mergeLogFiltersToBlockRequest({\n      logFilters: topics.map((topic0) => ({ ...filter, topic0, topic1: null })),\n      factories: [],\n    }),\n  ).toStrictEqual({ address: [REQUEST_ADDRESS_A], topics: undefined });\n})"}];
beforeEach(ctx=>{current={...metadata.find(t=>t.title===ctx.task.name),calls:[]};if(!current.id)throw Error('Unmapped selected declaration');});
afterEach(async ctx=>{current.instrumentation_after_each_state=ctx.task.result?.state??'unknown';results.push(current);await context.common?.shutdown.kill();});
afterAll(()=>fs.writeFileSync(process.env.PONDER_SELECTED_FIXTURE!,JSON.stringify({baseline:{"repository":"https://github.com/ponder-sh/ponder","revision":"17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334","core_version":"0.18.2"},method:'Selective unchanged source declarations and actual source helper implementations; loopback endpoint adaptation; actual Anvil version is recorded in parity/runs/tool-versions.txt. This selective run is distinct from complete original-file acceptance.',sources:[{"file":"packages/core/src/_test/setup.ts","sha256":"266fd6d21510c2f90ea7b47d2631c2218eafe149b1f680acbefd07b502932e2d"},{"file":"packages/core/src/_test/utils.ts","sha256":"c82853764e17721ff80d6a669362ac6b6f6a17cfe634dd33fbac4fc97bff4d23"},{"file":"packages/core/src/_test/simulate.ts","sha256":"12df0b2c2c3ddf8353bc4e193de9fe88803742f367469951c27dfda9316fd501"},{"file":"packages/core/src/runtime/events.ts","sha256":"00c6728ed56c9f8a41bd427d5de4488d942e260558a4e4ce6eb98654946d7a8f"},{"file":"packages/core/src/runtime/filter.ts","sha256":"b9fe402a5a857bf0c309af808d7a04b1ac8052cfaacf7230f48425eb07ea4661"},{"file":"packages/core/src/rpc/actions.ts","sha256":"2d1093e132ac37651988322bb2c5407467c61e72daf19acb250d2ecf7533b7db"},{"file":"packages/core/src/rpc/index.ts","sha256":"30a43c79029f02d9cb9ab992dfabfe799be35162d750853b75b0de965aa3fb87"},{"file":"packages/core/src/runtime/filter.test.ts","sha256":"5e0f0ea0b651c6b795ec3558e768836c8045dced6b16933ee9dbb1ee5a4f177b"}],cases:results},null,2)+'\n'));
import {EMPTY_LOG_FILTER} from '@/_test/constants.js';const {zeroHash}=viem;const {ADDRESS_CHUNK_SIZE}=actions;const {getChildAddress,isBlockFilterMatched,mergeLogFiltersToBlockRequest}=predicates;const {splitEvents}=events;const ADDRESS_A: Address = "0x1111111111111111111111111111111111111111";
const ADDRESS_B: Address = "0x2222222222222222222222222222222222222222";
const TOPIC1_ALICE: Hex = `0x${"0".repeat(24)}${ALICE.slice(2)}`;
const TOPIC1_BOB: Hex = `0x${"0".repeat(24)}${BOB.slice(2)}`;
const baseLogFilter = {
  type: "log",
  chainId: 1,
  sourceId: "Erc20",
  fromBlock: undefined,
  toBlock: undefined,
  hasTransactionReceipt: false,
  include: [] as LogFilter["include"],
};
const REQUEST_ADDRESS_A = "0x000000000000000000000000000000000000000a";
const REQUEST_ADDRESS_B = "0x000000000000000000000000000000000000000b";
const REQUEST_TOPIC_A =
  "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const REQUEST_TOPIC_B =
  "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
const REQUEST_TOPIC_C =
  "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
const REQUEST_FACTORY = {
  id: "factory" as FactoryId,
  type: "log",
  chainId: 1,
  sourceId: "test",
  address: REQUEST_ADDRESS_B,
  eventSelector: REQUEST_TOPIC_C,
  childAddressLocation: "topic1",
  fromBlock: undefined,
  toBlock: undefined,
} satisfies LogFactory;export function setupCommon() {
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
describe('selected filter',()=>{beforeEach(setupCommon);beforeEach(setupAnvil);
test("getChildAddress() topics", () => {
  const factory = {
    type: "log",
    childAddressLocation: "topic1",
  } as unknown as LogFactory;
  const log = {
    topics: [
      null,
      "0x000000000000000000000000a21a16ec22a940990922220e4ab5bf4c2310f556",
    ],
  } as unknown as SyncLog;

  expect(getChildAddress({ log, factory })).toBe(
    "0xa21a16ec22a940990922220e4ab5bf4c2310f556",
  );
})
test("getChildAddress() offset", () => {
  const factory = {
    type: "log",
    childAddressLocation: "offset32",
  } as unknown as LogFactory;
  const log = {
    data: "0x0000000000000000000000000000000000000000000000000000000017d435c9000000000000000000000000a21a16ec22a940990922220e4ab5bf4c2310f556",
  } as unknown as SyncLog;

  expect(getChildAddress({ log, factory })).toBe(
    "0xa21a16ec22a940990922220e4ab5bf4c2310f556",
  );
})
test("isBlockFilterMatched", async () => {
  const chain = getChain();
  const rpc = createRpc({
    chain,
    common: context.common,
  });

  const { eventCallbacks } = getBlocksIndexingBuild({
    interval: 1,
  });

  const filter = eventCallbacks[0]!.filter as BlockFilter;

  const rpcBlock = await eth_getBlockByNumber(rpc, ["0x0", true]);

  let isMatched = isBlockFilterMatched({
    filter,
    block: rpcBlock,
  });
  expect(isMatched).toBe(true);

  filter.interval = 2;
  filter.offset = 1;

  isMatched = isBlockFilterMatched({
    filter,
    block: rpcBlock,
  });
  expect(isMatched).toBe(false);
})
test("mergeLogFiltersToBlockRequest returns undefined for no inputs", () => {
  expect(mergeLogFiltersToBlockRequest({ logFilters: [], factories: [] })).toBe(
    undefined,
  );
})
test("mergeLogFiltersToBlockRequest returns one log filter", () => {
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: REQUEST_ADDRESS_A,
    topic0: REQUEST_TOPIC_A,
    topic2: REQUEST_TOPIC_B,
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),
  ).toStrictEqual({
    address: [REQUEST_ADDRESS_A],
    topics: [[REQUEST_TOPIC_A], null, [REQUEST_TOPIC_B]],
  });
})
test("mergeLogFiltersToBlockRequest unions addresses and topics", () => {
  const filterA = {
    ...EMPTY_LOG_FILTER,
    address: REQUEST_ADDRESS_A,
    topic0: REQUEST_TOPIC_A,
    topic1: REQUEST_TOPIC_C,
  } satisfies LogFilter;
  const filterB = {
    ...EMPTY_LOG_FILTER,
    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],
    topic0: REQUEST_TOPIC_B,
    topic1: [REQUEST_TOPIC_C, REQUEST_TOPIC_A],
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: [filterA, filterB],
      factories: [],
    }),
  ).toStrictEqual({
    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],
    topics: [
      [REQUEST_TOPIC_A, REQUEST_TOPIC_B],
      [REQUEST_TOPIC_C, REQUEST_TOPIC_A],
    ],
  });
})
test("mergeLogFiltersToBlockRequest matches all values if one input matches all values", () => {
  const filterA = {
    ...EMPTY_LOG_FILTER,
    address: REQUEST_ADDRESS_A,
    topic0: REQUEST_TOPIC_A,
    topic1: REQUEST_TOPIC_C,
  } satisfies LogFilter;
  const filterB = {
    ...EMPTY_LOG_FILTER,
    address: undefined,
    topic0: REQUEST_TOPIC_B,
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: [filterA, filterB],
      factories: [],
    }),
  ).toStrictEqual({
    address: undefined,
    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_B]],
  });
})
test("mergeLogFiltersToBlockRequest matches all addresses for a factory log filter", () => {
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: REQUEST_FACTORY,
    topic0: REQUEST_TOPIC_A,
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: [filter],
      factories: [REQUEST_FACTORY],
    }),
  ).toStrictEqual({
    address: undefined,
    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],
  });
})
test("mergeLogFiltersToBlockRequest includes factories without log filters", () => {
  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: [],
      factories: [REQUEST_FACTORY],
    }),
  ).toStrictEqual({
    address: [REQUEST_ADDRESS_B],
    topics: [[REQUEST_TOPIC_C]],
  });
})
test("mergeLogFiltersToBlockRequest unions factory and log filter addresses", () => {
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: REQUEST_ADDRESS_A,
    topic0: REQUEST_TOPIC_A,
    topic1: REQUEST_TOPIC_B,
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: [filter],
      factories: [REQUEST_FACTORY],
    }),
  ).toStrictEqual({
    address: [REQUEST_ADDRESS_A, REQUEST_ADDRESS_B],
    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],
  });
})
test("mergeLogFiltersToBlockRequest matches all addresses for a factory without an address", () => {
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: REQUEST_ADDRESS_A,
    topic0: REQUEST_TOPIC_A,
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: [filter],
      factories: [{ ...REQUEST_FACTORY, address: undefined }],
    }),
  ).toStrictEqual({
    address: undefined,
    topics: [[REQUEST_TOPIC_A, REQUEST_TOPIC_C]],
  });
})
test("mergeLogFiltersToBlockRequest ignores inputs with an empty address list", () => {
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: [],
    topic0: REQUEST_TOPIC_A,
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: [filter],
      factories: [{ ...REQUEST_FACTORY, address: [] }],
    }),
  ).toBe(undefined);
})
test("mergeLogFiltersToBlockRequest matches all addresses above ADDRESS_CHUNK_SIZE", () => {
  const addresses = Array.from(
    { length: ADDRESS_CHUNK_SIZE + 1 },
    (_, i) => `0x${i.toString(16).padStart(40, "0")}` as Address,
  );
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: addresses,
    topic0: REQUEST_TOPIC_A,
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),
  ).toStrictEqual({ address: undefined, topics: [[REQUEST_TOPIC_A]] });

  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: [{ ...filter, address: addresses.slice(1) }],
      factories: [],
    })?.address,
  ).toHaveLength(ADDRESS_CHUNK_SIZE);
})
test("mergeLogFiltersToBlockRequest matches all topics above ADDRESS_CHUNK_SIZE", () => {
  const topics = Array.from(
    { length: ADDRESS_CHUNK_SIZE + 1 },
    (_, i) => `0x${i.toString(16).padStart(64, "0")}` as Hex,
  );
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: REQUEST_ADDRESS_A,
    topic0: REQUEST_TOPIC_A,
    topic1: topics,
  } satisfies LogFilter;

  expect(
    mergeLogFiltersToBlockRequest({ logFilters: [filter], factories: [] }),
  ).toStrictEqual({
    address: [REQUEST_ADDRESS_A],
    topics: [[REQUEST_TOPIC_A]],
  });

  expect(
    mergeLogFiltersToBlockRequest({
      logFilters: topics.map((topic0) => ({ ...filter, topic0, topic1: null })),
      factories: [],
    }),
  ).toStrictEqual({ address: [REQUEST_ADDRESS_A], topics: undefined });
})
});
describe('selected events',()=>{beforeEach(setupCommon);
test("splitEvents()", async () => {
  const events = [
    {
      chain: { id: 1 },
      checkpoint: "0",
      event: {
        block: {
          hash: "0x1",
          timestamp: 1,
          number: 1n,
        },
      },
    },
    {
      chain: { id: 1 },
      checkpoint: "0",
      event: {
        block: {
          hash: "0x2",
          timestamp: 2,
          number: 2n,
        },
      },
    },
  ] as unknown as Event[];

  const result = splitEvents(events);

  expect(result).toMatchInlineSnapshot(`
    [
      {
        "chainId": 1,
        "checkpoint": "000000000100000000000000010000000000000001999999999999999999999999999999999",
        "events": [
          {
            "chain": {
              "id": 1,
            },
            "checkpoint": "0",
            "event": {
              "block": {
                "hash": "0x1",
                "number": 1n,
                "timestamp": 1,
              },
            },
          },
        ],
      },
      {
        "chainId": 1,
        "checkpoint": "000000000200000000000000010000000000000002999999999999999999999999999999999",
        "events": [
          {
            "chain": {
              "id": 1,
            },
            "checkpoint": "0",
            "event": {
              "block": {
                "hash": "0x2",
                "number": 2n,
                "timestamp": 2,
              },
            },
          },
        ],
      },
    ]
  `);
})
});
