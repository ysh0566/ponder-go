# M39 indexing action source evidence

`source-indexing-actions-original.mjs` runs the entire unchanged pinned
`packages/core/src/indexing/index.test.ts` (23 tests), observing only its final
eight action cases: nine original assertions and no TypeScript directives.
The runtime copy uses the existing explicit loopback/Anvil adapter and retains
its adapter diff, manifest, and executed-source archive. This is not described
as a pristine environmental execution.

Observers retain original action inputs/ABI, assigned event block, chain,
actual request/response boundaries, synchronous store invocation, and physical
RPC cache rows. The original zero-data test's first mocked `0x` response stays
unchanged, followed by its genuine RPC retry; its original two-call assertion
still runs. All other original action responses come from deployed ERC20 or
Multicall3 contracts and real RPC. No expected response is substituted.

The source client's `event` property is setter-only. Capture wraps that setter
and calls the original setter rather than inventing a readable source property.
Store wrappers invoke the original method synchronously. Only post-test
observation waits for already-started cache writes before querying actual rows;
no action is delayed for cache persistence by its observer.

`source-indexing-actions-probes.mjs` runs one zero-credit test with nine real
DB/RPC actions on each backend: warm read, partial-cache aggregate with ordered
duplicates, all-hit aggregate, a failed child with both allowFailure modes,
empty aggregate, immutable override, and two uncached empty-code reads. The
aggregate request encodes only missing children (2, 1, and 1 respectively).
Revert/error text is preserved as source evidence, not used to manufacture
native errors. These vectors do not claim profiling/prefetch/LRU completeness.

Both runners require explicit `--backend=postgres|pglite --output=<new dir>`.
Both fixture builders require `--postgres`, `--pglite`, and `--output`; the
original fixture also requires `--declarations`. The source-only collector
requires explicit `--postgres --pglite --fixture --output` and grants no native
coverage. The evidence control suite rejects altered original declarations,
RPC counts, retry response, aggregate selector, failed-child caching, duplicate
order, immutable block, and empty-code caching.

Development captures: `source01/postgres` is retained as an observer failure
(setter-only event property); `source02/{postgres,pglite}` passes all 23 original
tests. `probe01/{postgres,pglite}` passes the nine-action supplemental test.
Final acceptance must rerun against fresh current-context hashes.

Review extension: the supplemental test now has eleven actions. The final two
exercise `batchSize: 4` with a failed first chunk and a later successful uncached
child at explicit block 4 (both RPC chunks must run and the latter persists),
and an ABI encoding failure with `allowFailure: true` (actual `0x` placeholder
call; source wraps a zero-data cause rather than returning the encoding error).
Source `cause_name` and `short_message` are captured to distinguish genuine
contract reverts from zero-data failures. Native comparison checks those
specific typed causes, not all `ContractFunctionExecutionError` values as one
class; full viem presentation/custom-error decoding equivalence is not claimed.
