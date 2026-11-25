# ENS avatars in Go indexing callbacks

The pinned viem 2.35.0 implementation and registered Go indexing callbacks read twenty avatar records on Anvil 1.8.5. A real Solidity resolver selects responses by the hash of the complete request. A separate token contract stores independent ERC-721 and ERC-1155 URIs. The HTTP gateway serves known metadata GETs and image HEADs. PostgreSQL and PGlite callbacks run with separate business stores and RPC caches. These are native regression vectors, with no change to the upstream case counts.

Records include inline data, Latin-1 SVG encoding, bare IPFS CIDs, IPFS/IPNS, Arweave, unusable media, ERC-721, ERC-1155, DID references, token ID substitution, remote JSON, embedded JSON and missing records. The test changes the ENS record and the NFT contract in block 2. A lookup at block 1 reads the historical ENS record and, as viem does, reads token metadata at latest. Replacing block 2 restores the original ENS record at that height. Strict mode preserves resolver errors. Invalid media returns nil, and HTTP requests obey caller cancellation.

The default HTTP transport checks the resolved IP at socket creation. An explicit HTTP client supports controlled private gateways. The tests cover a default loopback rejection, a working explicitly configured gateway, and an interrupted image request. AvatarToken.hex was built from the accompanying Solidity with solc 0.8.37, optimizations enabled, Shanghai EVM and no metadata hash.

```sh
node testdata/indexing/ens/oracle.mjs
go test -count=2 ./indexing -run 'Test(CallbackENSAvatarAtEvent|AvatarGateway|CallbackENSRecordsAtEvent|ENSRecordErrors|CallbackENSResolverAtEvent)$'
go test -race -count=2 ./indexing -run 'Test(CallbackENSAvatarAtEvent|AvatarGateway|CallbackENSRecordsAtEvent|ENSRecordErrors|CallbackENSResolverAtEvent)$'
```

[Implementation](../../../indexing/client_ens_avatar.go) · [Go tests](../../../indexing/client_ens_avatar_test.go) · [Upstream oracle](../../../testdata/indexing/ens/avatar-oracle.mjs)

Both package selections passed twice, ordinarily and with the race detector. The viem oracle also verified the 12 earlier address and text vectors. All packages compiled with `go test -run '^$' ./...`, `go vet ./...` passed and all 503 repository tooling tests passed.
