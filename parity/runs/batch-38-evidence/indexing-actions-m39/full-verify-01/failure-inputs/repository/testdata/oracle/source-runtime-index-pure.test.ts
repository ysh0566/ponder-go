import fs from 'node:fs';
import {zeroAddress} from 'viem';
import {beforeEach,afterEach,afterAll,expect,test} from 'vitest';
import {ALICE,EMPTY_BLOCK_FILTER,EMPTY_LOG_FILTER} from '@/_test/constants.js';
import type {BlockFilter,Filter,Factory,Fragment,LogFilter} from '@/internal/types.js';
import type {Interval} from '@/utils/interval.js';
import type {CachedIntervals} from '@/runtime/index.js';
import * as actual from '@/runtime/index.js';
import {getFragments,getFactoryFragments} from '@/runtime/fragments.js';
const metadata=[{"id":"928495e24f9b9eec3013","title":"getCachedBlock() no cached intervals","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":142,"source_state":"active","assertions":[{"kind":"runtime","line":159,"text":"expect(cachedBlock).toBe(undefined)"}],"declaration_sha256":"ac125596865ceab8f435b5b176415e7ef63950d8e690a43ff1fe39fea1e81691"},{"id":"c429e0be63d1e2a5621c","title":"getCachedBlock() with cache","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":162,"source_state":"active","assertions":[{"kind":"runtime","line":179,"text":"expect(cachedBlock).toBe(24)"},{"kind":"runtime","line":204,"text":"expect(cachedBlock).toBe(100)"}],"declaration_sha256":"c4c0ec19b9838539c811df3039fbd719bdb5f9724192edf7cc6d9c4ad47cad06"},{"id":"4096c895c425e67b73c8","title":"getCachedBlock() with incomplete cache","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":207,"source_state":"active","assertions":[{"kind":"runtime","line":224,"text":"expect(cachedBlock).toBeUndefined()"}],"declaration_sha256":"0d958a0f41aa57a4eb853e17b473dca1a63f2b00364031cde3e02f641c479575"},{"id":"0ad0d344d7cb00ef36b7","title":"getCachedBlock() with multiple filters","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":227,"source_state":"active","assertions":[{"kind":"runtime","line":257,"text":"expect(cachedBlock).toBe(24)"},{"kind":"runtime","line":272,"text":"expect(cachedBlock).toBe(24)"},{"kind":"runtime","line":287,"text":"expect(cachedBlock).toBe(49)"}],"declaration_sha256":"954cd2b22c98b9a88b3e99545a34c6173ce9ae0563c9f78b7faff4acdae52133"},{"id":"02613908644ac30deaea","title":"getCachedBlock() with factory","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":290,"source_state":"active","assertions":[{"kind":"runtime","line":320,"text":"expect(cachedBlock).toBe(1)"},{"kind":"runtime","line":346,"text":"expect(cachedBlock).toBe(18)"}],"declaration_sha256":"97db76345fb2a3cf0c2677398a030f02f359918c19b62487c6442840cd1d2627"},{"id":"cfc3d65d78ce2707a366","title":"getRequiredIntervals()","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":349,"source_state":"active","assertions":[{"kind":"runtime","line":380,"text":"expect(requiredIntervals).toMatchInlineSnapshot(`\n    [\n      [\n        25,\n        150,\n      ],\n    ]\n  `)"},{"kind":"runtime","line":403,"text":"expect(requiredIntervals).toMatchInlineSnapshot(`\n    [\n      [\n        25,\n        100,\n      ],\n      [\n        103,\n        150,\n      ],\n    ]\n  `)"},{"kind":"runtime","line":430,"text":"expect(requiredIntervals).toMatchInlineSnapshot(`\n    [\n      [\n        50,\n        150,\n      ],\n    ]\n  `)"}],"declaration_sha256":"6c55bb88e84f54fd4bbdc1749b5238bee2d96d0fc1a58117dffe52e3600a38d7"},{"id":"f6920d1d20a903b4f5a4","title":"getRequiredIntervalsWithFilters()","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":440,"source_state":"active","assertions":[{"kind":"runtime","line":461,"text":"expect(requiredIntervals).toMatchInlineSnapshot(`\n    {\n      \"factoryIntervals\": [],\n      \"intervals\": [\n        {\n          \"filter\": {\n            \"address\": \"0x0000000000000000000000000000000000000000\",\n            \"chainId\": 1,\n            \"fromBlock\": 0,\n            \"hasTransactionReceipt\": false,\n            \"include\": [],\n            \"sourceId\": \"test\",\n            \"toBlock\": 100,\n            \"topic0\": \"0x0000000000000000000000000000000000000000000000000000000000000000\",\n            \"topic1\": null,\n            \"topic2\": null,\n            \"topic3\": null,\n            \"type\": \"log\",\n          },\n          \"interval\": [\n            25,\n            100,\n          ],\n        },\n      ],\n    }\n  `)"},{"kind":"runtime","line":511,"text":"expect(requiredIntervals).toMatchInlineSnapshot(`\n    {\n      \"factoryIntervals\": [],\n      \"intervals\": [\n        {\n          \"filter\": {\n            \"address\": [\n              \"0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266\",\n            ],\n            \"chainId\": 1,\n            \"fromBlock\": 0,\n            \"hasTransactionReceipt\": false,\n            \"include\": [],\n            \"sourceId\": \"test\",\n            \"toBlock\": 100,\n            \"topic0\": \"0x0000000000000000000000000000000000000000000000000000000000000000\",\n            \"topic1\": null,\n            \"topic2\": null,\n            \"topic3\": null,\n            \"type\": \"log\",\n          },\n          \"interval\": [\n            25,\n            50,\n          ],\n        },\n      ],\n    }\n  `)"}],"declaration_sha256":"aef500864430941a245f4ae066cdc7c709faa414b81c52cd9a73ec6050066f6e"},{"id":"1469b5672695375f7fe2","title":"getRequiredIntervalsWithFilters() with factory","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":542,"source_state":"active","assertions":[{"kind":"runtime","line":576,"text":"expect(requiredIntervals).toMatchInlineSnapshot(`\n    {\n      \"factoryIntervals\": [\n        {\n          \"factory\": {\n            \"address\": \"0xef2d6d194084c2de36e0dabfce45d046b37d1106\",\n            \"chainId\": 1,\n            \"childAddressLocation\": \"topic1\",\n            \"eventSelector\": \"0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc\",\n            \"fromBlock\": 2,\n            \"id\": \"id\",\n            \"sourceId\": \"factory\",\n            \"toBlock\": 5,\n            \"type\": \"log\",\n          },\n          \"interval\": [\n            2,\n            2,\n          ],\n        },\n      ],\n      \"intervals\": [\n        {\n          \"filter\": {\n            \"address\": {\n              \"address\": \"0xef2d6d194084c2de36e0dabfce45d046b37d1106\",\n              \"chainId\": 1,\n              \"childAddressLocation\": \"topic1\",\n              \"eventSelector\": \"0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc\",\n              \"fromBlock\": 2,\n              \"id\": \"id\",\n              \"sourceId\": \"factory\",\n              \"toBlock\": 5,\n              \"type\": \"log\",\n            },\n            \"chainId\": 1,\n            \"fromBlock\": 10,\n            \"hasTransactionReceipt\": false,\n            \"include\": [],\n            \"sourceId\": \"test\",\n            \"toBlock\": 20,\n            \"topic0\": \"0x0000000000000000000000000000000000000000000000000000000000000000\",\n            \"topic1\": null,\n            \"topic2\": null,\n            \"topic3\": null,\n            \"type\": \"log\",\n          },\n          \"interval\": [\n            10,\n            20,\n          ],\n        },\n      ],\n    }\n  `)"}],"declaration_sha256":"22ae33678ce64c36753d834bb949c4667e19b8a89d41415fa3a3c8d04f0371cc"},{"id":"34b296bcde7be28a3324","title":"getRequiredIntervalsWithFilters() with factory and missing parent","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":633,"source_state":"active","assertions":[{"kind":"runtime","line":676,"text":"expect(requiredIntervals.factoryIntervals).toStrictEqual([\n    {\n      factory: { ...filter.address, address: [parentY] },\n      interval: [0, 100],\n    },\n  ])"}],"declaration_sha256":"7ac19da915d70d0061a5afdd3d2996dbffa584f5268f22bebf5a56babd0b87d0"},{"id":"82f711b0ecef2ba1a7c9","title":"getRequiredIntervals() with factory","source_file":"packages/core/src/runtime/index.test.ts","source_sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be","line":684,"source_state":"active","assertions":[{"kind":"runtime","line":718,"text":"expect(requiredIntervals).toMatchInlineSnapshot(`\n    [\n      [\n        19,\n        20,\n      ],\n    ]\n  `)"},{"kind":"runtime","line":739,"text":"expect(requiredIntervals).toMatchInlineSnapshot(`\n    [\n      [\n        5,\n        5,\n      ],\n      [\n        10,\n        20,\n      ],\n    ]\n  `)"}],"declaration_sha256":"f66e629bf9b0a1f24bc45b914abf9737aaf8982b49a1462c3cc0bc6e957f8fae"}];
const cases=[];let current;
// IDs persist across a call's before/result/after snapshots; each snapshot expands
// each object once, preserving Map identity, property order and explicit undefined.
function recorder(){const ids=new WeakMap();let serial=0;return function mark(value){const expanded=new Set();function walk(v){
 if(v===undefined)return {$undefined:true};
 if(typeof v==='bigint')return {$bigint:String(v)};
 if(typeof v==='number'&&!Number.isFinite(v))return {$number:String(v)};
 if(Array.isArray(v))return v.map(walk);
 if(v instanceof Map)return {$map:[...v].map(([k,value])=>[walk(k),walk(value)])};
 if(v&&typeof v==='object'){let id=ids.get(v);if(id===undefined){id=++serial;ids.set(v,id)}if(expanded.has(v))return {$ref:id};expanded.add(v);return {$id:id,$object:Object.entries(v).map(([k,value])=>[k,walk(value)])};}
 return v;
}return walk(value)}}
function capture(operation,fn){return (...args)=>{const mark=recorder(),entry={operation,args:mark(args)};current.calls.push(entry);try{const result=fn(...args);entry.result=mark(result);return result;}catch(error){entry.error={name:error.name,message:error.message};throw error;}finally{entry.after=mark(args)}}}
const getCachedBlock=capture('getCachedBlock',actual.getCachedBlock);
const getRequiredIntervals=capture('getRequiredIntervals',actual.getRequiredIntervals);
const getRequiredIntervalsWithFilters=capture('getRequiredIntervalsWithFilters',actual.getRequiredIntervalsWithFilters);
beforeEach(ctx=>{const meta=metadata.find(m=>m.title===ctx.task.name);if(!meta)throw Error('Unmapped original declaration');current={...meta,calls:[]};});
afterEach(ctx=>{current.observed_after_each_state=ctx.task.result?.state??'unknown';cases.push(current)});
afterAll(()=>fs.writeFileSync(process.env.PONDER_RUNTIME_PURE_FIXTURE!,JSON.stringify({baseline:{"repository":"https://github.com/ponder-sh/ponder","revision":"17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334","core_version":"0.18.2"},method:"Ten unchanged pure test declarations and eighteen original assertions, importing actual pinned runtime/index.ts and fragments.ts. Original setupCommon/setupAnvil/setupIsolatedDatabase/setupCleanup hooks are NOT run. Supplemental isolated evidence only; zero original source-record credit, not pristine-file acceptance.",source_credit:0,pristine_file:false,typescript:"5.4.5",sources:[{"file":"packages/core/src/runtime/index.test.ts","sha256":"57d70b2306eb6de27b87bf10b0bbbd5be6309b8f837cf687bf402e17987399be"},{"file":"packages/core/src/runtime/index.ts","sha256":"3211c59db32a335f335ceafffadb68c0f04aaf1ff837bb4dfe097e14f2041434"},{"file":"packages/core/src/runtime/filter.ts","sha256":"b9fe402a5a857bf0c309af808d7a04b1ac8052cfaacf7230f48425eb07ea4661"},{"file":"packages/core/src/runtime/fragments.ts","sha256":"598f0605739d2348adc6a39e8d0fc9cd30bb1c996b132f6f642b1e6024bdf77f"},{"file":"packages/core/src/utils/interval.ts","sha256":"cf376d2082b933f8a6aad8a9f56153edf0a7adfec12722f92d96d2f2593d3335"},{"file":"packages/core/src/utils/dedupe.ts","sha256":"ecba745be6172e75021e553749d487b87f4f704b205a8c1f6261bb3ac2ddb1bf"},{"file":"packages/core/src/_test/constants.ts","sha256":"b214b5a5504156518027fbea443927abdabe11b735413eabbadd285e22fcb9b0"},{"file":"pnpm-lock.yaml","sha256":"797129174b3edb211a89aa5ff6029c8c7cf79d49f559ed8a168dc2eee9d6a38d"}],cases},null,2)+'\n'));
test("getCachedBlock() no cached intervals", async () => {
  const filter = {
    ...EMPTY_BLOCK_FILTER,
    fromBlock: 0,
    toBlock: 100,
  } satisfies BlockFilter;

  const cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([[filter, []]]);

  const cachedBlock = getCachedBlock({
    filters: [filter],
    cachedIntervals,
  });

  expect(cachedBlock).toBe(undefined);
});
test("getCachedBlock() with cache", async () => {
  const filter = {
    ...EMPTY_BLOCK_FILTER,
    fromBlock: 0,
    toBlock: 100,
  } satisfies BlockFilter;

  let cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([[filter, [{ fragment: {} as Fragment, intervals: [[0, 24]] }]]]);

  let cachedBlock = getCachedBlock({
    filters: [filter],
    cachedIntervals,
  });

  expect(cachedBlock).toBe(24);

  cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([
    [
      filter,
      [
        {
          fragment: {} as Fragment,
          intervals: [
            [0, 50],
            [50, 102],
          ],
        },
      ],
    ],
  ]);

  cachedBlock = getCachedBlock({
    filters: [filter],
    cachedIntervals,
  });

  expect(cachedBlock).toBe(100);
});
test("getCachedBlock() with incomplete cache", async () => {
  const filter = {
    ...EMPTY_BLOCK_FILTER,
    fromBlock: 0,
    toBlock: 100,
  } satisfies BlockFilter;

  const cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([[filter, [{ fragment: {} as Fragment, intervals: [[1, 24]] }]]]);

  const cachedBlock = getCachedBlock({
    filters: [filter],
    cachedIntervals,
  });

  expect(cachedBlock).toBeUndefined();
});
test("getCachedBlock() with multiple filters", async () => {
  const filters = [
    {
      ...EMPTY_BLOCK_FILTER,

      fromBlock: 0,
      toBlock: 100,
    },
    {
      ...EMPTY_BLOCK_FILTER,

      offset: 1,
      fromBlock: 50,
      toBlock: 150,
    },
  ] satisfies BlockFilter[];

  let cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([
    [filters[0]!, [{ fragment: {} as Fragment, intervals: [[0, 24]] }]],
    [filters[1]!, []],
  ]);

  let cachedBlock = getCachedBlock({
    filters,
    cachedIntervals,
  });

  expect(cachedBlock).toBe(24);

  cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([
    [filters[0]!, [{ fragment: {} as Fragment, intervals: [[0, 24]] }]],
    [filters[1]!, [{ fragment: {} as Fragment, intervals: [[50, 102]] }]],
  ]);

  cachedBlock = getCachedBlock({
    filters,
    cachedIntervals,
  });

  expect(cachedBlock).toBe(24);

  cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([
    [filters[0]!, [{ fragment: {} as Fragment, intervals: [[0, 60]] }]],
    [filters[1]!, []],
  ]);

  cachedBlock = getCachedBlock({
    filters,
    cachedIntervals,
  });

  expect(cachedBlock).toBe(49);
});
test("getCachedBlock() with factory", async () => {
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: {
      id: "id",
      type: "log",
      chainId: 1,
      sourceId: "factory",
      address: "0xef2d6d194084c2de36e0dabfce45d046b37d1106",
      eventSelector:
        "0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc",
      childAddressLocation: "topic1",
      fromBlock: 2,
      toBlock: 5,
    },
    fromBlock: 10,
    toBlock: 20,
  } satisfies LogFilter;

  // @ts-expect-error
  let cachedIntervals: CachedIntervals = new Map([
    [filter, [{ fragment: {} as Fragment, intervals: [[10, 20]] }]],
    [filter.address, []],
  ]);

  let cachedBlock = getCachedBlock({
    filters: [filter],
    cachedIntervals,
  });

  expect(cachedBlock).toBe(1);

  // @ts-expect-error
  cachedIntervals = new Map([
    [
      filter,
      [
        {
          fragment: {} as Fragment,
          intervals: [[10, 18]],
        },
      ],
    ],
    [
      filter.address,
      [
        {
          fragment: {} as Fragment,
          intervals: [[2, 5]],
        },
      ],
    ],
  ]);

  cachedBlock = getCachedBlock({ filters: [filter], cachedIntervals });

  expect(cachedBlock).toBe(18);
});
test("getRequiredIntervals()", async () => {
  const filters = [
    {
      ...EMPTY_BLOCK_FILTER,

      fromBlock: 0,
      toBlock: 100,
    },
    {
      ...EMPTY_BLOCK_FILTER,

      offset: 1,
      fromBlock: 50,
      toBlock: 150,
    },
  ] satisfies BlockFilter[];

  let cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([
    [filters[0]!, [{ fragment: {} as Fragment, intervals: [[0, 24]] }]],
    [filters[1]!, []],
  ]);

  let requiredIntervals = getRequiredIntervals({
    filters,
    interval: [0, 150],
    cachedIntervals,
  });

  expect(requiredIntervals).toMatchInlineSnapshot(`
    [
      [
        25,
        150,
      ],
    ]
  `);

  cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([
    [filters[0]!, [{ fragment: {} as Fragment, intervals: [[0, 24]] }]],
    [filters[1]!, [{ fragment: {} as Fragment, intervals: [[50, 102]] }]],
  ]);

  requiredIntervals = getRequiredIntervals({
    filters,
    interval: [0, 150],
    cachedIntervals,
  });

  expect(requiredIntervals).toMatchInlineSnapshot(`
    [
      [
        25,
        100,
      ],
      [
        103,
        150,
      ],
    ]
  `);

  cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([
    [filters[0]!, [{ fragment: {} as Fragment, intervals: [[0, 60]] }]],
    [filters[1]!, []],
  ]);

  requiredIntervals = getRequiredIntervals({
    filters,
    interval: [0, 150],
    cachedIntervals,
  });

  expect(requiredIntervals).toMatchInlineSnapshot(`
    [
      [
        50,
        150,
      ],
    ]
  `);
});
test("getRequiredIntervalsWithFilters()", async () => {
  const filter: LogFilter = {
    ...EMPTY_LOG_FILTER,
    fromBlock: 0,
    toBlock: 100,
    address: zeroAddress,
  };

  let fragments = getFragments(filter);

  let cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([[filter, [{ fragment: fragments[0]!.fragment, intervals: [[0, 24]] }]]]);

  let requiredIntervals = getRequiredIntervalsWithFilters({
    filters: [filter],
    interval: [0, 100],
    cachedIntervals,
  });

  expect(requiredIntervals).toMatchInlineSnapshot(`
    {
      "factoryIntervals": [],
      "intervals": [
        {
          "filter": {
            "address": "0x0000000000000000000000000000000000000000",
            "chainId": 1,
            "fromBlock": 0,
            "hasTransactionReceipt": false,
            "include": [],
            "sourceId": "test",
            "toBlock": 100,
            "topic0": "0x0000000000000000000000000000000000000000000000000000000000000000",
            "topic1": null,
            "topic2": null,
            "topic3": null,
            "type": "log",
          },
          "interval": [
            25,
            100,
          ],
        },
      ],
    }
  `);

  filter.address = [zeroAddress, ALICE];
  fragments = getFragments(filter);

  cachedIntervals = new Map<
    Filter,
    { fragment: Fragment; intervals: Interval[] }[]
  >([
    [
      filter,
      [
        { fragment: fragments[0]!.fragment, intervals: [[0, 50]] },
        { fragment: fragments[1]!.fragment, intervals: [[0, 24]] },
      ],
    ],
  ]);

  requiredIntervals = getRequiredIntervalsWithFilters({
    filters: [filter],
    interval: [25, 50],
    cachedIntervals,
  });

  expect(requiredIntervals).toMatchInlineSnapshot(`
    {
      "factoryIntervals": [],
      "intervals": [
        {
          "filter": {
            "address": [
              "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
            ],
            "chainId": 1,
            "fromBlock": 0,
            "hasTransactionReceipt": false,
            "include": [],
            "sourceId": "test",
            "toBlock": 100,
            "topic0": "0x0000000000000000000000000000000000000000000000000000000000000000",
            "topic1": null,
            "topic2": null,
            "topic3": null,
            "type": "log",
          },
          "interval": [
            25,
            50,
          ],
        },
      ],
    }
  `);
});
test("getRequiredIntervalsWithFilters() with factory", async () => {
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: {
      id: "id",
      type: "log",
      chainId: 1,
      sourceId: "factory",
      address: "0xef2d6d194084c2de36e0dabfce45d046b37d1106",
      eventSelector:
        "0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc",
      childAddressLocation: "topic1",
      fromBlock: 2,
      toBlock: 5,
    },
    fromBlock: 10,
    toBlock: 20,
  } satisfies LogFilter;

  const fragments = getFragments(filter);
  const factoryFragments = getFactoryFragments(filter.address);

  // @ts-expect-error
  const cachedIntervals: CachedIntervals = new Map([
    [filter, [{ fragment: fragments[0]!.fragment, intervals: [[10, 24]] }]],
    [filter.address, [{ fragment: factoryFragments[0]!, intervals: [[3, 5]] }]],
  ]);

  const requiredIntervals = getRequiredIntervalsWithFilters({
    filters: [filter],
    interval: [0, 100],
    cachedIntervals,
  });

  expect(requiredIntervals).toMatchInlineSnapshot(`
    {
      "factoryIntervals": [
        {
          "factory": {
            "address": "0xef2d6d194084c2de36e0dabfce45d046b37d1106",
            "chainId": 1,
            "childAddressLocation": "topic1",
            "eventSelector": "0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc",
            "fromBlock": 2,
            "id": "id",
            "sourceId": "factory",
            "toBlock": 5,
            "type": "log",
          },
          "interval": [
            2,
            2,
          ],
        },
      ],
      "intervals": [
        {
          "filter": {
            "address": {
              "address": "0xef2d6d194084c2de36e0dabfce45d046b37d1106",
              "chainId": 1,
              "childAddressLocation": "topic1",
              "eventSelector": "0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc",
              "fromBlock": 2,
              "id": "id",
              "sourceId": "factory",
              "toBlock": 5,
              "type": "log",
            },
            "chainId": 1,
            "fromBlock": 10,
            "hasTransactionReceipt": false,
            "include": [],
            "sourceId": "test",
            "toBlock": 20,
            "topic0": "0x0000000000000000000000000000000000000000000000000000000000000000",
            "topic1": null,
            "topic2": null,
            "topic3": null,
            "type": "log",
          },
          "interval": [
            10,
            20,
          ],
        },
      ],
    }
  `);
});
test("getRequiredIntervalsWithFilters() with factory and missing parent", async () => {
  const parentX = "0xef2d6d194084c2de36e0dabfce45d046b37d1106";
  const parentY = "0x5fbdb2315678afecb367f032d93f642f64180aa3";

  const filter = {
    ...EMPTY_LOG_FILTER,
    address: {
      id: "id",
      type: "log",
      chainId: 1,
      sourceId: "factory",
      address: [parentX, parentY],
      eventSelector:
        "0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc",
      childAddressLocation: "topic1",
      fromBlock: undefined,
      toBlock: undefined,
    },
  } satisfies LogFilter;

  const [fragmentX, fragmentY] = getFactoryFragments(filter.address);

  // @ts-expect-error
  const cachedIntervals: CachedIntervals = new Map([
    [
      filter,
      getFragments(filter).map(({ fragment }) => ({ fragment, intervals: [] })),
    ],
    [
      filter.address,
      [
        { fragment: fragmentX!, intervals: [[0, 100]] },
        { fragment: fragmentY!, intervals: [] },
      ],
    ],
  ]);

  const requiredIntervals = getRequiredIntervalsWithFilters({
    filters: [filter],
    interval: [0, 100],
    cachedIntervals,
  });

  expect(requiredIntervals.factoryIntervals).toStrictEqual([
    {
      factory: { ...filter.address, address: [parentY] },
      interval: [0, 100],
    },
  ]);
});
test("getRequiredIntervals() with factory", async () => {
  const filter = {
    ...EMPTY_LOG_FILTER,
    address: {
      id: "id",
      type: "log",
      chainId: 1,
      sourceId: "factory",
      address: "0xef2d6d194084c2de36e0dabfce45d046b37d1106",
      eventSelector:
        "0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc",
      childAddressLocation: "topic1",
      fromBlock: 2,
      toBlock: 5,
    } satisfies Factory,
    fromBlock: 10,
    toBlock: 20,
  } satisfies LogFilter;

  // @ts-expect-error
  let cachedIntervals: CachedIntervals = new Map([
    [filter, [{ fragment: {} as Fragment, intervals: [[10, 18]] }]],
    [
      filter.address as Factory,
      [{ fragment: {} as Fragment, intervals: [[2, 5]] }],
    ],
  ]);

  let requiredIntervals = getRequiredIntervals({
    filters: [filter],
    interval: [2, 20],
    cachedIntervals,
  });

  expect(requiredIntervals).toMatchInlineSnapshot(`
    [
      [
        19,
        20,
      ],
    ]
  `);

  // @ts-expect-error
  cachedIntervals = new Map([
    [filter, [{ fragment: {} as Fragment, intervals: [[10, 18]] }]],
    [filter.address, [{ fragment: {} as Fragment, intervals: [[2, 4]] }]],
  ]);

  requiredIntervals = getRequiredIntervals({
    filters: [filter],
    interval: [2, 20],
    cachedIntervals,
  });

  expect(requiredIntervals).toMatchInlineSnapshot(`
    [
      [
        5,
        5,
      ],
      [
        10,
        20,
      ],
    ]
  `);
});
