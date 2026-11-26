# Reusing native callback RPC results after a fork

The atomic Go runtime saves the highest identifiable block dependency of a cached JSON-RPC request in the existing `rpc_request_results.block_number` column. The event transaction and the chain-wide cache writer use the same rule. The stored request key remains the SHA-256 digest of the original envelope.

| Request | Dependency used by a rewind |
| --- | --- |
| Recognized methods with an explicit block quantity | The block at the method's designated parameter, including genesis |
| EIP-1898 state reads with a single `blockNumber` member | The explicit block |
| `eth_getLogs` with numeric start and end and no block hash | The inclusive end of the range |
| Hash-only, tagged, unrecognized or ambiguous requests | Unknown height, cleared when the chain rewinds |

A PostgreSQL 14.24 and PGlite 0.2.13 scenario uses the normal Go RPC client, a deterministic counting transport and an event transaction at block 3. After a rewind to block 2, replay hits genesis and block 2 without contacting the node. A block 3 read and a hash-only read each reach the transport again. A globally replayed peer preserves its own block 3 boundary and discards its block 4 read. A third, unaffected chain retains its block 9 result. An aborted callback at block 4 leaves no result behind. Both databases store height 9,007,199,254,740,993 exactly. The method tests also cover malformed endpoints, unsupported shapes and the signed BIGINT boundary. [Native cases](../../../store/native_rpc_height_test.go)

The focused store selection passed 19 leaf tests in each of ordinary and race modes. Six staged runtime controls passed in each mode on PostgreSQL and PGlite using local Anvil. There were zero skips in those selections. The store count includes the table-driven selector check as one test. The existing source request hash and block-parameter vectors also passed separately in both modes.

```sh
for mode in ordinary race; do
  flags=()
  if [[ $mode == race ]]; then flags=(-race); fi
  go test "${flags[@]}" -p 1 -count=1 -timeout=25m -run '^(TestNativeRPCCacheHeight|TestNativeRPCCacheRewind|TestProtocolCacheBoundaries|TestSourceRPCBoundaries)$' ./store
  go test "${flags[@]}" -p 1 -count=1 -timeout=35m -run '^TestStagedRuntimePersistence$' ./runtime
done
```

At upstream Ponder 0.18.2 commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`, six selected request, insert, read and prune cases passed against a disposable PostgreSQL server. The filter excluded 46 cases. Run the following command from `packages/core` with Anvil 1.8.5 available and `DATABASE_URL` set for the disposable server.

```sh
node_modules/.bin/vitest run --maxWorkers=1 --hookTimeout=45000 src/sync-store/index.test.ts src/indexing/client.test.ts -t 'insertRpcRequestResults|getRpcRequestResults|pruneRpcRequestResult|request\(\) (block dependent method|non-block dependent method)$'
```

All 50 Go packages compiled with `go test -p 1 -run '^$' ./...` and passed `go vet -p 1 ./...`. The repository verification tools passed 503 Node.js cases and 43 Python cases. The module-wide ordinary and race suites run separately from these selections.
