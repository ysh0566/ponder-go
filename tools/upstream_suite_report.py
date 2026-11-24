#!/usr/bin/env python3
"""Reconcile original Vitest reports with the pinned upstream test inventory."""
import argparse
from collections import Counter, defaultdict
import hashlib
import json
from pathlib import Path, PurePosixPath
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]
STATUS_FIELDS = {
    "passed": "numPassedTests", "failed": "numFailedTests",
    "skipped": "numPendingTests", "todo": "numTodoTests",
}


def check(condition, message):
    if not condition:
        raise ValueError(message)


def sha(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def file_name(name, known):
    parts = PurePosixPath(name).parts
    check(".." not in parts, f"unexpected test path: {name}")
    matches = ["/".join(parts[i:]) for i, part in enumerate(parts) if part == "packages"]
    matched = [p for p in matches if p in known]
    check(len(matched) == 1, f"unrecognized test file: {name}")
    return matched[0]


def inventory(ledger, upstream):
    revision = subprocess.check_output(["git", "-C", upstream, "rev-parse", "HEAD"], text=True).strip()
    check(revision == ledger["baseline"]["revision"], "upstream checkout does not match the ledger")
    files = {f["path"]: f for f in ledger["files"]}
    for name, entry in files.items():
        check(sha(upstream / name) == entry["sha256"], f"upstream source changed: {name}")
    groups = defaultdict(list)
    for case in ledger["tests"]:
        name = case["source_file"]
        check(case["source_sha256"] == files[name]["sha256"], f"conflicting source digest: {name}")
        groups[name, case["full_name"], case["source_state"]].append(case["id"])
    counts = Counter(c["source_file"] for c in ledger["tests"])
    check(counts == {name: entry["expanded_records"] for name, entry in files.items()}, "ledger counts disagree")
    check(len(set(c["id"] for c in ledger["tests"])) == len(ledger["tests"]), "duplicate ledger ID")
    return files, groups


def read_report(path, known):
    doc = json.loads(path.read_bytes())
    files = set()
    outcomes = defaultdict(Counter)
    totals = Counter()
    for suite in doc["testResults"]:
        name = file_name(suite["name"], known)
        check(name not in files, f"duplicate file in {path.name}: {name}")
        files.add(name)
        has_failure = False
        for test in suite["assertionResults"]:
            status = test["status"]
            check(status in STATUS_FIELDS, f"unrecognized assertion status: {status}")
            ancestors = test["ancestorTitles"]
            if name.endswith(".test-d.ts") and ancestors[:1] == [name.split("/", 2)[2]]:
                ancestors = ancestors[1:]
            title = " > ".join([*ancestors, test["title"]])
            source_state = {"skipped": "skip", "todo": "todo"}.get(status, "active")
            outcomes[name, title, source_state][status] += 1
            totals[status] += 1
            has_failure |= status == "failed"
        check(suite["status"] == ("failed" if has_failure else "passed"),
              f"file status cannot be explained by its assertions: {name}")
    check(files, f"empty report: {path}")
    check(all(totals[s] == doc[field] for s, field in STATUS_FIELDS.items())
          and totals.total() == doc["numTotalTests"], f"assertion totals disagree: {path}")
    check(len(files) <= doc["numTotalTestSuites"] == sum(doc[k] for k in
          ("numPassedTestSuites", "numFailedTestSuites", "numPendingTestSuites")), f"suite totals disagree: {path}")
    success = totals["failed"] == 0
    exit_code = int(path.with_suffix(".exit").read_text().strip())
    check(doc["success"] is success and (exit_code == 0) == success and
          bool(doc["numFailedTestSuites"]) == (not success), f"inconsistent Vitest/process result: {path}")
    result = {"report": path.name, "sha256": sha(path), "files": len(files),
              "outcomes": dict(sorted(totals.items())), "exit": exit_code}
    return files, outcomes, result


def audit_scope(files, outcomes, expected_files, groups):
    check(files == expected_files, f"unexpected or missing files: {sorted(files ^ expected_files)}")
    expected = Counter({key: len(ids) for key, ids in groups.items() if key[0] in expected_files})
    observed = Counter({key: counts.total() for key, counts in outcomes.items()})
    check(expected == observed, f"declarations differ: missing {list((expected-observed).items())[:3]}, "
          f"unexpected {list((observed-expected).items())[:3]}")


def combine(primary, companion, groups):
    totals = Counter()
    unresolved = []
    differences = []
    for key, counts in sorted(primary.items()):
        ids = groups[key]
        details = {"file": key[0], "name": key[1], "source_ids": ids}
        if key in companion:
            other = companion[key]
            if counts != other:
                differences.append({**details, "pglite": dict(counts), "postgres": dict(other)})
            if counts["failed"] and other["failed"] and max(counts["failed"], other["failed"]) < len(ids):
                # Vitest provides no identity for duplicate runtime titles on these two backends.
                details.update(pglite=dict(counts), postgres=dict(other))
                counts = Counter(ambiguous=len(ids))
            elif other["failed"] > counts["failed"]:
                counts = other
        totals.update(counts)
        if counts["failed"] or counts["ambiguous"]:
            unresolved.append({**details, "outcomes": dict(sorted(counts.items()))})
    return dict(sorted(totals.items())), unresolved, differences


def reconcile(upstream, pglite, postgres, others):
    ledger = json.loads((ROOT / "parity/test-ledger.json").read_bytes())
    known, groups = inventory(ledger, upstream)
    backend = {name for name in known if name.startswith("packages/core/") and name.endswith(".test.ts")}
    runs = []
    primary_files, primary, run = read_report(pglite, known)
    audit_scope(primary_files, primary, backend, groups)
    runs.append({"partition": "core_pglite", **run})
    companion_files, companion, run = read_report(postgres, known)
    audit_scope(companion_files, companion, backend, groups)
    check(runs[0]["sha256"] != run["sha256"], "same report used for both backends")
    runs.append({"partition": "core_postgres", **run})
    for path in others:
        files, outcomes, run = read_report(path, known)
        check(primary_files.isdisjoint(files), f"overlapping report: {path}")
        primary_files.update(files)
        primary.update(outcomes)
        runs.append({"partition": "other", **run})
    audit_scope(primary_files, primary, set(known), groups)
    counts, unresolved, differences = combine(primary, companion, groups)
    return {"upstream": ledger["baseline"]["revision"], "files": len(known), "declarations": len(ledger["tests"]),
            "unique_outcomes": counts, "runs": runs, "backend_differences": differences,
            "unresolved_active_groups": unresolved,
            "native_port_ledger": {k: ledger["summary"][k] for k in ("ported", "unimplemented")}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream", required=True, type=Path)
    parser.add_argument("--pglite", required=True, type=Path)
    parser.add_argument("--postgres", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("reports", type=Path, nargs="+")
    args = parser.parse_args()
    try:
        result = reconcile(args.upstream, args.pglite, args.postgres, args.reports)
        with args.output.open("x") as out:
            out.write(json.dumps(result, indent=2) + "\n")
    except (ValueError, KeyError, TypeError, OSError, subprocess.CalledProcessError) as exc:
        parser.exit(2, f"{parser.prog}: {exc}\n")
    print(json.dumps(result["unique_outcomes"], sort_keys=True))
    return int(bool(result["unresolved_active_groups"]))


if __name__ == "__main__":
    sys.exit(main())
