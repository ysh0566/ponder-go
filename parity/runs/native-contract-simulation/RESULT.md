# Go callback EVM calls, estimates and ABI simulation

The Go client executes calls and estimates at the event or specified block. Callers supply RPC transaction fields and optional transient account state. Calls also accept block overrides. Contract simulation ABI-encodes the arguments, accepts an optional data suffix and returns the decoded value plus the encoded request. EVM execution reverts propagate as RPC errors. Missing output for a function declaring returns uses the callback’s cancellable empty-response retry policy. A function declaring no returns can succeed with an empty response.

## Real EVM execution

A registered callback ran on Anvil 1.8.5 with separate business transactions and durable caches on PostgreSQL 14.24 and PGlite 0.2.13. The pinned ERC-20 was deployed at height 1 and minted at height 2.

- The event saw a zero supply and a reverting transfer at height 1. At height 2 a transfer simulation returned true, the prepared calldata retained its suffix, and its gas estimate equaled the direct node result. The recipient’s onchain balance remained zero.
- A void mint simulation returned nil and its effects were discarded. State and block overrides changed the returned value and apparent block number. Node estimates with a code override also matched. A contract receiving the aggregate3 selector could return arbitrary data.
- Replacing block 2 with an empty block changed the supply back to zero. Repeating the transfer simulation and estimate at that height returned the node’s revert.
- Both temporary deployments ran real EVM code returning 42. The creation-code and factory wrappers produced replayable calldata, and the expected target still had no code after simulation.
- A viem-signed EIP-7702 authorization recovered the authority when the target was omitted. The gas estimate matched a direct node base estimate plus the per-authorization adjustment. Focused controls exercise multiple authorizations, successful secondary estimates and the upstream 100,000-gas fallback.

The pinned viem 2.35.0 actions independently ran on a fresh Anvil chain with the same original token, simulations, reorg, temporary deployments and authorization vector. Its suffixed transfer estimate was 51,760 gas and its authorized estimate was 88,001 gas.

[Go actions](../../../indexing/client_simulation.go) · [Regression tests](../../../indexing/client_simulation_test.go)

## Verification

The following package selections passed both normally and with the Go race detector.

```sh
go test -count=2 -timeout=35m ./indexing -run 'Test(CallbackSimulateContractAndEstimate|SimulationRequestAndRetry|AuthorizationGasOnNode|AuthorizationGasAdjustment|DeploylessEVMCalls|CallbackMulticallDeploymentAndOverrides|SourceClientRequests|ManagedReadContractRetryRetainsCanonicalRoot)$'
go test ./config -run '^TestSourceCallbackContext$'
```

All module packages compiled with `go test -run '^$' ./...`. `go vet ./...` and all 503 repository tooling tests passed. The complete-module ordinary and race results in the main README refer to their separately recorded snapshot.
