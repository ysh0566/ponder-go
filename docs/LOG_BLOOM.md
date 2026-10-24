# Native log bloom screening

M27 complete verification exits0: normal/race each pass1804 leaves with zero
failures/skips, including PG194/PGlite191. All20 packages/134 listed top-level
functions/48 disjoint batches per mode finish with the same935-file fingerprint
`c18845a3bd6421af83240dd76033c2647b09f5ccc5ebb5db60f86c8d1bf6225d`.
Vet/build, actual original captures/oracles, types, SDK and supplementary
differential checks pass. Exact source mappings are325/905 (321Go+4SDK), with580
static records unimplemented; static discovery is not dynamically exhaustive.
Frozen evidence is `parity/runs/batch-26.json` and `batch-26-evidence/`.

The eight original bloom declarations retain12 assertions/19 direct operations
and both real compiler-error proofs. Source capture is call-through with
unchanged original setup/constants/assertions. Native extra cases and497 source
vectors add zero source credit. First full normal1795pass/4fail/0skip stopped
before race on an owned supplemental test's old request-count expectation.
That scenario keeps its original input/setup and explicitly expects one RPC.
A separate4-case actual-EVM false-positive test expects3 RPCs and still rejects
extra logs locally. Fixed source functions independently check actual headers:
old[true,false,false], new[true,true,true]. This preserves the original behavior
change visibly and adds coverage without replacing the original scenario.

Recovery race24/24 pass covers old scenario4/new scenario4/envelope10/actual
runtime6, including explicit emptyhex0x, missing-field rejection/atomic retry,
uncached absent-header fetching and actual Anvil chain143. Full02 verifies the
final inputs; complete failed full01 evidence is retained separately. M26
publication at `253f4f9` is recorded as the preceding accepted base.

The native `bloom` package follows pinned
`packages/core/src/sync-realtime/bloom.ts`: Keccak produces three 11-bit
positions; each address alternative is OR, the four topics are AND, and each
topic alternative is OR. Null/undefined are wildcards and empty topic arrays
match nothing. A child factory address is a wildcard in a business log filter;
factory discovery separately checks the parent and creation-event selector.
The source deliberately treats a business log filter's empty address array as
a bloom wildcard, while a factory's empty parent array cannot match.

Eight complete original declarations contain12 boolean assertions and two
`topic0: undefined` compiler-error directives. Capture runs the whole original
file with original constants/setup and unchanged Vitest assertions, records
call-through inputs/results/mutations, then Go replays every operation and
explicit assertion. Uint8Array and undefined markers remain distinct. This is
instrumented original evidence, separate from pristine backend acceptance.
Additional generated vectors execute the actual pinned functions and add no
source-test credit.

The public native `filter.LogFilter.Topic0` accepts a single `HexTopic`, matching
the source's single-Hex boundary. Both source error directives require real
positive-build/run and negative-build diagnostics against that API. Intentional
invalid runtime fixtures use the explicitly dynamic Object adapter. Go omitted
zero values and arbitrary string conversion, plus other dynamic fields, remain
broader than the source TypeScript types. This is a narrow verified obligation,
not full static equivalence.

Engine construction precomputes bloom predicates for all declared ABI events
and factories once. It retains all event selectors so changing the public
handler map later cannot invalidate a handler-only optimization. Block screening
may skip only contract/factory `eth_getLogs` requests. Setup events, account
transactions/transfers, contract traces, empty factory discovery/provenance,
block handlers, completion and rollback still execute normally.

The source treats an all-zero bloom as missing and bypasses screening on async
chains143/10143/43114/43113. Native absent, short or malformed header blooms also
fall back to requesting logs. Only valid, nonzero256-byte blooms on synchronous
chains can suppress requests. Bloom false positives are allowed; a skipped
request must never hide a matching log.

The native real-EVM fixture compares an all-zero-header baseline with actual
blooms using the same config and business results on PG/PGlite and legacy/staged
runtime; counts requests for a nonmatching nonzero block; checks account and
contract traces still execute; discovers birth-block logs; replaces a branch
using the same parent transaction hash and another child; and fetches real logs
for zero/missing/short/malformed headers and a real Anvil chain143. These checks
are supplementary, not original E2E/source credit.

Complete realtime reconnect/stall/validation, history planning, original config
DSL, multichain scheduling, full query/API/CLI/types/SDK and simulation remain
separate incomplete work.

Two preparation failures are retained in `parity/runs/bloom-m27/`: new mapping
metadata initially used the wrong assertion-hash convention, and the new Go
replay serializer compared object key order instead of the exact object values.
Both were fixture/metadata corrections; no original assertions or captured
expected values changed, and neither is labelled a production regression fix.

The existing protocol cache requires the header `logsBloom` field and rejects
an absent field before screening. The runtime fixture preserves that rejection,
checks that no block/business progress commits, then retries the actual header.
A fresh uncached runtime separately verifies missing-bloom fetching and identical
canonical business data. Screening does not weaken protocol validation or fill
missing fields. This existing boundary was discovered by a failed new-fixture
expectation; no production behavior was changed to accept malformed input.
