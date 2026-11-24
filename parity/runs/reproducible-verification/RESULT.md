# Reproducible configuration and upstream capture verification

Ponder 0.18.2 is pinned to `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. This stage fixes two clean-checkout failures in the verification path. The native configuration test now builds from four versioned inputs. The full driver binds its four freshly generated fixtures to its own reports. The historical verifier still uses the frozen archive for callers without a current run ID.

## Configuration

The four configurations come from the original call-through capture with SHA-256 `0513f81e7d41a481f3377035f76931cf08c00c1be11377841b4b62d6d4de8181`. The native test compares each prepared input with the argument observed in a fresh call to the upstream builder. The original declaration text and source hashes bind each configuration to the test that supplied it.

On a standalone checkout without the archived configuration capture, Go 1.25.11 ran the full configuration package in normal and race modes. Each run passed six top-level tests and 58 subtests. Both modes matched the pinned original on all 39 active runtime declarations and all ten type-error directives. The remaining declaration carries an upstream skip. Nine directives use the four versioned inputs and one uses the existing filter compiler control.

```sh
go test -count=1 -json ./config > normal.jsonl
go test -count=1 -race -json ./config > race.jsonl
node tools/config-pristine-oracle.mjs
node tools/config-live-results.mjs --normal=normal.jsonl --race=race.jsonl --output=config-comparison.json
```

## Fresh original runs

The original test command, test file, raw Vitest report, individual outcomes, capture file and source file hashes are checked together. For SQL, the proof also includes the SHA-256 of the current capture report.

| Original file | Passed in full original file | Declarations in fixture | Source and harness files checked |
| --- | ---: | ---: | ---: |
| Runtime events | 11 | 6 | 26 |
| Runtime filters | 23 | 9 | 26 |
| Realtime Bloom | 8 | 8 | 26 |
| SQL parser | 14 | 14 | 16 |

The ordinary `sh tools/verify.sh` entry point ran in a separate loopback network with Anvil 1.8.5 and PostgreSQL 14.24. Capture run `20261124T201937-1` passed the SQL step that previously rejected its regenerated fixture. Its fresh SQL report has SHA-256 `55bd6811791fa710877386a299b8463aafb461fc32b32d18c5d90c28c7215a06`. A separate pristine SQL execution also passed all 14 cases. The source comparison checked 19 assertions, 38 operations and 61 vectors.

The same driver subsequently passed the complete original sync store (38), historical sync (40) and realtime sync (30) suites on each of PostgreSQL and PGlite.

The repository's Node controls passed all 517 cases using the indexed historical inputs and the explicit fixtures required by the three indexing service suites. The Python archive and history tools passed all 19 tests. Additional checks rejected a mismatched run, a substituted command, missing source entries, changed harness files and runtime evidence with the Anvil version removed. The four published historical fixtures were checked against the frozen manifest.

The native source inventory for the clean checkout contains 1,337 files with SHA-256 `54f513ed1faee4349e1e3ea1f00c4a6272ea1e1748482beab2302b85832abe3f`. The module passed `go vet ./...` and both `./cmd/ponder` and `./examples/erc20` built with clean Git provenance. This is a scoped verification result. Formal coverage remains 543 of 905 upstream records.

The clean-checkout config comparison has SHA-256 `92e77b4e0dca5e09b30923ddc0cd2ecce347c8bb1f3c9683209d57611b78f4b6`.
