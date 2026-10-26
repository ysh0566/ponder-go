#!/usr/bin/env python3
"""Print a portable Go overlay; never extract or modify active Go files."""
import argparse, hashlib, json
from pathlib import Path
p = argparse.ArgumentParser()
p.add_argument("--repo-root", required=True, type=Path)
a = p.parse_args()
root = a.repo_root.resolve()
base = Path(__file__).resolve().parent
if not (root / "go.mod").is_file():
    p.error("repo-root must contain go.mod")
replace = {}
for item in json.loads((base / "inputs.json").read_text()):
    src = (base / item["input"]).resolve()
    target = (root / item["target"]).resolve()
    if not src.is_relative_to(base / "inputs") or not target.is_relative_to(root):
        raise ValueError("path outside allowed roots")
    if hashlib.sha256(src.read_bytes()).hexdigest() != item["sha256"]:
        raise ValueError("input hash mismatch: " + item["input"])
    replace[str(target)] = str(src)
print(json.dumps({"Replace": replace}, indent=2))
