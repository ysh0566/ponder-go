# RPC pagination, receipts and response validation

M24 adds the remaining ten declarations from the pinned `rpc/actions.test.ts`:
six lazy pagination cases, two receipt fallback/sorting cases, and two log/block
validation cases. Their exact original statements, shared fixtures, inputs and
29 assertion expressions are retained in `parity/rpc-actions-source-cases.json`.
The earlier four log actions and two trace cases retain their own mappings.
All ten new mappings pass the final full-module acceptance: Go and race each
pass1723 leaves, with SDK/type/differential checks and unchanged source hashes.
See parity/runs/batch-23.json; the overall port remains incomplete.

`Client.GetLogsWithPagination` returns a lazy `iter.Seq2[LogsPage,error]`. Creating
a sequence issues no RPC; stopping iteration issues no subsequent request.
The first range is500 blocks; successful inferred ranges grow by the source's
rounded1.05 factor. Provider hints confirm a range, inferred reductions remain
eligible for growth, and retries resume at the unfinished cursor. An explicit
`ActionOptions.EthGetLogsBlockRange` disables range retries. Original errors and
Go contexts retain pointer identity. The native request boundary is `Requester`;
source request-context options are represented by Go context plus ActionOptions.

`Client.Actions()` shares learned limits and block-receipt capability through
the underlying client, including transaction-scoped `WithCache` wrappers.
Independent clients have independent action state. Numeric page requests avoid
immutable response caching. Existing request cancellation, retry settings,
request IDs and transport rate limits still apply. `NewActions` supports
source-style mocks or another Requester when the caller owns that state object.

`GetTransactionReceipts` returns each complete standardized response together
with its actual request. Block receipt failures, including invalid responses,
warn and permanently switch that RPC action state to individual receipts.
Hashes are deduplicated, results are sorted by transaction index, and no hashes
cause no request. `GetTransactionReceipt` preserves a typed not-found error;
`GetBlockReceipts` rejects null responses. Standardization supplies only absent
fields and retains structured provider errors and request provenance.

`ValidateLogsAndBlock` checks block hash/number and transaction index/hash,
including the original zero-transaction-hash exception. A nonempty bloom with
no returned logs remains valid. This helper does not claim that all validation
helpers or the complete historical runtime planner are integrated.

`GetLogsRetryHelper` ports all22 ordered provider-specific rules and the original
catch-all patterns, inclusive ranges, suggested/inferred flags, exact unchanged
hex-range comparison and bigint arithmetic.62 supplementary vectors execute
unchanged pinned source functions and check retry plans, receipt defaults/errors
and log consistency. They provide no original test-record credit. Six additional
native boundary leaves exercise typed missing receipts, invalid block fallback,
per-client state, cancellation, scoped cache bypass and concurrent consumers.
Live provider tests requiring API keys remain separate and unexecuted.

Address chunks and individual receipt fallback requests execute sequentially;
the original uses Promise.all. Full responses, request provenance and result
ordering are retained, while parallel request throughput remains different.
The main runtime's existing cache-aware block-hash planner is unchanged.
