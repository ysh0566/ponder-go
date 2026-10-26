#!/usr/bin/env python3
"""Observe only this launch's task descendants; no host-global drain claim.

The sampler is not a fork audit or cgroup. It cannot prove the absence of a
process that detached and lost its parent between samples without being seen.
Observed identities, their groups, and actual PostgreSQL shutdown are checked
separately. No unrelated process is signaled, stopped, or deleted.
"""
import argparse
import datetime
import hashlib
import json
import os
import pathlib
import shutil
import signal
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent

def utc():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()

def ref(path):
    path = pathlib.Path(path).resolve(strict=True)
    return {"file": os.path.relpath(path, ROOT), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}

def scoped(value, prefix):
    path = pathlib.Path(os.path.abspath(ROOT / value))
    parent = ROOT / prefix
    if path.parent != parent or path.name in ("", ".", "..") or path.exists():
        raise RuntimeError("A fresh direct-child scoped output is required: " + str(path))
    if parent.resolve() != parent:
        raise RuntimeError("Output scope must not be a symlink")
    return path

def census(known_pids=()):
    found = {}
    for proc in pathlib.Path("/proc").iterdir():
        if not proc.name.isdigit():
            continue
        try:
            raw = (proc / "stat").read_text()
            tail = raw[raw.rfind(")") + 2:].split()
            row = {"pid": int(proc.name), "ppid": int(tail[1]), "pgid": int(tail[2]),
                   "start_ticks": tail[19], "state": tail[0],
                   "comm": raw[raw.find("(") + 1:raw.rfind(")")]}
            found[(row["pid"], row["start_ticks"])] = row
        except (FileNotFoundError, ProcessLookupError):
            continue
        except PermissionError:
            if int(proc.name) in known_pids:
                raise RuntimeError("Observation denied for a previously owned task PID: " + proc.name)
            continue
    return found

