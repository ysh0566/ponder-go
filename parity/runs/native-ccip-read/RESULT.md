# EIP-3668 in native Go callbacks

The pinned viem 2.35.0 client and Go used the same real Solidity resolver on separate Anvil 1.8.5 chains. The Go tests used registered indexing callbacks, PostgreSQL 14.24 and PGlite 0.2.13. A local HTTP server supported POST with JSON data and sender, GET with lowercase sender and data substitutions, JSON and plain hex replies, HTTP 503, malformed media and cancellation.

A read at block 1 receives the verified value from the verification contract at latest (block 2). After the first call reverted, the callback decoded the gateway response and read an offset set only in block 2. A mismatched revert sender and unrelated contract reverts triggered no gateway traffic. Tests cover gateway fallback, a second OffchainLookup raised by the onchain verifier, disabled resolution, bounded recursion, cancellation and raw eth_call. The ENS local batch included a nested success, a Solidity HttpError and a malformed-response Error. The contract validated both error payloads and returned the account, text and avatar record. Reverse lookup, resolver discovery and an outer multicall revert also completed through a verified gateway response.

The fixture was compiled with solc 0.8.37, optimization, Shanghai EVM target and no metadata hash. The tests exercise protocol behavior locally and do not change upstream case counts.

```sh
node testdata/indexing/ccip/oracle.mjs
go test -count=2 ./indexing -run 'Test(CallbackCCIPRead|CCIPRevertEnvelope|CCIPGatewayLimits|CallbackENSAvatarAtEvent|CallbackMulticallDeploymentAndOverrides|CallbackSimulateContractAndEstimate|SourceClientRequests|ManagedReadContractRetryRetainsCanonicalRoot)$'
go test -race -count=2 ./indexing -run 'Test(CallbackCCIPRead|CCIPRevertEnvelope|CCIPGatewayLimits|CallbackENSAvatarAtEvent|CallbackMulticallDeploymentAndOverrides|CallbackSimulateContractAndEstimate|SourceClientRequests|ManagedReadContractRetryRetainsCanonicalRoot)$'
```

[Implementation](../../../indexing/client_ccip.go) · [Go tests](../../../indexing/client_ccip_test.go) · [Solidity verifier](../../../testdata/indexing/ccip/GatewayResolver.sol) · [Pinned client oracle](../../../testdata/indexing/ccip/oracle.mjs)

The ordinary and race selections passed twice on both database backends. The separate address/text and avatar oracle still passed all 32 records. All Go packages compiled with `go test -run '^$' ./...`, `go vet ./...` and 503 repository tooling tests passed.
