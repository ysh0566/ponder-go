set -euo pipefail
regex='^(TestSourceIndexingProfile|TestSourceIndexingProfileEdges|TestProfilePrimitiveEquality|TestSourceIndexingPrefetch|TestSourceIndexingPrefetchProbes|TestClientProfileServiceContext|TestClientPrefetchCloseOwnsFailedSibling|TestClientPrefetchReaderSingleSettlement)$'
base=parity/runs/indexing-profile-m40/review-focused03
for mode in normal race; do
 d="$base/$mode"; mkdir -p "$d"; df -B1 . > "$d/capacity.txt"; test "$(df -B1 --output=avail . | tail -1)" -ge 5368709120
 flag=; test "$mode" != race || flag=-race
 printf '%s\n' "PONDER_INDEXING_PROFILE_NATIVE_OUTPUT=\$PWD/$d/native.json PONDER_INDEXING_PROFILE_PROBES_NATIVE_OUTPUT=\$PWD/$d/probes.json go test $flag -json ./indexing -run '$regex' -count=1" > "$d/command.txt"
 set +e
 PONDER_INDEXING_PROFILE_NATIVE_OUTPUT="$PWD/$d/native.json" PONDER_INDEXING_PROFILE_PROBES_NATIVE_OUTPUT="$PWD/$d/probes.json" go test $flag -json ./indexing -run "$regex" -count=1 > "$d/output.jsonl" 2> "$d/stderr.txt"
 status=$?; set -e; echo "$status" > "$d/exit-code.txt"; tail -4 "$d/output.jsonl"; cat "$d/stderr.txt"; test "$status" = 0 || exit "$status"
done
python3 - <<'PY'
import json
for mode in ['normal','race']:
 for name,count in [('native',24),('probes',4)]:
  p=f'parity/runs/indexing-profile-m40/review-focused03/{mode}/{name}.json';v=json.load(open(p));print(mode,name,len(v['cases']));assert len(v['cases'])==count and all(c['pass'] for c in v['cases'])
PY
