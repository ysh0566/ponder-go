#!/usr/bin/env bash
# Supplemental native controls; this script never edits production source.
# Must run inside the scoped M42 fresh PostgreSQL fixture.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
[[ $# == 1 ]] || { echo 'usage: negative-controls.sh NEW_OUTPUT_DIRECTORY' >&2; exit 2; }
out=$(realpath -m "$1")
case "$out" in "$root"/parity/runs/multichain-atomic-m42/*) ;; *) exit 2;; esac
[[ ! -e "$out" && -n "${PONDER_M42_POSTGRES_DIRECTORY:-}" && -n "${PONDER_TEST_POSTGRES:-}" && -n "${PONDER_M42_POSTGRES_RECEIPT:-}" ]] || exit 2
receipt=$(realpath -m "$PONDER_M42_POSTGRES_RECEIPT")
case "$receipt" in "$root"/parity/runs/multichain-atomic-m42/*) ;; *) exit 2;; esac
case "$PONDER_M42_POSTGRES_DIRECTORY" in /workspace/scratch/fac5abfa4c9e/ponder-cloud/tmp/ponder-m42-pg-*/data) ;; *) exit 2;; esac
[[ "$(cat "$receipt/show-data-directory.txt")" == "$PONDER_M42_POSTGRES_DIRECTORY" ]] || exit 2
actual=$(psql -XAt "$PONDER_TEST_POSTGRES" -c 'SHOW data_directory')
[[ "$actual" == "$PONDER_M42_POSTGRES_DIRECTORY" ]] || { echo 'Negative controls require verified fresh M42 fixture' >&2; exit 2; }
mkdir -p "$out"
date -u +%Y-%m-%dT%H:%M:%SZ > "$out/started-at.txt"
printf '%s\n' "$receipt" > "$out/fixture-receipt.txt"
printf '%s\n' "$actual" > "$out/show-data-directory.txt"
node --input-type=module -e 'import {sourceFingerprint} from "./tools/go-source-fingerprint.mjs"; console.log(JSON.stringify(sourceFingerprint(process.cwd()),null,2))' > "$out/native-inputs-before.json"
sha256sum tools/multichain-atomic-negative-controls.sh tools/go-source-fingerprint.mjs > "$out/tool-inputs-before.sha256"
python3 - "$root" "$out" <<'PY'
import hashlib,json,pathlib,sys
root,out=map(pathlib.Path,sys.argv[1:])
p=root/'store/multichain_actions.go';s=p.read_text();(out/'production.go.txt').write_text(s)
(out/'production.sha256').write_text(hashlib.sha256(p.read_bytes()).hexdigest()+'\n')
controls={
 'historical-singlechain': ('\t\trows, err := tx.Query(ctx, "SELECT c.chain_name', '\t\tif err := a.singleRuntimeChain(ctx, tx, target.ChainID); err != nil { return err }\n\t\trows, err := tx.Query(ctx, "SELECT c.chain_name'),
 'separate-setup-checkpoints': ('\t\tfor _, state := range states {', '\t\t// Deliberately broken: commit setup before its checkpoint metadata.\n\t\tif err := tx.Commit(ctx); err != nil { return err }\n\t\tfor _, state := range states {'),
}
for name,(old,new) in controls.items():
 assert s.count(old)==1,(name,s.count(old))
 target=out/(name+'.go.txt');target.write_text(s.replace(old,new))
 (out/(name+'.overlay.json')).write_text(json.dumps({'Replace':{str(p):str(target)}},indent=2)+'\n')
(out/'inputs.json').write_text(json.dumps({p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(out.iterdir()) if p.is_file()},indent=2)+'\n')
PY
for control in historical-singlechain separate-setup-checkpoints; do
 if [[ "$control" == historical-singlechain ]]; then
  pattern='^TestMultichainPrimitiveAtomicSetupAndGroups$'
 else
  pattern='^TestMultichainPrimitiveFailuresAndCommitEvidence$/(postgres|pglite)/metadata$'
 fi
 printf '%s\n' "go test -overlay=$out/$control.overlay.json ./store -run $pattern -count=1 -v" > "$out/$control.command.txt"
 set +e
 go test -overlay="$out/$control.overlay.json" ./store -run "$pattern" -count=1 -v > "$out/$control.red.log" 2>&1
 status=$?
 set -e
 printf '%s\n' "$status" > "$out/$control.exit-code.txt"
 [[ $status == 1 ]] || { cat "$out/$control.red.log"; echo 'negative control failed to fail' >&2; exit 1; }
 grep -q -- '--- FAIL: .*postgres' "$out/$control.red.log"
 grep -q -- '--- FAIL: .*pglite' "$out/$control.red.log"
done
printf '%s\n' "go test ./store -run '^TestMultichainPrimitive' -count=1 -v" > "$out/restored-green.command.txt"
go test ./store -run '^TestMultichainPrimitive' -count=1 -v > "$out/restored-green.log" 2>&1
node --input-type=module -e 'import {sourceFingerprint} from "./tools/go-source-fingerprint.mjs"; console.log(JSON.stringify(sourceFingerprint(process.cwd()),null,2))' > "$out/native-inputs-after.json"
sha256sum tools/multichain-atomic-negative-controls.sh tools/go-source-fingerprint.mjs > "$out/tool-inputs-after.sha256"

python3 - "$root" "$out" <<'PY'
import hashlib,json,pathlib,sys
root,out=map(pathlib.Path,sys.argv[1:])
assert (out/'native-inputs-before.json').read_bytes()==(out/'native-inputs-after.json').read_bytes()
assert (out/'tool-inputs-before.sha256').read_bytes()==(out/'tool-inputs-after.sha256').read_bytes()
assert hashlib.sha256((root/'store/multichain_actions.go').read_bytes()).hexdigest()==(out/'production.sha256').read_text().strip()
(out/'result.json').write_text(json.dumps({'productionUnchanged':True,'nativeInputs':json.loads((out/'native-inputs-before.json').read_text()),'finishedNativeInputs':json.loads((out/'native-inputs-after.json').read_text()),'scriptHash':hashlib.sha256((root/'tools/multichain-atomic-negative-controls.sh').read_bytes()).hexdigest(),'source_fingerprint':json.loads((out/'native-inputs-before.json').read_text()),'finished_source_fingerprint':json.loads((out/'native-inputs-after.json').read_text()),'driver_sha256':hashlib.sha256((root/'tools/multichain-atomic-negative-controls.sh').read_bytes()).hexdigest(),'started_at':(out/'started-at.txt').read_text().strip(),'controls':['historical-singlechain','separate-setup-checkpoints'],'negativeControlsFailedOnBothBackends':True,'restoredGreen':True,'originalCredit':0,'hashes':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(out.iterdir()) if p.is_file()}},indent=2)+'\n')
PY
