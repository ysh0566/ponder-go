#!/usr/bin/env bash
# New M42-only task fixture. NEVER points pg_ctl/initdb/cleanup at an old cluster.
# Run only after explicit fixture authorization. Both server and clients share
# this exec network namespace; logs/receipt survive the fixture's shutdown.
set -euo pipefail
B=/workspace/scratch/fac5abfa4c9e/ponder-cloud
source "$B/env.sh"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [[ $# -lt 2 ]]; then echo 'usage: fixture.sh NEW_RECEIPT_DIRECTORY COMMAND [ARGS...]' >&2; exit 2; fi
receipt=$(realpath -m "$1"); shift
case "$receipt" in "$ROOT"/parity/runs/multichain-atomic-m42/*) ;; *) echo 'Scoped receipt required' >&2; exit 2;; esac
[[ ! -e "$receipt" ]] || { echo 'Receipt already exists' >&2; exit 2; }
python3 "$B/check-capacity.py"
mkdir -p "$receipt"
dir=$(mktemp -d "$TMPDIR/ponder-m42-pg-XXXXXXXX")
printf '%s\n' "$dir" > "$receipt/owned-directory.txt"
printf '%s\n' 'New disposable cluster; preexisting PostgreSQL data directories are untouched.' > "$receipt/scope.txt"
cleanup() {
 local status=$?
 if pg_ctl -D "$dir/data" status > /dev/null 2>&1; then
  psql -XAt -h 127.0.0.1 -p 55432 -U agent -d postgres -c "SELECT json_build_object('databases',(SELECT json_agg(datname ORDER BY datname) FROM pg_database),'roles',(SELECT json_agg(rolname ORDER BY rolname) FROM pg_roles));" > "$receipt/final-inventory.json" 2> "$receipt/final-inventory.stderr" || status=1
  # pg_ctl is scoped solely to the path returned by this invocation's mktemp.
  pg_ctl -D "$dir/data" -m fast -w stop >> "$receipt/shutdown.log" 2>&1 || status=1
 fi
 printf '%s\n' "$status" > "$receipt/exit-code.txt"
 # Preserve this task-owned cluster for audit/retry; caller may explicitly
 # remove this exact owned-directory later as its ordinary fixture lifecycle.
 exit "$status"
}
trap cleanup EXIT
initdb -D "$dir/data" --no-locale -E UTF8 -A trust > "$receipt/initdb.log" 2>&1
pg_ctl -D "$dir/data" -l "$receipt/server.log" -o "-p 55432 -c listen_addresses='127.0.0.1' -c unix_socket_directories=''" -w start > "$receipt/start.log" 2>&1
actual=$(psql -XAt -h 127.0.0.1 -p 55432 -U agent -d postgres -c 'SHOW data_directory')
printf '%s\n' "$actual" > "$receipt/show-data-directory.txt"
[[ "$actual" == "$dir/data" ]] || { echo 'Connected server is not the new owned cluster; refusing test DDL' >&2; exit 1; }
createdb -h 127.0.0.1 -p 55432 -U agent ponder_test
psql -XAt -h 127.0.0.1 -p 55432 -U agent -d postgres -c "SELECT json_build_object('databases',(SELECT json_agg(datname ORDER BY datname) FROM pg_database),'roles',(SELECT json_agg(rolname ORDER BY rolname) FROM pg_roles));" > "$receipt/initial-inventory.json"
python3 - "$receipt/initial-inventory.json" <<'PYFIXTURE'
import json,sys
v=json.load(open(sys.argv[1]))
assert sorted(v['databases'])==['ponder_test','postgres','template0','template1'],v
assert all(r=='agent' or r.startswith('pg_') for r in v['roles']),v
PYFIXTURE
sha256sum "$ROOT/../ponder-upstream/packages/core/src/_test/setup.ts" > "$receipt/pinned-test-lifecycle.sha256"
printf '%s\n' 'Original pinned setupIsolatedDatabase may target only its vitest_<pool> databases and ponder_vitest_<pool>_public roles inside this newly owned cluster. No old-cluster data is reachable through this process endpoint.' > "$receipt/legacy-fixture-scope.txt"
export PONDER_TEST_POSTGRES='postgres://agent@127.0.0.1:55432/ponder_test?sslmode=disable'
export PONDER_M42_POSTGRES_DIRECTORY="$dir/data"
export PONDER_M42_POSTGRES_RECEIPT="$receipt"
export PATH="$ROOT/../ponder-upstream/node_modules/.bin:$PATH"
"$@"
