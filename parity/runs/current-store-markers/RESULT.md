# Reopening current journal databases

An existing `json-v3` journal must have both its business namespace and its sync namespace. The business fingerprint identifies the journal format, schema, ordering and write mode. The sync ownership row identifies the paired business namespace. Managed application startup also requires the existing application metadata row. New database initialization writes its markers in the same transaction that creates both namespaces.

The regression tests removed a fingerprint, removed a sync owner, changed a sync owner, removed each namespace in turn and removed the application metadata. The previous revision accepted all six cases on each backend. The updated revision returned a migration error in all twelve cases. Queries after each failed reopen verified that the missing objects stayed missing and existing markers and business rows retained their values.

| Verification | Result |
| --- | --- |
| Go storage, normal and race | Each mode passed 14 top-level tests and 92 leaf cases on the checked revision with zero skips or failures. |
| Pinned original database tests | All 18 declarations passed on PostgreSQL 14.24 and all 18 passed on PGlite 0.2.13. The source returns early on PGlite for the snapshot-copy case. |
| Clean Go checkout | Module build and vet passed. |

One earlier run of the 100 ms PostgreSQL lease test observed an expired lease at its first write. The final runs exercised the same case again in both modes. The original parity inventory still contains 580 mapped declarations, 320 pending ports and five retired cache migrations. [Native and upstream execution digests](results.json) · [Original inventory](../public-provider-archives/upstream.json)

To rerun the marker and format regressions, set `PONDER_UPSTREAM` to the pinned upstream checkout with core dependencies installed, and point `PONDER_TEST_POSTGRES` at a disposable PostgreSQL database.

```sh
go test -count=1 -run '^(TestStoreFormatMarkers|TestStorageVersionRejectsOldJournal)$' ./store
go test -race -count=1 -run '^(TestStoreFormatMarkers|TestStorageVersionRejectsOldJournal)$' ./store
```
