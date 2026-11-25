# Native ENS address and text records

Go indexing callbacks can resolve an ENS-normalized name to an Ethereum address, a multicoin byte string or text at the callback block or an explicit block. The Universal Resolver call includes the DNS wire name, the namehash-based record request and the selected gateway list. A missing record returns nil by default. Strict mode propagates recognized resolver errors. Transport and unexpected decoding errors propagate.

The pinned viem 2.35.0 actions read 12 records on a real Anvil 1.8.5 chain. The fixture stores replies under the hash of the complete incoming calldata. These vectors distinguish both address overloads, coin type zero, differently configured gateway arrays, an empty text key, UTF-8 and already-hashed labels, and null results represented by unset bytes, empty text and zero addresses. Independent setup transactions allow the Go tests to replay the same records without using the Go implementation to generate its own expected requests. The tests query two historical heights and replace the second block after a snapshot.

The verification script runs against the sibling pinned upstream and uses a fresh loopback chain. The checked-in resolver runtime was built from RecordResolver.sol with solc 0.8.37, optimization enabled, Shanghai EVM target and no metadata hash.

```sh
node testdata/indexing/ens/oracle.mjs
go test -count=2 ./indexing -run 'Test(CallbackENSRecordsAtEvent|ENSRecordErrors|CallbackENSResolverAtEvent|ENSDNSNamePackets)$'
go test -race -count=2 ./indexing -run 'Test(CallbackENSRecordsAtEvent|ENSRecordErrors|CallbackENSResolverAtEvent|ENSDNSNamePackets)$'
```

[Callback implementation](../../../indexing/client_ens_records.go) · [Real callback tests](../../../indexing/client_ens_records_test.go) · [Pinned upstream oracle](../../../testdata/indexing/ens/oracle.mjs)

Both Go commands passed on PostgreSQL 14.24 and PGlite 0.2.13, including known resolver errors and a same-height replacement after a snapshot. The upstream oracle also reproduced the checked-in vectors. All module packages compiled with `go test -run '^$' ./...`, `go vet ./...` completed successfully and all 503 repository tooling tests passed. Comparison of the three action lists in the pinned Ponder client with exported Go callback methods found 20 of 21 names. The remaining action is `GetEnsAvatar`.
