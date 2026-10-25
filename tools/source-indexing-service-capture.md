# M38 unchanged indexing service source capture

`source-indexing-service-original.mjs` runs the unchanged complete pinned
`src/indexing/index.test.ts` (23 cases / 42 assertions), using only the established
loopback-Anvil startup adapter. First 15 cases / 33 assertions / 3 type directives
are service targets. Last eight client-facade cases execute but are not service
mapping candidates. Every original input, callback body, assertion and skip state
is retained. PostgreSQL and PGlite run separately against real databases.

Observers wrap actual createIndexing/client/store entry points and original
callbacks. Callable callback proxies forward function properties (including
Vitest mockRejectedValue) and preserve the actual argument/context/event/client/
database objects delivered to the original body. Historical property reads are
observed by wrapping the existing profile Set.add, not by reading source event
proxies. Capturing nested historical event values would itself change include
tracking, so this capture never does so. Raw public event inputs are captured
before the service applies its proxies. Profile reads retain duplicate reads and
order; the original column-selection case supplies 1001 successful invocations
followed by one missing-field InvalidEventAccessError invocation.

## Wire format

Evidence `calls.json.gz` and fixture `source-indexing-service-live.json.gz` are
lossless gzip JSON. `$object` is ordered key/value pairs; `$id`/`$ref` preserve
object identity within each source case. `$bigint`, `$undefined`, `$number`,
`$map`, `$set`, `$function`, `$error` retain source distinctions. A single mark
operation expands each object once; separate marks expand objects again using
the same identity. Therefore decoding references must be scoped appropriately:
a later snapshot can describe changed values of an existing identity.

Fixture `backend_cases.postgres` and `.pglite` each contain all 23 declarations;
only the first 15 have `targeted:true` and service calls. Each `createIndexing`
call has original build arguments, actual schemas and method invocations. Each
method has raw input events, before/after service state, actual callbacks,
ordered operations, raw RPC requests/responses, metrics, update notifications,
and nine physical sync-table snapshots. Real store insert/find and client
getBalance operations execute unchanged. Duration values are nondeterministic;
compare metric semantics and counts, not elapsed time equality.

Inputs and original callback behavior drive native execution. Raw provider
responses can supply only the RPC boundary. Callback results, service state,
metrics and physical rows are comparison targets, never database/state seeds.
Original callback source is retained; replay must execute equivalent actual
operations and errors rather than return captured callback results.

Two type directives reject `.mockRejectedValue` on a normal callback function;
the third rejects deletion of the required transaction.maxPriorityFeePerGas.
Runtime success does not discharge those static obligations. Maximum formal
credit is 12 of the 15 unless exact native static counterparts are demonstrated.
No outer historical runner/cutover/atomic multichain claim follows from these
service tests; only column selection directly calls the historical method.

## Commands

Use the pinned upstream node_modules/.bin in PATH, source the task env, check
>=5GiB capacity, and own the serial DB slot before invoking the existing
`bash .../ponder-cloud/with-postgres.sh` wrapper in the same exec namespace.

```
node tools/source-indexing-service-original.mjs --backend=postgres --output=parity/runs/indexing-service-m38/NEW/source-postgres
node tools/source-indexing-service-original.mjs --backend=pglite --output=parity/runs/indexing-service-m38/NEW/source-pglite
node tools/source-indexing-service-fixture.mjs --postgres=parity/runs/indexing-service-m38/NEW/source-postgres --pglite=parity/runs/indexing-service-m38/NEW/source-pglite --output=testdata/indexing/source-indexing-service-live.json.gz --declarations=parity/source-indexing-service-declarations.json
node tools/source-indexing-service-evidence.test.mjs --fixture=testdata/indexing/source-indexing-service-live.json.gz
```

All evidence destinations are explicit. Existing result directories are never
overwritten. Failed attempts remain evidence. Compression of older raw JSON
captures preserves original bytes and is recorded with both hashes.

Source-only collection (zero credit; requires fresh explicit destination):
```
node tools/source-indexing-service-collect.mjs --postgres=parity/runs/indexing-service-m38/NEW/source-postgres --pglite=parity/runs/indexing-service-m38/NEW/source-pglite --fixture=testdata/indexing/source-indexing-service-live.json.gz --output=parity/runs/indexing-service-m38/NEW/source-collection.json
```

Supplemental actual source Proxy reflection, no DB/RPC/globalSetup needed:
```
node tools/source-indexing-service-proxy.mjs --output=parity/runs/indexing-service-m38/NEW/proxy-source
node tools/source-indexing-service-proxy-fixture.mjs --input=parity/runs/indexing-service-m38/NEW/proxy-source --output=testdata/indexing/source-indexing-service-proxy.json
node tools/source-indexing-service-proxy-evidence.test.mjs --fixture=testdata/indexing/source-indexing-service-proxy.json
```

The direct probe invokes exported actual `createEventProxy`, observes 23 ordered
Get/Set/Delete/Has/ownKeys/Object.keys/Inspect/bypass operations, and preserves
explicit initial/replacement underlying values. Its reset callback records source
invocation and resets its test profile count; it does not substitute service
reset logic. Full service invalidation remains covered by original column
selection and native integration. All supplemental evidence has zero credit.

Supplemental actual cached-client persistence completion and error branding:
```
node tools/source-indexing-service-cache.mjs --output=parity/runs/indexing-service-m38/NEW/cache-source
node tools/source-indexing-service-cache-fixture.mjs --input=parity/runs/indexing-service-m38/NEW/cache-source --output=testdata/indexing/source-indexing-service-cache.json
node tools/source-indexing-service-cache-evidence.test.mjs --fixture=testdata/indexing/source-indexing-service-cache.json
```

These four zero-credit controls use real Anvil and actual `createRpc`,
`createCachedViemClient` and `createIndexing`. The cache dependency returns an
empty lookup then gates `insertRpcRequestResults`; balance comes exclusively
from real provider `eth_getBalance(address, "0x0")`. Successful return precedes
release of the pending cache insertion; both late success and late failure
settle afterward without changing the client result. The error controls show
that a user error's `name` cannot counterfeit source `BaseError` identity.
No PostgreSQL/PGlite operations are claimed by these boundary controls.

Initial evidence retained: source01 failed before tests due observer syntax,
source02 passed both23 and was losslessly compressed with manifests, source03
is the final gzip-writer capture. Proxy01 passed, then proxy02 refreshed with
explicit replacement-input/underlying snapshots. Cache01/02 passed with a
numeric setup block causing Viem latest selection; cache03 corrects the
supplemental input to genuine bigint zero and verifies raw provider `0x0`.
Original source test inputs/assertions were never edited in any attempt.
