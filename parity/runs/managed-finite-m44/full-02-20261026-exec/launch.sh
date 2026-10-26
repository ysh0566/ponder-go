#!/usr/bin/env bash
set -uo pipefail
repo=/workspace/scratch/fac5abfa4c9e/ponder-restored-20261026/work/ponder-go
cd "$repo" || exit 1
source ../../env.sh || exit 1
out=parity/runs/managed-finite-m44/full-02-20261026
baseline=parity/runs/finite-historical-m43/m44-regression-full-02-20261026
receipt="$out-exec"
[[ ! -e "$out" && ! -e "$baseline" && ! -e "$receipt" ]] || exit 2
mkdir "$receipt" || exit 2
cp "$0" "$receipt/launch.sh" || exit 2
namespace=$(readlink /proc/self/ns/pid)
started=$(date -u +%FT%TZ)
observer_status=not_run
collector_status=not_run
observer_pid=; observer_ticks=; collector_pid=; collector_ticks=; interrupted=
start_ticks() { python3 - "$1" <<'PYID'
import pathlib,sys
try:
 raw=pathlib.Path('/proc/'+sys.argv[1]+'/stat').read_text(); print(raw[raw.rfind(')')+2:].split()[19])
except FileNotFoundError: print('gone')
PYID
}
forward_signal() {
 local entry pid ticks
 interrupted="$1"
 for entry in "$observer_pid:$observer_ticks" "$collector_pid:$collector_ticks"; do
  pid=${entry%%:*}; ticks=${entry#*:}
  if [[ -n "$pid" && -n "$ticks" && "$ticks" != gone && "$(start_ticks "$pid")" == "$ticks" ]]; then
   kill -TERM "$pid" 2>/dev/null || true
  fi
 done
}
trap 'forward_signal INT' INT
trap 'forward_signal TERM' TERM
join_child() {
 local pid=$1 result
 while true; do
  wait "$pid"; result=$?
  kill -0 "$pid" 2>/dev/null || break
 done
 # Retrieve retained Bash child status after any interrupted wait as well.
 wait "$pid"; result=$?
 return "$result"
}
finish() {
 local shell_status=$?
 trap - EXIT
 python3 - "$receipt" "$out" "$baseline" "$namespace" "$started" "$observer_status" "$collector_status" "$shell_status" "$0" "$interrupted" <<'PY'
import sys,json,pathlib,hashlib,os,datetime
receipt,out,baseline,namespace,started,observer,collector,shell,script,interrupted=sys.argv[1:]
root=pathlib.Path.cwd()
def ref(p):
 p=pathlib.Path(p)
 return {'file':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} if p.is_file() else {'file':str(p),'missing':True}
now=os.readlink('/proc/self/ns/pid')
script_same=pathlib.Path(script).read_bytes()==pathlib.Path(receipt+'/launch.sh').read_bytes()
r={'format':'managed-finite-same-exec-sequence-v1','complete':True,'success':observer=='0' and collector=='0' and shell=='0' and not interrupted and now==namespace and script_same,'started_utc':started,'finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'pid_namespace_before':namespace,'pid_namespace_after':now,'observer_exit_code':observer,'collector_exit_code':collector,'shell_exit_code':int(shell),'interrupted':interrupted,'script_unchanged':script_same,'commands':[['python3','tools/managed-finite-observe.py','--output='+out,'--baseline='+baseline,'--budget-gib=4'],['node','tools/managed-finite-results.mjs','--run-dir='+out,'--output='+out+'/verification.json']],'references':[ref(p) for p in [receipt+'/launch.sh',receipt+'/observer.log',receipt+'/collector.log',out+'-observer/result.json',out+'/closeout.json',out+'/verification.json',baseline+'-fixture/exit-code.txt',baseline+'-fixture/shutdown.log']],'scope':'Observer and final collector ran sequentially inside this exact recorded execution PID namespace, not an unrelated later exec or a host-global process census.'}
with open(receipt+'/result.json','x') as f:json.dump(r,f,indent=2);f.write('\n')
print(json.dumps({'execution':receipt+'/result.json','success':r['success']}))
if not r['success']:sys.exit(1)
PY
 local audit_status=$?
 [[ "$shell_status" == 0 && "$audit_status" == 0 ]] || exit 1
 exit 0
}
trap finish EXIT
python3 - "$receipt/admission.json" "$namespace" "$started" "$out" "$baseline" <<'PY'
import json,sys
p,ns,started,out,baseline=sys.argv[1:]
with open(p,'x') as f:json.dump({'started_utc':started,'pid_namespace':ns,'run_dir':out,'baseline_run':baseline,'sequence':['observer','final_collector'],'budget_gib':4,'floor_gib':5},f,indent=2);f.write('\n')
PY
[[ $? == 0 ]] || exit 1
PYTHONDONTWRITEBYTECODE=1 python3 tools/managed-finite-observe.py --output="$out" --baseline="$baseline" --budget-gib=4 > "$receipt/observer.log" 2>&1 &
observer_pid=$!; observer_ticks=$(start_ticks "$observer_pid")
join_child "$observer_pid"; observer_status=$?; observer_pid=
if [[ "$observer_status" == 0 && -z "$interrupted" ]]; then
 node tools/managed-finite-results.mjs --run-dir="$out" --output="$out/verification.json" > "$receipt/collector.log" 2>&1 &
 collector_pid=$!; collector_ticks=$(start_ticks "$collector_pid")
 join_child "$collector_pid"; collector_status=$?; collector_pid=
fi
[[ "$observer_status" == 0 && "$collector_status" == 0 && -z "$interrupted" ]]
