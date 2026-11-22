#!/usr/bin/env python3
"""Restore historical evidence from a verified external bundle, without bloating .git."""

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tempfile


FORMAT = "ponder-parity-git-archive-v1"
MANIFEST = "parity/archive-manifest.json"
CATALOG = "parity/archive-files.json.gz"
ROOT = Path(__file__).resolve().parent.parent


def progress(stage, number, total):
    if number % 1000 == 0:
        print(f"{stage}: {number}/{total}", file=sys.stderr, flush=True)


def git(root, *args):
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True)
    if result.returncode:
        raise ValueError(result.stderr.decode(errors="replace").strip())
    return result.stdout


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def archive_repository(root, bundle=None):
    """Open legacy objects separately; never fetch them into the working repo."""
    manifest = json.loads((root / MANIFEST).read_text())
    external = manifest.get("external_store")
    if external is None:
        return root  # Compatibility with original snapshots and their tests.
    if external.get("kind") != "git_bundle" or not re.fullmatch(r"[0-9a-f]{64}", external["sha256"]):
        raise ValueError("Unsupported external archive store")
    cache = root / ".local/parity-archive" / ("objects-" + external["sha256"][:16] + ".git")
    if cache.resolve() != cache.absolute():
        raise ValueError("External archive cache must not contain symlinks")
    marker = {key: external[key] for key in ("sha256", "bytes", "bundle_ref")}
    marker["source_commit"] = manifest["source_commit"]
    supplied = bundle or os.environ.get("PONDER_PARITY_BUNDLE")
    default = PurePosixPath(external["default_file"])
    if default.is_absolute() or ".." in default.parts or default.parts[:1] != (".local",):
        raise ValueError("Default archive bundle must be an ignored .local path")
    path = Path(supplied).absolute() if supplied else root / default
    # An explicitly supplied bundle is always checked, even when a cache exists.
    if supplied:
        if not path.is_file() or path.stat().st_size != external["bytes"] or digest(path) != external["sha256"]:
            raise ValueError("External archive bundle SHA-256/size differs")
    if cache.exists():
        if not (cache / "ponder-parity-source.json").is_file() or json.loads((cache / "ponder-parity-source.json").read_text()) != marker:
            raise ValueError("External archive cache identity differs; refusing reuse")
        git(cache, "cat-file", "-e", manifest["source_commit"] + "^{commit}")
        return cache
    if not path.is_file():
        raise ValueError("Historical evidence is outside this repository. Supply --bundle /path/to/original.bundle or PONDER_PARITY_BUNDLE. Ordinary clones intentionally omit the old objects.")
    if not supplied and (path.stat().st_size != external["bytes"] or digest(path) != external["sha256"]):
        raise ValueError("External archive bundle SHA-256/size differs")
    if not re.fullmatch(r"(?:HEAD|refs/[A-Za-z0-9._/-]+)", external["bundle_ref"]):
        raise ValueError("Invalid external archive ref")
    git(root, "bundle", "verify", str(path))
    cache.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix="import-", dir=cache.parent))
    try:
        git(temporary, "init", "--bare")
        git(temporary, "-c", "gc.auto=0", "-c", "maintenance.auto=false", "fetch", "--no-tags", str(path), external["bundle_ref"] + ":refs/archive/source")
        if git(temporary, "rev-parse", manifest["source_commit"] + "^{tree}").decode().strip() != manifest["source_tree"]:
            raise ValueError("External bundle source tree differs")
        (temporary / "ponder-parity-source.json").write_text(json.dumps(marker, sort_keys=True) + "\n")
        try:
            temporary.rename(cache)
        except FileExistsError:
            if json.loads((cache / "ponder-parity-source.json").read_text()) != marker:
                raise ValueError("Concurrent archive cache identity differs")
        return cache
    finally:
        if temporary.exists():
            shutil.rmtree(temporary)


def safe_path(root, name):
    relative = PurePosixPath(name)
    if (relative.is_absolute() or ".." in relative.parts or
            relative.as_posix() != name or "\x00" in name):
        raise ValueError("Noncanonical archive path: " + name)
    if relative.parts[:2] != ("parity", "runs") or len(relative.parts) < 3:
        raise ValueError("Archive path must be inside parity/runs/: " + name)
    target = root
    for part in relative.parts:
        target = target / part
        if target.is_symlink():
            raise ValueError("Symlink in archive path: " + str(target))
    return target


