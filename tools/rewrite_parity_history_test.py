#!/usr/bin/env python3
"""Audit source preservation and removal of old objects in an independent clone."""
from pathlib import Path
import tempfile
import unittest

import rewrite_parity_history as rewrite


class HistoryTests(unittest.TestCase):
    def test_all_branches_keep_code_metadata_and_failure_states(self):
        with tempfile.TemporaryDirectory(prefix="ponder-history-filter-") as temporary:
            root = Path(temporary)
            source = root / "source"
            source.mkdir()
            rewrite.git(source, "init", "-b", "main")
            rewrite.git(source, "config", "user.name", "Archive Test")
            rewrite.git(source, "config", "user.email", "archive-test@example.invalid")
            files = {"app.go": b"package app\n", "parity/test-ledger.json": b'{"failed":1,"skip":2}\n',
                     "parity/runs/result.json": b'{"passed":false}\n',
                     "parity/runs/raw/subdir/old.bin": bytes(range(256)) * 8192}
            for name, data in files.items():
                path = source / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            rewrite.git(source, "add", ".")
            rewrite.git(source, "commit", "-m", "first failure preserved")
            first = rewrite.git(source, "rev-parse", "HEAD").decode().strip()
            blob = rewrite.git(source, "rev-parse", "HEAD:parity/runs/raw/subdir/old.bin").decode().strip()
            rewrite.git(source, "switch", "-c", "other")
            (source / "other.go").write_text("package app\n")
            rewrite.git(source, "add", ".")
            rewrite.git(source, "commit", "-m", "other branch")
            rewrite.git(source, "switch", "main")
            rewrite.git(source, "rm", "parity/runs/raw/subdir/old.bin")
            (source / "app.go").write_text("package app\n// second version\n")
            rewrite.git(source, "add", ".")
            rewrite.git(source, "commit", "-m", "remove raw data")
            before = rewrite.git(source, "rev-parse", "HEAD")
            report = rewrite.rewrite(source, root / "filtered.git", root / "report.json")
            self.assertEqual(rewrite.git(source, "rev-parse", "HEAD"), before)
            self.assertTrue(report["fsck_passed"])
            self.assertEqual(len(report["commits"]), 3)
            self.assertTrue(all(x["message_author_committer_unchanged"] for x in report["commits"]))
            mapped = {x["old"]: x["new"] for x in report["commits"]}
            self.assertNotEqual(first, mapped[first])
            clone = root / "fresh"
            rewrite.git(source, "clone", "--no-local", str(root / "filtered.git"), str(clone))
            self.assertEqual((clone / "parity/test-ledger.json").read_bytes(), files["parity/test-ledger.json"])
            self.assertEqual((clone / "parity/runs/result.json").read_bytes(), files["parity/runs/result.json"])
            self.assertFalse((clone / "parity/runs/raw").exists())
            with self.assertRaises(RuntimeError):
                rewrite.git(clone, "cat-file", "-e", blob)
            self.assertFalse((clone / ".git/objects/info/alternates").exists())
            original = rewrite.git(source, "cat-file", "commit", first)
            filtered = rewrite.git(clone, "cat-file", "commit", mapped[first])
            self.assertEqual(original.split(b"\n\n", 1)[1], filtered.split(b"\n\n", 1)[1])
            for prefix in (b"author ", b"committer "):
                self.assertEqual([x for x in original.splitlines() if x.startswith(prefix)],
                                 [x for x in filtered.splitlines() if x.startswith(prefix)])


if __name__ == "__main__":
    unittest.main(verbosity=2)