def write_new(path, value):
    with open(path, "x") as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write("\n")

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    parser.add_argument("--baseline", required=True)
    parser.add_argument("--budget-gib", type=float, default=4)
    args = parser.parse_args()
    output = scoped(args.output, "parity/runs/managed-finite-m44")
    baseline = scoped(args.baseline, "parity/runs/finite-historical-m43")
    if not baseline.name.startswith("m44-regression-"):
        raise RuntimeError("Explicit M44 regression child required")
    evidence = pathlib.Path(str(output) + "-observer")
    if evidence.exists():
        raise RuntimeError("Never replace observer evidence")
    initial_free = shutil.disk_usage(ROOT).free
    floor, budget = 5 * 1024**3, int(args.budget_gib * 1024**3)
    if budget <= 0 or budget > 4 * 1024**3 or initial_free - budget < floor:
        raise RuntimeError("Proposed footprint budget does not fit above 5 GiB floor")
    evidence.mkdir(parents=True)
    command = ["node", "tools/managed-finite-launch.mjs", "--output=" + str(output), "--baseline=" + str(baseline)]
    report = {"format": "managed-finite-observed-descendants-v1", "complete": False, "success": False,
              "run_dir": os.path.relpath(output, ROOT), "started_utc": utc(), "command": command,
              "observer": ref(__file__), "sampling_interval_ms": 1000,
              "capacity": {"floor_bytes": floor, "budget_bytes": budget, "initial_free_bytes": initial_free},
              "scope": "Only actual sampled descendants and their observed identities/groups; not exhaustive fork auditing or host-global process closure."}
    write_new(evidence / "admission.json", report)
    interrupted = []
    for sig in (signal.SIGTERM, signal.SIGINT):
        signal.signal(sig, lambda number, _frame: interrupted.append(signal.Signals(number).name))
    observed, groups = {}, set()
    signaled, child = False, None
    timeline_path = evidence / "timeline.jsonl"
    try:
        with open(evidence / "launch.log", "xb") as log, open(timeline_path, "x") as timeline:
            child = subprocess.Popen(command, cwd=ROOT, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            rows = census({child.pid})
            identity = next((v for v in rows.values() if v["pid"] == child.pid), None)
            if identity is None or identity["pgid"] != child.pid:
                raise RuntimeError("Cannot establish exact launch process identity")
            report["root_identity"] = {k: identity[k] for k in ("pid", "pgid", "start_ticks")}
            observed[(child.pid, identity["start_ticks"])] = identity
            groups.add(identity["pgid"])
            write_new(evidence / "root-identity.json", {"recorded_utc": utc(), "root_identity": report["root_identity"], "command": command})
            remaining_since = None
            while True:
                rows = census({key[0] for key in observed})
                owned = {key: value for key, value in rows.items() if key in observed or value["pgid"] in groups}
                changed = True
                while changed:
                    changed = False
                    parents = {v["pid"] for v in owned.values()}
                    for key, value in rows.items():
                        if key not in owned and value["ppid"] in parents:
                            owned[key] = value
                            changed = True
                observed.update(owned)
                groups.update(v["pgid"] for v in owned.values())
                free = shutil.disk_usage(ROOT).free
                timeline.write(json.dumps({"utc": utc(), "free_bytes": free, "processes": list(owned.values())}, sort_keys=True) + "\n")
                timeline.flush()
                over_budget = initial_free - free > budget
                if free < floor or over_budget:
                    interrupted.append("capacity_floor_or_budget")
                if interrupted and not signaled and child.poll() is None:
                    # Popen owns this exact unreaped child; additionally verify its
                    # Linux start identity. The launcher handles its exact inner group.
                    if (child.pid, identity["start_ticks"]) not in rows:
                        raise RuntimeError("Refusing signal after launch identity changed")
                    child.send_signal(signal.SIGTERM)
                    signaled = True
                status = child.poll()
                if status is not None:
                    if not owned:
                        break
                    if remaining_since is None:
                        remaining_since = time.monotonic()
                    if time.monotonic() - remaining_since >= 30:
                        report["drain_error"] = "Observed task descendants remain after launch exit; no unrelated process was handled."
                        break
                time.sleep(1)
            status = child.wait()
            rows = census({key[0] for key in observed})
            identities = [v for key, v in rows.items() if key in observed]
            remaining_groups = [{"pgid": group, "processes": [v for v in rows.values() if v["pgid"] == group]} for group in sorted(groups)]
            remaining_groups = [group for group in remaining_groups if group["processes"]]
            report.update(exit_code=status if status >= 0 else None,
                          signal=None if status >= 0 else signal.Signals(-status).name,
                          observed=sorted(observed.values(), key=lambda v: (v["pid"], v["start_ticks"])),
                          final_remaining_identities=identities, final_remaining_groups=remaining_groups,
                          interrupted=interrupted)
            final_free = shutil.disk_usage(ROOT).free
            report["capacity"]["final_free_bytes"] = final_free
            if final_free < floor or initial_free - final_free > budget:
                interrupted.append("final_capacity_floor_or_budget")
            if status == 0 and not identities and not remaining_groups and not interrupted:
                report.update(wrapper=ref(output / "result.json"), closeout=ref(output / "closeout.json"),
                              launch=ref(pathlib.Path(str(output) + "-launch") / "launch.json"))
                report["success"] = True
    except BaseException as error:
        report["error"] = repr(error)
        if child is not None and child.poll() is None:
            # Retain and join the exact child even when observation fails.
            child.send_signal(signal.SIGTERM)
            child.wait()
    finally:
        report["complete"] = True
        report["finished_utc"] = utc()
        report["inputs"] = [ref(p) for p in (evidence / "admission.json", evidence / "root-identity.json", timeline_path, evidence / "launch.log") if p.exists()]
        write_new(evidence / "result.json", report)
        if output.is_dir():
            write_new(output / "outer-closeout.json", report)
        print(json.dumps({"observer": os.path.relpath(evidence / "result.json", ROOT), "success": report["success"]}))
    return 0 if report["success"] else 1

if __name__ == "__main__":
    raise SystemExit(main())
