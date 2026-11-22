#!/usr/bin/env python3
"""Prepare and audit a filtered mirror; never mutate the source repository."""

import argparse
import json
from pathlib import Path
import subprocess


def git(repo, *args, data=None):
    result = subprocess.run(["git", "-C", str(repo), *args], input=data, capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stderr.decode(errors="replace"))
    return result.stdout


def leaves(repo, tree):
    result = {}
    for item in git(repo, "ls-tree", "-r", "-z", tree).split(b"\0"):
        if item:
            meta, name = item.split(b"\t", 1)
            result[name] = meta
    return result


class Filter:
    def __init__(self, repo, retained):
        self.repo, self.memo = repo, {}
        self.trie = {}
        for path in retained:
            node = self.trie
            for part in path.removeprefix("parity/runs/").split("/"):
                node = node.setdefault(part, {})

    def entries(self, oid):
        result = []
        for item in git(self.repo, "ls-tree", "-z", oid).split(b"\0"):
            if item:
                meta, name = item.split(b"\t", 1)
                mode, kind, child = meta.decode().split()
                result.append((mode, kind, child, name))
        return result

    def write(self, entries):
        data = b"".join(f"{mode} {kind} {oid}\t".encode() + name + b"\0" for mode, kind, oid, name in entries)
        return git(self.repo, "mktree", "-z", data=data).decode().strip()

    def allowed(self, oid, prefix, node):
        key = (oid, prefix)
        if key in self.memo:
            return self.memo[key]
        result = []
        for mode, kind, child, raw_name in self.entries(oid):
            name = raw_name.decode()
            if name not in node:
                continue
            if kind == "tree":
                child = self.allowed(child, prefix + "/" + name, node[name])
                if child is None:
                    continue
            elif node[name]:
                # A directory in the retained tree must not retain an old file
                # that happened to occupy the same path in an earlier commit.
                continue
            result.append((mode, kind, child, raw_name))
        self.memo[key] = self.write(result) if result else None
        return self.memo[key]

    def root(self, oid):
        key = (oid, "root")
        if key in self.memo:
            return self.memo[key]
        root_entries = self.entries(oid)
        output = []
        for mode, kind, child, name in root_entries:
            if name == b"parity" and kind == "tree":
                parity = []
                for pmode, pkind, pchild, pname in self.entries(child):
                    if pname == b"runs":
                        if pkind != "tree":
                            raise ValueError("Historical parity/runs is not a tree")
                        pchild = self.allowed(pchild, "parity/runs", self.trie)
                        if pchild is None:
                            continue
                    parity.append((pmode, pkind, pchild, pname))
                child = self.write(parity)
            output.append((mode, kind, child, name))
        result = self.write(output)
        self.memo[key] = result
        return result


