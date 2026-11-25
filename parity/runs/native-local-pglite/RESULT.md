# Generated Go projects with a local PGlite database

The generated project declares PGlite 0.2.13 as its local Node dependency. The test runs its Go code through the CLI with a project-local driver and the upstream module override cleared. The first run selects the default directory and indexes a real ERC-20 Transfer on Anvil 1.8.5. HTTP SQL returns balances of −25 and 25. After shutdown the CLI lists the development namespace. The database is moved, then a new start selects that directory with the same namespace and an unused inherited PostgreSQL URL. The same balances remain queryable, including through the views published after catch-up. The separate serve command rejects PGlite before creating a directory.

Offline administration uses the same transactions and deployment manifests as the PostgreSQL commands. Tests switch a published view between deployments with incompatible column types, prune the old one, preserve the published one and query its data after reopening. A second process cannot open an active directory. The same exclusion applies through symlinks. A missing target is left absent. In-process pruning also recognizes stores pinned on the current engine.

| Check | Selection |
| --- | --- |
| Generated project and offline CLI | `go test -count=1 -run '^(TestOfflinePGliteCommands\|TestGeneratedLocalPGlite\|TestInitGoProject)$' ./cmd/ponder` and the same with `-race` |
| Full CLI package | `go test -count=1 ./cmd/ponder` and `go test -race -count=1 ./cmd/ponder` with a separate task-owned database for prune |
| Database and runtime regression | Existing engine-kill, directory ownership, managed domains, publication and database selection tests on both backends as applicable |
| Pinned original | Vitest executes `packages/core/src/build/pre.test.ts` against Ponder 0.18.2 |
| Static checks | `go test -run '^$' ./...` and `go vet ./...` |

The source inventory stays at 883 Go mappings. This milestone adds native application and deployment coverage.

All 24 top-level CLI tests passed in ordinary and race mode with zero skips. Each mode also passed all 11 selected store, runtime and configuration tests with zero skips. A separate PGlite managed-bootstrap run passed both commit boundaries. The seven original database configuration cases passed. A clean npm install of the generated manifest resolved PGlite 0.2.13. Module-wide build and vet passed, as did all 503 repository verification-tool checks. Full module execution is tracked separately from these results.
