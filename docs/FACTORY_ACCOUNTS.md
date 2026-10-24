# Native factory account targets

M26 connects `config.AccountSource.Factory` to native factory discovery and the
existing account transaction/transfer pipeline. Focused race checks pass29
leaves:19 native configuration boundaries,2 normalization/identity cases and8
actual EVM cases across PostgreSQL/PGlite, legacy/staged runtime and both initial
cache settings. Complete verification exits0: Go/race each pass1769 leaves with
zero failures/skips, including PG189/PGlite186. All19 packages/129 top-level
functions/46 disjoint sequential batches per mode complete with unchanged
start/end source fingerprint922/3066e64710d39e96b10634f0468c7f842505a93a401dede5b43ed42d70eef1ca.
Vet, builds, original/core/capture/type/SDK/differential gates pass. Frozen evidence
is `parity/runs/batch-25.json` and `batch-25-evidence/`. The source ledger remains
317/905 mapped (313Go plus4 retained SDK),588 unimplemented. These29 native
checks add no original source-test credit.

An account chooses static `Address` or `Factory`; specifying both, including an
explicit empty static array, is rejected. The factory accepts one/multiple
parents or an omitted parent wildcard, an ABI event, and a child parameter or
location. Parent arrays must contain valid addresses and must not be empty.
Effective factory bounds inherit the account bounds unless explicit. Discovery
may begin before business indexing; a later factory start or an end beyond the
account end is rejected. Source `latest` resolution and the complete TS config
DSL remain separate.

```go
childParameter := "child"
discoveryStart := uint64(0)
account := config.AccountSource{
    Name: "Wallet", Chain: "local", StartBlock: 100,
    Factory: &factory.Config{
        Address: parentAddress,
        Event: createdEvent,
        Parameter: &childParameter,
        FromBlock: &discoveryStart,
    },
}
```

Discovery scans from the earliest business or factory start. Contract and
account factories share the same durable child records and discovery requests
when their native factory IDs agree. Callbacks use the original birth-block
predicate; known children can match `Wallet:transaction:from`,
`Wallet:transaction:to`, `Wallet:transfer:from`, and `Wallet:transfer:to`.
Business indexing still respects the account's own start/end bounds.

The actual EVM tests exclude transfer99 before discovery, include a nested
birth-block transfer5, roll back a failed transfer7 handler, retry without
extra business rows, close/reopen both backends, switch cache settings, replace
the same parent transaction hash with another child6/post-transfer11, and
exercise child-signed outgoing transaction/transfer2. A second namespace starts
business indexing at block3 but must discover the child at block2. Orphan
business/discovery/cache entries are removed; a canonical prebirth protocol
trace is retained in legacy cold recovery.

Existing transaction boundaries are explicit: the legacy constructor commits
each event with its journal/checkpoint; staged commits the full block. A failed
transfer can leave an earlier committed transaction in legacy mode. Staged
recovery starts from safe and replays the live suffix, so external handler
side effects are not guaranteed to occur exactly once. Business rows and child
provenance remain idempotent. Original snapshots/helpers/assertions were not
changed to create these native results.

New account normalization preserves caller input and inherits both bounds.
Existing contract factory end defaults/IDs remain unchanged; changing those
under an already recorded config could reuse an interval with a different
factory ID. A complete configuration identity/upgrade design, original config
cases, full generated-ABI factory E2E and multichain scheduling remain open.

The initial API-only red recorded the real prebirth99 dispatch. Later focused
failures came from the new fixture's overlong schema name and an overbroad
orphan query that included a canonical block1 trace. Both original failures and
fixture corrections are preserved under `parity/runs/account-factory-m26/`.