def rewrite(source, mirror, output):
    if mirror.exists():
        raise ValueError("Destination already exists")
    source = source.resolve()
    git(source, "clone", "--mirror", "--shared", str(source), str(mirror))
    retained = {p.decode() for p in git(source, "ls-tree", "-r", "-z", "--name-only", "HEAD", "--", "parity/runs").split(b"\0") if p}
    refs = []
    for line in git(mirror, "for-each-ref", "--format=%(refname) %(objectname) %(symref)").decode().splitlines():
        parts = line.split()
        refs.append({"name": parts[0], "old": parts[1], "symbolic": parts[2] if len(parts) > 2 else None})
    commits = git(mirror, "rev-list", "--reverse", "--topo-order", "--all").decode().splitlines()
    transform = Filter(mirror, retained)
    mapping, audit = {}, []
    for number, old in enumerate(commits, 1):
        raw = git(mirror, "cat-file", "commit", old)
        header, message = raw.split(b"\n\n", 1)
        blocks = []
        for line in header.splitlines():
            if line.startswith(b" "):
                blocks[-1].append(line)
            else:
                blocks.append([line])
        rewritten, old_tree, new_tree, stripped = [], None, None, []
        for block in blocks:
            first = block[0]
            if first.startswith(b"tree "):
                old_tree = first[5:].decode()
                new_tree = transform.root(old_tree)
                rewritten.append(b"tree " + new_tree.encode())
            elif first.startswith(b"parent "):
                rewritten.append(b"parent " + mapping[first[7:].decode()].encode())
            elif first.startswith((b"gpgsig ", b"mergetag ")):
                stripped.append(first.split(b" ", 1)[0].decode())
            else:
                rewritten.extend(block)
        new = git(mirror, "hash-object", "-w", "-t", "commit", "--stdin", data=b"\n".join(rewritten) + b"\n\n" + message).decode().strip()
        mapping[old] = new
        original_leaves = leaves(mirror, old_tree)
        filtered_leaves = leaves(mirror, new_tree)
        expected = {p: meta for p, meta in original_leaves.items() if not p.startswith(b"parity/runs/") or p.decode() in retained}
        if filtered_leaves != expected:
            raise ValueError("Filtered tree changed non-archive bytes/modes: " + old)
        audit.append({"old": old, "new": new, "old_tree": old_tree, "new_tree": new_tree,
                      "removed_files": len(original_leaves) - len(filtered_leaves),
                      "all_retained_blobs_and_modes_identical": True, "message_author_committer_unchanged": True,
                      "invalidated_signature_headers_removed": stripped})
        print(f"audited commits: {number}/{len(commits)}", flush=True)
    commands = ["start", "option no-deref"]
    for ref in refs:
        if ref["symbolic"]:
            continue
        ref["type"] = git(mirror, "cat-file", "-t", ref["old"]).decode().strip()
        if ref["type"] == "commit":
            ref["new"] = mapping[ref["old"]]
        elif ref["type"] == "tree":
            # Codex turn-diff snapshots can point directly to root trees.
            # Filtering commits alone would leave their payloads reachable.
            ref["new"] = transform.root(ref["old"])
            original_leaves = leaves(mirror, ref["old"])
            expected = {p: meta for p, meta in original_leaves.items() if not p.startswith(b"parity/runs/") or p.decode() in retained}
            if leaves(mirror, ref["new"]) != expected:
                raise ValueError("Snapshot ref changed non-archive content: " + ref["name"])
            ref["all_retained_blobs_and_modes_identical"] = True
        else:
            raise ValueError("Ref type requires explicit migration: " + ref["name"] + " (" + ref["type"] + ")")
        commands.append(f"update {ref['name']} {ref['new']} {ref['old']}")
    commands.extend(["prepare", "commit"])
    git(mirror, "update-ref", "--stdin", data=("\n".join(commands) + "\n").encode())
    git(mirror, "reflog", "expire", "--expire=all", "--all")
    git(mirror, "repack", "-a", "-d")
    git(mirror, "prune-packed")
    # All reachable filtered objects now have an independent local pack. Remove
    # access to the original object store before validating the result.
    alternates = mirror / "objects/info/alternates"
    alternates.unlink()
    git(mirror, "fsck", "--full", "--no-reflogs")
    original_only = set(mapping) - set(mapping.values())
    reachable = set(git(mirror, "rev-list", "--all").decode().splitlines())
    if original_only & reachable:
        raise ValueError("Old history is still reachable")
    report = {"source": str(source), "mirror": str(mirror), "retained_run_paths": sorted(retained),
              "commits": audit, "refs": refs, "all_retained_blobs_and_modes_identical": True,
              "shared_object_store_removed": True, "fsck_passed": True,
              "count_objects": git(mirror, "count-objects", "-vH").decode()}
    output.write_text(json.dumps(report, indent=2) + "\n")
    (output.parent / "commit-map.tsv").write_text("old\tnew\n" + "".join(f"{old}\t{new}\n" for old, new in mapping.items()))
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=Path.cwd())
    parser.add_argument("--mirror", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    rewrite(args.source, args.mirror.absolute(), args.report.absolute())


if __name__ == "__main__":
    main()
