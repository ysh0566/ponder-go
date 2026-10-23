# Native log factory

`config.Contract.Factory` selects child contracts discovered from a factory event. It is mutually exclusive with `Contract.Address`. The existing single-chain engine injects the chain ID and source name; a missing factory `FromBlock` defaults to the contract's start block.

```go
parameter := "child"
contract := config.Contract{
    Name: "Token", Chain: "local", ABI: tokenABI, StartBlock: 0,
    Factory: &factory.Config{
        Address: "0x1000000000000000000000000000000000000000",
        Event: abi.Event{
            Name: "Created", Type: "event",
            Inputs: []abi.Parameter{{Name: "child", Type: "address"}},
        },
        Parameter: &parameter,
    },
}
```

`Parameter` can select an indexed input or a named tuple path such as `marketParams.oracle`. Alternatively set `Location` to `topic1`, `topic2`, `topic3` or an explicit `offsetN`. `factory.Build` reproduces the pinned builder's selector, location, address sorting, deduplication order and ID. `abi.BytesConsumed` and `abi.NestedOffset` reproduce the pinned head-offset helpers. Dynamic ABI heads follow that helper's behavior; full source filtering and dynamic tuple-path extensions still require their original tests.

For each canonical block, the engine fetches factory logs before child-contract logs. An empty child set skips the child log request; it never becomes a wildcard request. A child can produce indexed logs in its creation block. Duplicate factory definitions share their discovery ID.

Discovery lives in `<owned shadow>_sync.factory_addresses`, with factory ID, chain, child address, block number/hash and log index. Replaying one block replaces that block's discoveries in a transaction. Queries expose the earliest creation block at or before the requested height. Addresses persist across PostgreSQL connection restarts and PGlite disk reopen. Business rows, journal and checkpoints remain in the indexing schema and its event transaction.

The sync discovery transaction can commit before a handler fails. Retrying the handler reuses/replaces the same discovery without duplicating business effects. A chain rewind removes discoveries above the common ancestor and the orphan branch's business writes; new canonical blocks register their own children. `TestFactoryRuntime` checks failure after a business write, disk/connection reopen, creation-block logs, later logs, replay, fork replacement and a second reopen on both real backends.

Native extraction rejects malformed/truncated child-address words. It preserves hex case in the extraction helper; persisted addresses use lowercase. This explicit validation is additional native behavior for malformed RPC input.

The six original builder tests and seven offset tests are exact source mappings. The two backend integration tests and 66 synthetic builder differential vectors are separate evidence. Original full factory E2E, all runtime filter/fragments cases, account/trace factory sources and multichain scheduling remain pending. See `parity/test-ledger.json` and `parity/feature-ledger.json`.

`Store.RevertAndRewind` combines indexing undo and sync rewind in one transaction on the current shared native backend. A block header is saved before its first handler commit, while its completed interval is saved only after successful processing. Tests also cover a lost handler commit acknowledgement followed by database reopen and a fork before block completion. Distinct physical sync/indexing databases would need a separate durable coordinator and are not implemented.
