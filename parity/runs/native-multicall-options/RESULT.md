# Multicall deployment selection and state overrides

The high-level Go callback client reads Multicall3 from the configured chain metadata. A known deployment height is checked against the selected block. An explicit address takes precedence. When a caller supplies an account or state override, the client sends it with every aggregate chunk and keeps the results separate from ordinary per-contract reads. Raw aggregate calls with additional fields, gas limits or overrides reach the node intact.

## Backend and node checks

A Go callback runs inside an indexing service business transaction on each of PostgreSQL 14.24 and PGlite 0.2.13. Each uses a separate durable RPC cache and a fresh Anvil 1.8.5 chain with the upstream Multicall3 bytecode. Two small EVM contracts expose slot zero and transaction origin.

- The registered chain deployment at the event height returns the ordinary stored value 42 and zero origin. An explicit sender changes the observed origin, including when ordinary results are already cached.
- Two chunks with a sender and storage difference return 97 and the sender. A full empty storage replacement returns zero. A subsequent ordinary or explicitly addressed aggregate still returns 42 and zero origin.
- Raw aggregate requests with sender only, override only and both match the node byte for byte. A gas limit rejected by the node also fails through the raw callback client.
- A setup event before deployment fails before transport. At the deployment height the selection succeeds. Explicit addresses and immutable block selection are supported. Conflicting storage overrides, invalid accounts and negative balances are rejected.

[Implementation](../../../indexing/client_multicall.go) · [Regression tests](../../../indexing/client_multicall_deployment_test.go)

## Verification

The low-gas raw aggregate check failed against the preceding main tree because the callback client accepted a request rejected by Anvil. It passed against this implementation. A raw aggregate with an EIP-1898 block-hash selector also matched the node. The pinned viem 2.35.0 multicall action reproduced the expected configured-address, sender, storage difference, chunked and explicit-address results on a separate Anvil chain.

Ordinary and race modes each passed two repetitions of the following selection. The final block-hash selector additionally ran in ordinary mode and twice with the race detector.

```sh
go test -count=2 -timeout=35m ./indexing -run 'Test(CallbackMulticallDeploymentAndOverrides|MulticallDeploymentBounds|SourceClientRequests|RawAggregateFailurePolicy|ManagedClientMulticallRootIsJoinedAndSealed|ManagedClientSpeculativeFailureAllowsMulticallFallback|CallbackHistoricalFeesAndProof)$'
```

The generated callback context test in `./config`, including the existing ENS reverse action, passed in ordinary and race mode. All packages compiled, `go vet ./...` and all 503 repository tool tests passed.