def tree_entries(root, source):
    entries = []
    for record in git(root, "ls-tree", "-r", "-z", "-l", source, "--", "parity/runs").split(b"\0"):
        if not record:
            continue
        meta, name = record.split(b"\t", 1)
        mode, kind, oid, size = meta.decode().split()
        name = name.decode()
        safe_path(root, name)
        if kind != "blob" or mode not in ("100644", "100755"):
            raise ValueError("Only regular evidence files are supported: " + name)
        if not size.isdecimal():
            raise ValueError("Missing/different Git object size for " + name + "; use a full clone or import the backup bundle")
        entries.append({"path": name, "git_blob": oid, "bytes": int(size), "mode": mode})
    return entries


def keep_reason(name):
    parts = PurePosixPath(name).parts
    base = parts[-1]
    if len(parts) == 3 and base.endswith(".json"):
        return "top-level verification, publication, or recovery record"
    if parts[2].startswith("batch-") and parts[2].endswith("-evidence"):
        return None
    if any(part in ("failure-inputs", "native-inputs", "summary-failure-inputs", "prepared-inputs") for part in parts):
        return None
    if name == "parity/runs/sync-store-m32/type-compile.json":
        return "direct native test dependency"
    if base in ("result.json", "summary.json"):
        return "historical run result; its referenced payloads are archived"
    if "publication" in base and base.endswith(".json"):
        return "publication record"
    return None


