# Supplemental SQL lifetime / unused-prefetch diagnostics

These are the exact 11 overlay inputs used by `rebuild-lifetime-final-01`: eight instrumented implementation files and three test files. `inputs.json` preserves target-relative names and SHA-256 values. Historical `private` names remain unchanged for byte identity; they are now published supplemental diagnostics, not upstream originals. The overlay replaces implementation files with their historical instrumented versions: it is **not** a test of arbitrary future production changes, nor a replacement for the unoverlaid full acceptance suite.

Historical normal and race each passed 10 leaf scenarios (12 passing test nodes), zero failures/skips. Cases cover unknown commit before extractor SQL; finite success, handler error, committed callback error, unreadable unknown outcome, caller cancellation and owner Close with admitted SQL; and unused learned prediction under ordinary error, caller cancellation and owner Close. This proves only the observed instrumented PostgreSQL lifecycle boundaries, not PGlite parity, every scheduling interleaving, original-source parity or new source credit.

`normal.jsonl` and `race.jsonl` retain actual SQL/context/PID observations. Only the historical fixture data-directory string is replaced with `<fresh-task-owned-pgdata>` (seven occurrences per log). `result.json` records original and public hashes and normalized source paths. Private fixture metadata is omitted. No original receipt was changed; this public wrapper is not a new execution.

## Reproduce on a fresh local fixture

Prerequisites: the repository's existing Go dependencies/toolchain and PostgreSQL tools on PATH, an unprivileged local OS account, and a free loopback port. Do not use an existing database. Keep at least 5 GiB free and budget before running. The example uses trust authentication **only for this disposable loopback fixture**; choose an unused port with `PGPORT`. It changes no existing cluster or authentication configuration. Retain the new directory and logs; no deletion is performed.

Run from the repository root in **one shell/execution namespace** (startup, identity verification, both modes and shutdown together):

```bash
set -euo pipefail
repo="$PWD"
pub="$repo/parity/runs/finite-historical-m43/lifetime-supplemental-public"
run=$(mktemp -d "${TMPDIR:-/tmp}/ponder-lifetime-public.XXXXXXXX")
export PGPORT="${PGPORT:-55439}"
export PGUSER="$(id -un)"
export PONDER_M43_LIFETIME_EXPECT_PGDATA="$run/data"
export PONDER_TEST_POSTGRES="postgresql://${PGUSER}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"
python3 "$pub/generate_overlay.py" --repo-root "$repo" > "$run/overlay.json"
initdb -D "$run/data" --auth-local=trust --auth-host=trust > "$run/initdb.log" 2>&1
# Register shutdown before startup, including error paths; never remove the fixture.
trap 'pg_ctl -D "$run/data" -m fast -w stop > "$run/shutdown.log" 2>&1 || true' EXIT
pg_ctl -D "$run/data" -l "$run/server.log" -o "-h 127.0.0.1 -p $PGPORT -k $run" -w start
actual=$(psql "$PONDER_TEST_POSTGRES" -Atqc 'SHOW data_directory')
test "$actual" = "$PONDER_M43_LIFETIME_EXPECT_PGDATA"
select='^TestPrivateM43(LastWriteLifetimeSQL|UnknownBeforeExtractorSQL|UnusedPredictionLifetime)$'
set +e
go test -overlay "$run/overlay.json" -count=1 -json -run "$select" ./historical > "$run/normal.jsonl" 2> "$run/normal.stderr"
normal=$?
go test -race -overlay "$run/overlay.json" -count=1 -json -run "$select" ./historical > "$run/race.jsonl" 2> "$run/race.stderr"
race=$?
set -e
pg_ctl -D "$run/data" -m fast -w stop > "$run/shutdown.log" 2>&1
trap - EXIT
printf 'normal=%s race=%s retained=%s\n' "$normal" "$race" "$run"
test "$normal" -eq 0 && test "$race" -eq 0
```

The generator prints absolute `Replace` mappings to stdout, verifies all input hashes and writes no active `.go` files. New logs are new evidence; compare scenario counts and failures/skips rather than expecting identical timestamps/PIDs. This publication preparation did not rerun the database tests.
