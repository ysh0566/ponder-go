#!/usr/bin/env bash
# Reconstructed M43 supplemental controls. Only Go overlay files are mutated.
# Requires the exact new task-owned cluster; never starts or resets a database.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
[[ $# == 1 ]] || { echo 'usage: negative-controls.sh NEW_OUTPUT_DIRECTORY' >&2; exit 2; }
out=$(realpath -m "$1")
case "$out" in "$root"/parity/runs/finite-historical-m43/*) ;; *) exit 2;; esac
[[ ! -e "$out" && -n "${PONDER_M43_POSTGRES_DIRECTORY:-}" && -n "${PONDER_TEST_POSTGRES:-}" && -n "${PONDER_M43_POSTGRES_RECEIPT:-}" ]] || exit 2
receipt=$(realpath -m "$PONDER_M43_POSTGRES_RECEIPT")
case "$receipt" in "$root"/parity/runs/finite-historical-m43/*) ;; *) exit 2;; esac
owned=$(cat "$receipt/owned-directory.txt")
case "$owned" in "${TMPDIR:?}"/ponder-m43-pg-*) ;; *) exit 2;; esac
[[ "$PONDER_M43_POSTGRES_DIRECTORY" == "$owned/data" && "$(realpath "$owned/data")" == "$owned/data" ]] || exit 2
[[ "$(cat "$receipt/show-data-directory.txt")" == "$PONDER_M43_POSTGRES_DIRECTORY" ]] || exit 2
actual=$(psql -XAt "$PONDER_TEST_POSTGRES" -c 'SHOW data_directory')
[[ "$actual" == "$PONDER_M43_POSTGRES_DIRECTORY" ]] || { echo 'Negative controls require verified fresh M43 fixture' >&2; exit 2; }
mkdir -p "$out"
date -u +%Y-%m-%dT%H:%M:%SZ > "$out/started-at.txt"
printf '%s\n' "$receipt" > "$out/fixture-receipt.txt"
printf '%s\n' "$actual" > "$out/show-data-directory.txt"
node --input-type=module -e 'import {sourceFingerprint} from "./tools/go-source-fingerprint.mjs"; console.log(JSON.stringify(sourceFingerprint(process.cwd()),null,2))' > "$out/native-inputs-before.json"
sha256sum tools/finite-historical-negative-controls.sh tools/go-source-fingerprint.mjs > "$out/tool-inputs-before.sha256"
python3 - "$root" "$out" <<'PY'
import hashlib,json,pathlib,sys
root,out=map(pathlib.Path,sys.argv[1:])
p=root/'historical/runner.go';s=p.read_text();(out/'production.go.txt').write_text(s)
(out/'production.sha256').write_text(hashlib.sha256(p.read_bytes()).hexdigest()+'\n')
controls={
 'discard-refetch':('events = replacement','_ = replacement'),
 'repeat-setup':('if !snapshot.Initialized {','if true {'),
 'retain-resume-complete':('\t\tif snapshot.Initialized {','\t\tif false {'),
}
for name,(old,new) in controls.items():
 assert s.count(old)==1,(name,s.count(old))
 target=out/(name+'.go.txt');target.write_text(s.replace(old,new))
 (out/(name+'.overlay.json')).write_text(json.dumps({'Replace':{str(p):str(target)}},indent=2)+'\n')
(out/'inputs.json').write_text(json.dumps({p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(out.iterdir()) if p.is_file()},indent=2)+'\n')
PY
for control in discard-refetch repeat-setup retain-resume-complete; do
 if [[ "$control" == discard-refetch ]]; then
  pattern='^TestFiniteHistoricalRunnerRetry$/postgres/sticky_false$'
  reason='missing-field attempts: InvalidEventAccessError key=block.gasUsed retries=9'
 elif [[ "$control" == repeat-setup ]]; then
  pattern='^TestFiniteHistoricalRunner$/(postgres|pglite)$'
  reason='multichain runtime already initialized or has pending state'
 else
  pattern='^TestFiniteHistoricalRunnerResumeCompleteCache$/(postgres|pglite)$'
  reason='resume complete-cache conflict: sqlstate=23505 constraint=account_pkey prefix_unchanged=true resumed=true committed_groups=0 setup_unchanged=true'
 fi
 printf '%s\n' "go test -overlay=$out/$control.overlay.json ./historical -run $pattern -count=1 -v" > "$out/$control.command.txt"
 set +e
 go test -overlay="$out/$control.overlay.json" ./historical -run "$pattern" -count=1 -v > "$out/$control.red.log" 2>&1
 status=$?
 set -e
 printf '%s\n' "$status" > "$out/$control.exit-code.txt"
 [[ $status == 1 ]] || { cat "$out/$control.red.log"; echo 'negative control failed to fail' >&2; exit 1; }
 grep -Fq -- "$reason" "$out/$control.red.log"
 ! grep -Fq -- '[build failed]' "$out/$control.red.log"
 if [[ "$control" == discard-refetch ]]; then
  grep -Fq -- '--- FAIL: TestFiniteHistoricalRunnerRetry/postgres/sticky_false ' "$out/$control.red.log"
  grep -Fq -- "$reason missing=" "$out/$control.red.log"
 elif [[ "$control" == repeat-setup ]]; then
  for backend in postgres pglite; do grep -Fq -- "--- FAIL: TestFiniteHistoricalRunner/$backend " "$out/$control.red.log"; done
 else
  for backend in postgres pglite; do grep -Fq -- "--- FAIL: TestFiniteHistoricalRunnerResumeCompleteCache/$backend " "$out/$control.red.log"; done
  python3 - "$out/$control.red.log" "$reason" <<'PY'
import pathlib,sys
log=pathlib.Path(sys.argv[1]).read_text(); reason=sys.argv[2]
observations={'postgres':'pgconn_structured','pglite':'pglite_cause_text'}
for backend in ['postgres','pglite']:
 header='=== RUN   TestFiniteHistoricalRunnerResumeCompleteCache/'+backend+'\n'
 assert log.count(header)==1,(backend,'missing or duplicate run header')
 start=log.index(header)+len(header); end=log.find('\n=== RUN ',start)
 segment=log[start:end if end>=0 else len(log)]
 assert reason+' backend='+backend+' observation='+observations[backend]+' waits=[] ' in segment,(backend,'missing exact SQL conflict and unchanged durable prefix')
PY
 fi
done
printf '%s\n' "go test ./historical -run '^(TestFiniteHistoricalRunner|TestFiniteHistoricalRunnerRetry|TestFiniteHistoricalRunnerResumeCompleteCache)$' -count=1 -v" > "$out/restored-green.command.txt"
go test ./historical -run '^(TestFiniteHistoricalRunner|TestFiniteHistoricalRunnerRetry|TestFiniteHistoricalRunnerResumeCompleteCache)$' -count=1 -v > "$out/restored-green.log" 2>&1
! grep -Eq -- '--- (FAIL|SKIP):' "$out/restored-green.log"
node --input-type=module -e 'import {sourceFingerprint} from "./tools/go-source-fingerprint.mjs"; console.log(JSON.stringify(sourceFingerprint(process.cwd()),null,2))' > "$out/native-inputs-after.json"
sha256sum tools/finite-historical-negative-controls.sh tools/go-source-fingerprint.mjs > "$out/tool-inputs-after.sha256"
python3 - "$root" "$out" <<'PY'
import hashlib,json,pathlib,sys
root,out=map(pathlib.Path,sys.argv[1:])
assert (out/'native-inputs-before.json').read_bytes()==(out/'native-inputs-after.json').read_bytes()
assert (out/'tool-inputs-before.sha256').read_bytes()==(out/'tool-inputs-after.sha256').read_bytes()
assert hashlib.sha256((root/'historical/runner.go').read_bytes()).hexdigest()==(out/'production.sha256').read_text().strip()
controls=[{'name':'discard-refetch','test':'TestFiniteHistoricalRunnerRetry','backends':['postgres'],'suffix':'/sticky_false','reason':'missing-field attempts: InvalidEventAccessError key=block.gasUsed retries=9'},{'name':'repeat-setup','test':'TestFiniteHistoricalRunner','backends':['postgres','pglite'],'suffix':'','reason':'multichain runtime already initialized or has pending state'},{'name':'retain-resume-complete','test':'TestFiniteHistoricalRunnerResumeCompleteCache','backends':['postgres','pglite'],'suffix':'','reason':'resume complete-cache conflict: sqlstate=23505 constraint=account_pkey prefix_unchanged=true resumed=true committed_groups=0 setup_unchanged=true'}]
(out/'result.json').write_text(json.dumps({'productionUnchanged':True,'nativeInputs':json.loads((out/'native-inputs-before.json').read_text()),'finishedNativeInputs':json.loads((out/'native-inputs-after.json').read_text()),'scriptHash':hashlib.sha256((root/'tools/finite-historical-negative-controls.sh').read_bytes()).hexdigest(),'started_at':(out/'started-at.txt').read_text().strip(),'controls':controls,'negativeControlsFailedInDeclaredScope':True,'restoredGreen':True,'originalCredit':0,'hashes':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(out.iterdir()) if p.is_file()}},indent=2)+'\n')
PY
