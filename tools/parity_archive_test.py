#!/usr/bin/env python3
"""Exercise lossless recovery and refusal to remove/overwrite changed evidence."""

import gzip
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import parity_archive as archive


class ArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="ponder-parity-archive-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        archive.git(self.root, "init", "-b", "main")
        archive.git(self.root, "config", "user.name", "Archive Test")
        archive.git(self.root, "config", "user.email", "archive-test@example.invalid")
        self.files = {
            "parity/test-ledger.json": b'{"ported":0,"failed":1}\n',
            "parity/runs/summary.json": b'{"passed":false}\n',
            "parity/runs/author-repair.json": b'{"map":["old","new"]}\n',
            "parity/runs/batch-1-evidence/result.json": b'{"failed":true}\n',
            "parity/runs/run-1/result.json": b'{"failed":true}\n',
            "parity/runs/run-1/output.bin": b"\x00\xff\x00binary\n",
            "parity/runs/run-1/duplicate.bin": b"\x00\xff\x00binary\n",
            "parity/runs/run-1/repro.sh": b"#!/bin/sh\nexit 1\n",
            "parity/runs/run-10/output.bin": b"other run\n",
        }
        for name, data in self.files.items():
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
            if name.endswith(".sh"):
                target.chmod(0o755)
        archive.git(self.root, "add", ".")
        archive.git(self.root, "commit", "-m", "original evidence including failure")
        self.source = archive.git(self.root, "rev-parse", "HEAD").decode().strip()
        archive.snapshot(self.root)

    def test_roundtrip_and_idempotency_preserve_status_bytes_and_modes(self):
        before = archive.verify(self.root)
        self.assertEqual(before["files"], 5)
        self.assertEqual(before["unique_blobs"], 4)
        archive.prune(self.root)
        self.assertEqual((self.root / "parity/runs/run-1/result.json").read_bytes(), b'{"failed":true}\n')
        self.assertEqual((self.root / "parity/test-ledger.json").read_bytes(), self.files["parity/test-ledger.json"])
        result = archive.restore(self.root)
        self.assertEqual(result["restored"], 5)
        self.assertEqual(archive.restore(self.root)["already_present"], 5)
        for name, data in self.files.items():
            self.assertEqual((self.root / name).read_bytes(), data)
        self.assertTrue((self.root / "parity/runs/run-1/repro.sh").stat().st_mode & 0o111)
        self.assertEqual(archive.verify(self.root), before)

    def test_restore_prefix_uses_directory_boundary(self):
        archive.prune(self.root)
        result = archive.restore(self.root, ["parity/runs/run-1"])
        self.assertEqual(result["restored"], 3)
        self.assertFalse((self.root / "parity/runs/run-10/output.bin").exists())
        with self.assertRaisesRegex(ValueError, "No archived files"):
            archive.restore(self.root, ["parity/runs/unknown"])

    def test_prune_refuses_all_mutations_when_one_file_is_changed(self):
        target = self.root / "parity/runs/run-10/output.bin"
        target.write_bytes(b"new evidence: must survive")
        with self.assertRaisesRegex(ValueError, "different existing file"):
            archive.prune(self.root)
        for name in self.files:
            self.assertTrue((self.root / name).is_file())
        self.assertEqual(target.read_bytes(), b"new evidence: must survive")

    def test_prune_refuses_missing_backup_object(self):
        _, entries = archive.load_archive(self.root)
        oid = entries[0]["git_blob"]
        (self.root / ".git/objects" / oid[:2] / oid[2:]).unlink()
        with self.assertRaisesRegex(ValueError, "Missing/different Git object"):
            archive.prune(self.root)
        for name in self.files:
            self.assertTrue((self.root / name).is_file())

    def test_prune_detects_edit_after_initial_content_check(self):
        first = self.root / "parity/runs/batch-1-evidence/result.json"
        check = archive.unchanged
        def edit_during_last_check(path, entry):
            result = check(path, entry)
            if entry["path"] == "parity/runs/run-10/output.bin":
                first.write_bytes(b"concurrent new evidence")
            return result
        with mock.patch.object(archive, "unchanged", side_effect=edit_during_last_check):
            with self.assertRaisesRegex(ValueError, "changed since verification"):
                archive.prune(self.root)
        self.assertEqual(first.read_bytes(), b"concurrent new evidence")
        for name in self.files:
            self.assertTrue((self.root / name).is_file())

    def test_restore_refuses_overwrite_before_writing_any_files(self):
        archive.prune(self.root)
        target = self.root / "parity/runs/run-10/output.bin"
        target.parent.mkdir(parents=True)
        target.write_bytes(b"new evidence")
        with self.assertRaisesRegex(ValueError, "different existing file"):
            archive.restore(self.root)
        self.assertEqual(target.read_bytes(), b"new evidence")
        self.assertFalse((self.root / "parity/runs/run-1/output.bin").exists())

    def test_restore_refuses_parent_symlinks(self):
        archive.prune(self.root)
        outside = self.root / "outside"
        outside.mkdir()
        (self.root / "parity/runs/batch-1-evidence").symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "Symlink"):
            archive.restore(self.root)
        self.assertEqual(list(outside.iterdir()), [])

    def test_restore_refuses_dangling_file_symlinks(self):
        archive.prune(self.root)
        target = self.root / "parity/runs/run-1/output.bin"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.symlink_to(self.root / "must-not-exist")
        with self.assertRaisesRegex(ValueError, "Symlink"):
            archive.restore(self.root)
        self.assertFalse((self.root / "must-not-exist").exists())

    def test_catalog_corruption_is_detected(self):
        path = self.root / archive.CATALOG
        path.write_bytes(path.read_bytes() + b"tamper")
        with self.assertRaisesRegex(ValueError, "SHA-256 differs"):
            archive.verify(self.root)

    def rewrite_catalog(self, update):
        path = self.root / archive.CATALOG
        catalog = json.loads(gzip.decompress(path.read_bytes()))
        update(catalog)
        path.write_bytes(gzip.compress(json.dumps(catalog).encode(), mtime=0))
        manifest_path = self.root / archive.MANIFEST
        manifest = json.loads(manifest_path.read_text())
        manifest["catalog"]["sha256"] = archive.digest(path)
        manifest_path.write_text(json.dumps(manifest))

    def test_catalog_cannot_redirect_to_different_git_blob(self):
        def change(catalog):
            catalog["files"][0]["git_blob"] = "0" * 40
        self.rewrite_catalog(change)
        with self.assertRaisesRegex(ValueError, "original Git tree"):
            archive.verify(self.root)

    def test_catalog_cannot_escape_runs_root(self):
        def change(catalog):
            catalog["files"][0]["path"] = "parity/runs/../../outside"
        self.rewrite_catalog(change)
        with self.assertRaisesRegex(ValueError, "Noncanonical"):
            archive.verify(self.root)

    def test_incorrect_sha256_never_creates_restored_output(self):
        archive.prune(self.root)
        def change(catalog):
            for entry in catalog["files"]:
                if entry["path"] == "parity/runs/run-1/repro.sh":
                    entry["sha256"] = "0" * 64
        self.rewrite_catalog(change)
        target = self.root / "restored"
        with self.assertRaisesRegex(ValueError, "SHA-256 differs"):
            archive.restore(self.root, ["parity/runs/run-1/repro.sh"], destination=target)
        self.assertFalse((target / "parity/runs/run-1/repro.sh").exists())
        self.assertFalse(list(target.rglob(".parity-restore-*")))

    def test_bundle_recovers_without_original_repository(self):
        backup = self.root / "backup.bundle"
        archive.git(self.root, "bundle", "create", str(backup), "HEAD")
        fresh = self.root / "fresh"
        fresh.mkdir()
        archive.git(fresh, "init", "-b", "main")
        # No access to the original object directory or any network service.
        archive.git(fresh, "fetch", str(backup), "HEAD")
        for name in (archive.MANIFEST, archive.CATALOG):
            target = fresh / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes((self.root / name).read_bytes())
        result = archive.restore(fresh)
        self.assertEqual(result["restored"], 5)
        self.assertEqual(archive.verify(fresh)["source_commit"], self.source)

    def test_new_runs_stay_ignored_and_summaries_can_be_promoted(self):
        (self.root / ".gitignore").write_text((archive.ROOT / ".gitignore").read_text())
        name = "parity/runs/new-run/raw.json"
        target = self.root / name
        target.parent.mkdir(parents=True)
        target.write_text('{"raw":true}\n')
        self.assertTrue(archive.git(self.root, "check-ignore", name).strip())
        archive.git(self.root, "add", ".")
        self.assertNotIn(name, archive.git(self.root, "ls-files").decode().splitlines())
        summary = target.parent / "result.json"
        summary.write_text('{"failed":true}\n')
        archive.git(self.root, "add", "-f", str(summary.relative_to(self.root)))
        self.assertIn(str(summary.relative_to(self.root)), archive.git(self.root, "ls-files").decode().splitlines())


if __name__ == "__main__":
    unittest.main(verbosity=2)
