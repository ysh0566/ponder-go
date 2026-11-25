# Database configuration

Source configuration builds now resolve PGlite directories, PostgreSQL pool settings and ordering before compiling chains. The seven original pre-build cases check the default directory, explicit backend, environment precedence, connection errors and nested TLS settings. Six original PostgreSQL name cases check field, URL, environment and default precedence. The displayed location excludes URL userinfo and TLS options. [Usage](../../../docs/SOURCE_CONFIGURATION.md)

| Verification | Result |
| --- | --- |
| Pinned Ponder 0.18.2 original tests | All 13 declarations passed in an isolated network namespace. |
| Complete Go configuration package | Normal and race modes each passed 10 top-level tests and the same 77 leaf cases with zero skips. |
| Original chain and indexing builder regression | Both native package runs also executed the 40 original assertions in the existing configuration file. Each reported 39 passes and one original skip. |
| Supplemental checks | Upstream and Go checks covered socket and IPv6 URLs, encoded names, option defaults and in-memory PGlite. Go also checked error redaction and independence between builds. |
| Integrated module | Clean Go build and vet passed. |

The ledger maps 593 of 905 declarations, with 307 awaiting implementation and five retired cache migrations. Regenerating the inventory independently reproduced all 905 source identities, assertions, inputs, scope and mappings. The original inventory still contains 875 passes, 18 failures, ten source skips and two source todos. [Run digests and source IDs](results.json) · [Full original inventory](../public-provider-archives/upstream.json)

Run `go test -count=1 ./config` and `go test -race -count=1 ./config` from the module with the prepared pinned sibling checkout and Foundry 1.8.5 available. The fixed source files are `packages/core/src/build/pre.test.ts` and `packages/core/src/utils/pg.test.ts`.