class Blobs:
    """One Git process, bounded memory, and explicit failure on missing objects."""

    def __init__(self, root):
        self.process = subprocess.Popen(
            ["git", "-C", str(root), "cat-file", "--batch"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        )

    def copy(self, entry, output=None):
        p = self.process
        p.stdin.write((entry["git_blob"] + "\n").encode())
        p.stdin.flush()
        header = p.stdout.readline().decode().strip().split()
        if header != [entry["git_blob"], "blob", str(entry["bytes"])]:
            raise ValueError("Missing/different Git object for " + entry["path"] +
                             "; use a full clone or import the documented backup bundle")
        remaining = entry["bytes"]
        sha = hashlib.sha256()
        while remaining:
            block = p.stdout.read(min(1024 * 1024, remaining))
            if not block:
                raise ValueError("Truncated Git object: " + entry["path"])
            remaining -= len(block)
            sha.update(block)
            if output is not None:
                output.write(block)
        if p.stdout.read(1) != b"\n":
            raise ValueError("Invalid Git object framing")
        if sha.hexdigest() != entry["sha256"]:
            raise ValueError("Git object SHA-256 differs: " + entry["path"])

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.process.stdin.close()
        self.process.stdout.close()
        if self.process.wait() != 0 and sys.exc_info()[0] is None:
            raise ValueError("git cat-file failed")


def snapshot(root, source="HEAD"):
    for name in (MANIFEST, CATALOG):
        if (root / name).exists():
            raise ValueError("Archive index already exists: " + name)
    source = git(root, "rev-parse", "--verify", source + "^{commit}").decode().strip()
    git(root, "diff", "--quiet", source, "--", "parity/runs")
    entries = tree_entries(root, source)
    if not entries:
        raise ValueError("No historical evidence to archive")
    hashes = {}
    kept, archived = [], []
    for entry in entries:
        path = safe_path(root, entry["path"])
        if entry["git_blob"] not in hashes:
            # Read each distinct payload once; Git diff above guards local edits.
            content = path.read_bytes()
            blob = hashlib.sha1(b"blob " + str(len(content)).encode() + b"\0" + content).hexdigest()
            if len(content) != entry["bytes"] or blob != entry["git_blob"]:
                raise ValueError("Worktree differs from archived commit: " + entry["path"])
            hashes[entry["git_blob"]] = hashlib.sha256(content).hexdigest()
        entry["sha256"] = hashes[entry["git_blob"]]
        reason = keep_reason(entry["path"])
        if reason:
            kept.append({**entry, "reason": reason})
        else:
            archived.append(entry)
    catalog = {"format": FORMAT, "source_commit": source, "files": archived}
    payload = (json.dumps(catalog, separators=(",", ":"), ensure_ascii=False) + "\n").encode()
    with (root / CATALOG).open("xb") as out:
        with gzip.GzipFile(fileobj=out, filename="", mode="wb", mtime=0) as zipped:
            zipped.write(payload)
    counts = lambda items: {"files": len(items), "bytes": sum(x["bytes"] for x in items)}
    manifest = {
        "format": FORMAT,
        "source_repository": "https://github.com/ysh0566/ponder-go",
        "source_commit": source,
        "source_tree": git(root, "rev-parse", source + "^{tree}").decode().strip(),
        "storage": "Original Git blobs remain reachable through unchanged ancestor history. No network access or new service is required.",
        "catalog": {"path": CATALOG, "sha256": digest(root / CATALOG)},
        "original": counts(entries), "retained": counts(kept), "archived": counts(archived),
        "retained_files": kept,
        "policy": "Preserve every original byte and status. Indexing/archiving provides no new test or parity credit. Future raw runs are ignored; promote reviewed summaries explicitly.",
    }
    with (root / MANIFEST).open("x") as out:
        json.dump(manifest, out, indent=2, ensure_ascii=False)
        out.write("\n")
    return {key: manifest[key] for key in ("source_commit", "original", "retained", "archived")}


def load_archive(root, repository=None):
    manifest = json.loads((root / MANIFEST).read_text())
    if manifest["format"] != FORMAT or manifest["catalog"]["path"] != CATALOG:
        raise ValueError("Unsupported archive manifest")
    if digest(root / CATALOG) != manifest["catalog"]["sha256"]:
        raise ValueError("Archive catalog SHA-256 differs")
    with gzip.open(root / CATALOG, "rt") as stream:
        catalog = json.load(stream)
    if catalog["format"] != FORMAT or catalog["source_commit"] != manifest["source_commit"]:
        raise ValueError("Archive source differs")
    if not re.fullmatch(r"[0-9a-f]{40}", manifest["source_commit"]):
        raise ValueError("Invalid source commit")
    repository = repository if repository is not None else archive_repository(root)
    try:
        source_tree = git(repository, "rev-parse", manifest["source_commit"] + "^{tree}").decode().strip()
    except ValueError as error:
        raise ValueError("Archive source commit is unavailable; supply the external bundle documented in parity/README.md") from error
    if source_tree != manifest["source_tree"]:
        raise ValueError("Archive source tree differs")
    source = {x["path"]: x for x in tree_entries(repository, manifest["source_commit"])}
    seen = set()
    blob_hashes = {}
    for entries, key in ((catalog["files"], "archived"), (manifest["retained_files"], "retained")):
        if manifest[key] != {"files": len(entries), "bytes": sum(x["bytes"] for x in entries)}:
            raise ValueError("Archive counts differ")
        for entry in entries:
            name = entry["path"]
            safe_path(root, name)
            if name in seen or not re.fullmatch(r"[0-9a-f]{64}", entry["sha256"]):
                raise ValueError("Duplicate path or invalid SHA-256: " + name)
            seen.add(name)
            if source.get(name) != {k: entry[k] for k in ("path", "git_blob", "bytes", "mode")}:
                raise ValueError("Catalog differs from original Git tree: " + name)
            if blob_hashes.setdefault(entry["git_blob"], entry["sha256"]) != entry["sha256"]:
                raise ValueError("Inconsistent SHA-256 for duplicated Git blob: " + name)
    if seen != set(source) or manifest["original"] != {"files": len(source), "bytes": sum(x["bytes"] for x in source.values())}:
        raise ValueError("Archive does not cover the complete original tree")
    return manifest, catalog["files"]


def select(entries, prefixes):
    if not prefixes:
        return entries
    chosen = []
    for prefix in prefixes:
        prefix = prefix.rstrip("/")
        if prefix == "parity/runs":
            found = entries
        else:
            # Prefixes use directory boundaries, never ambiguous string starts.
            safe_path(Path("/"), prefix)
            found = [x for x in entries if x["path"] == prefix or x["path"].startswith(prefix + "/")]
        if not found:
            raise ValueError("No archived files match: " + prefix)
        chosen.extend(found)
    return list({x["path"]: x for x in chosen}.values())


def verify(root, prefixes=(), bundle=None, repository=None):
    repository = repository if repository is not None else archive_repository(root, bundle)
    manifest, entries = load_archive(root, repository)
    selected = select(entries, prefixes)
    unique = {x["git_blob"]: x for x in selected}
    with Blobs(repository) as blobs:
        for number, entry in enumerate(unique.values(), 1):
            blobs.copy(entry)
            progress("verify blobs", number, len(unique))
    return {"verified": True, "files": len(selected), "unique_blobs": len(unique),
            "bytes": sum(x["bytes"] for x in selected), "source_commit": manifest["source_commit"]}


def stat_identity(info):
    # Reading can update atime; only content/identity changes invalidate a check.
    return (info.st_dev, info.st_ino, info.st_mode, info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def unchanged(path, entry):
    before = path.stat() if path.is_file() else None
    if not path.is_file() or path.stat().st_size != entry["bytes"] or digest(path) != entry["sha256"]:
        raise ValueError("Refusing to change different existing file: " + str(path))
    after = path.stat()
    if stat_identity(before) != stat_identity(after):
        raise ValueError("Evidence changed during verification: " + str(path))
    if bool(after.st_mode & 0o111) != (entry["mode"] == "100755"):
        raise ValueError("Existing executable mode differs: " + str(path))
    return stat_identity(after)


def restore(root, prefixes=(), destination=None, bundle=None, repository=None):
    repository = repository if repository is not None else archive_repository(root, bundle)
    _, entries = load_archive(root, repository)
    entries = select(entries, prefixes)
    destination = Path(destination).absolute() if destination is not None else root
    if destination.resolve() != destination:
        raise ValueError("Destination must not contain symlinks")
    # Check the entire selection before creating any files.
    for entry in entries:
        target = safe_path(destination, entry["path"])
        if target.exists():
            unchanged(target, entry)
    restored = existing = 0
    with Blobs(repository) as blobs:
        for number, entry in enumerate(entries, 1):
            target = safe_path(destination, entry["path"])
            if target.exists():
                unchanged(target, entry)
                existing += 1
                progress("restore files", number, len(entries))
                continue
            target.parent.mkdir(parents=True, exist_ok=True)
            temporary = None
            try:
                with tempfile.NamedTemporaryFile(dir=target.parent, prefix=".parity-restore-", delete=False) as out:
                    temporary = Path(out.name)
                    blobs.copy(entry, out)
                    out.flush()
                    os.fsync(out.fileno())
                temporary.chmod(0o755 if entry["mode"] == "100755" else 0o644)
                # Exclusive link: never overwrite an existing file or symlink.
                os.link(temporary, target)
                restored += 1
                progress("restore files", number, len(entries))
            finally:
                if temporary is not None:
                    temporary.unlink(missing_ok=True)
    return {"restored": restored, "already_present": existing, "destination": str(destination)}


def prune(root, bundle=None):
    repository = archive_repository(root, bundle)
    _, entries = load_archive(root, repository)
    # All source blobs must be recoverable before removing any working copy.
    verify(root, repository=repository)
    existing = []
    for number, entry in enumerate(entries, 1):
        target = safe_path(root, entry["path"])
        if target.exists():
            existing.append((target, unchanged(target, entry)))
        progress("check files before pruning", number, len(entries))
    for target, checked_stat in existing:
        safe_path(root, target.relative_to(root).as_posix())
        if target.is_symlink() or stat_identity(target.stat()) != checked_stat:
            raise ValueError("Evidence changed since verification: " + str(target))
        target.unlink()
    # Only remove empty historical run directories, never the runs root itself.
    for directory, _, _ in os.walk(root / "parity/runs", topdown=False, followlinks=False):
        path = Path(directory)
        if path != root / "parity/runs" and not path.is_symlink():
            try:
                path.rmdir()
            except OSError:
                pass
    return {"removed_worktree_files": len(existing), "archive_objects_preserved": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("snapshot", "verify", "restore", "prune"))
    parser.add_argument("--prefix", action="append", default=[], help="Exact archived file or directory; repeatable")
    parser.add_argument("--destination", type=Path, help="Restore into a separate directory instead of this checkout")
    parser.add_argument("--bundle", type=Path, help="External original.bundle; imported only into ignored .local storage")
    args = parser.parse_args()
    if args.command not in ("restore", "verify") and args.prefix:
        parser.error("--prefix applies only to verify/restore")
    if args.command != "restore" and args.destination:
        parser.error("--destination applies only to restore")
    if args.command == "snapshot" and args.bundle:
        parser.error("--bundle does not apply to snapshot")
    try:
        if args.command == "snapshot":
            result = snapshot(ROOT)
        elif args.command == "verify":
            result = verify(ROOT, args.prefix, bundle=args.bundle)
        elif args.command == "restore":
            result = restore(ROOT, args.prefix, args.destination, bundle=args.bundle)
        else:
            result = prune(ROOT, bundle=args.bundle)
        print(json.dumps(result, indent=2))
    except (ValueError, KeyError, OSError, json.JSONDecodeError) as error:
        parser.exit(1, str(error) + "\n")


if __name__ == "__main__":
    main()
