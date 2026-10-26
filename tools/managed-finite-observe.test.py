#!/usr/bin/env python3
"""Supplemental process-identity controls; no database or product-test credit."""
import importlib.util
import os
import pathlib
import subprocess
import sys
import unittest

PATH = pathlib.Path(__file__).with_name("managed-finite-observe.py")
spec = importlib.util.spec_from_file_location("managed_observer", PATH)
observer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observer)

class ObserverIdentityControls(unittest.TestCase):
    def test_exact_owned_child_session_and_exit(self):
        # Only this test-owned child is started/joined. No unrelated process is
        # signaled, and the sampler output retained here contains no cmdline/env.
        child = subprocess.Popen([sys.executable, "-c", "import sys; sys.stdin.read()"],
                                 stdin=subprocess.PIPE, start_new_session=True)
        try:
            rows = observer.census()
            identity = next(row for row in rows.values() if row["pid"] == child.pid)
            self.assertEqual(identity["ppid"], os.getpid())
            self.assertEqual(identity["pgid"], child.pid)
            key = (child.pid, identity["start_ticks"])
            self.assertEqual(observer.census()[key]["start_ticks"], identity["start_ticks"])
            child.stdin.close()
            self.assertEqual(child.wait(timeout=5), 0)
            self.assertNotIn(key, observer.census())
        finally:
            if child.poll() is None:
                child.stdin.close()
                child.wait(timeout=5)

    def test_nested_and_outside_output_rejected_without_mutation(self):
        for path in ("parity/runs/managed-finite-m44/nested/run", "parity/runs/finite-historical-m43/not-m44"):
            with self.assertRaises(RuntimeError):
                observer.scoped(path, "parity/runs/managed-finite-m44")

    def test_existing_input_is_not_overwritable_output(self):
        with self.assertRaises(RuntimeError):
            observer.scoped("tools/managed-finite-observe.py", "tools")

if __name__ == "__main__":
    unittest.main()
