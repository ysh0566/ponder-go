# Per-chain Go RPC transports

A Go source build can take a named transport function instead of resolving a node URL. The function receives a context, method and parameters and returns a raw JSON result or error. The source endpoint pool owns admission, retry and cooldown state, and supplies the earlier of the caller deadline and a ten-second deadline for each attempt. Scoped clients share this transport.

The compiled runtime fixture runs a bounded Omnichain deployment with a URL-backed chain, a separately configured custom chain, and a third chain with no registered callbacks. The custom function forwards individual calls to its local Anvil node. A setup handler and block handlers verify the chain IDs and cache policy and write indexed hashes to the database. A second opening checks the persisted hashes and that callbacks are not repeated.

## Verification

```sh
go test -count=1 ./rpc ./config
go test -race -count=1 ./rpc ./config
selection='^(TestCompiledCustomTransport|TestCompiledMultichainIndexer|TestCompiledIndexerAdmission)$'
go test -v -count=1 -timeout=20m -run "$selection" ./runtime
go test -race -v -count=1 -timeout=20m -run "$selection" ./runtime
go test -run '^$' ./...
go vet ./...
```

The RPC and configuration packages passed in ordinary and race builds. The first configuration runs set an adapted-checkout environment variable to the unmodified source checkout and failed to find its adaptation manifest. The isolated reruns used the unmodified checkout in its normal mode. The focused protocol cases also checked transient timeout classification, shared concurrency, cancellation, retry limits, malformed JSON and cache keys. A separate ordinary/race regression checked an explicit URL override and rebuilding the same configuration without it.

The runtime selection passed sixteen cases per build, including both database engines with a custom transport, six URL-backed ordering/backend controls and eight admission checks. Module compilation, vet and 503 verification-tool tests passed.

The pinned upstream passed all eight cases with `vitest run --maxWorkers=1 --hookTimeout=45000 src/rpc/index.test.ts`. A separate probe against its unmodified `createRpc` supplied a viem custom transport and observed the original chain object, zero inner retries, a 10,000 ms timeout, hostname `custom_transport` and exactly two attempts for an opted-in null block followed by a valid block. Its first standalone invocation could not resolve the project’s viem package from a temporary directory. The rerun used the pinned dependency. These native and supplemental checks add no upstream case mappings.
