import json
from pathlib import Path
import tempfile
import unittest

import upstream_suite_report as report


class ReportTest(unittest.TestCase):
    def test_preserves_declared_skips_and_duplicate_titles(self):
        name = "packages/core/x.test.ts"
        active, skipped = (name, "repeat", "active"), (name, "y", "skip")
        groups = {active: ["a", "b"], skipped: ["c"]}
        outcomes = {active: report.Counter(passed=2), skipped: report.Counter(skipped=1)}
        report.audit_scope({name}, outcomes, {name}, groups)
        with self.assertRaisesRegex(ValueError, "declarations differ"):
            report.audit_scope({name}, {**outcomes, active: report.Counter(passed=1)}, {name}, groups)
        with self.assertRaisesRegex(ValueError, "declarations differ"):
            report.audit_scope({name}, {active: report.Counter(passed=2),
                               (name, "y", "active"): report.Counter(passed=1)}, {name}, groups)

    def test_runtime_backend_must_have_its_entire_scope(self):
        with self.assertRaisesRegex(ValueError, "files"):
            report.audit_scope({"packages/core/x.test-d.ts"}, {}, {"packages/core/x.test.ts"}, {})

    def test_secondary_failure_and_indistinguishable_duplicates(self):
        a, b = ("f", "one", "active"), ("f", "two", "active")
        counts, unresolved, differences = report.combine(
            {a: report.Counter(passed=1), b: report.Counter(passed=1, failed=1)},
            {a: report.Counter(failed=1), b: report.Counter(passed=1, failed=1)},
            {a: ["1"], b: ["2", "3"]})
        self.assertEqual(counts, {"failed": 1, "ambiguous": 2})
        self.assertEqual([g["name"] for g in unresolved], ["one", "two"])
        self.assertEqual(differences[0]["name"], "one")

    def test_canonical_file(self):
        name = "packages/utils/src/rpc.test.ts"
        self.assertEqual(report.file_name("/var/packages/checkout/" + name, {name}), name)
        for wrong in ("../" + name, "/var/" + name + ".bak"):
            with self.assertRaises(ValueError):
                report.file_name(wrong, {name})

    def test_type_title_and_process_status(self):
        name = "packages/core/src/foo.test-d.ts"
        test = {"ancestorTitles": ["src/foo.test-d.ts", "outer"], "title": "works", "status": "passed"}
        suite = {"name": "/checkout/" + name, "status": "passed", "assertionResults": [test]}
        doc = {"testResults": [suite], "numTotalTests": 1, "numPassedTests": 1, "numFailedTests": 0,
               "numPendingTests": 0, "numTodoTests": 0, "numTotalTestSuites": 2, "numPassedTestSuites": 2,
               "numFailedTestSuites": 0, "numPendingTestSuites": 0, "success": True}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "types.json"
            path.write_text(json.dumps(doc))
            path.with_suffix(".exit").write_text("0")
            self.assertEqual(report.read_report(path, {name})[1],
                             {(name, "outer > works", "active"): {"passed": 1}})
            path.with_suffix(".exit").write_text("1")
            with self.assertRaisesRegex(ValueError, "Vitest/process"):
                report.read_report(path, {name})
            suite["status"] = "failed"
            path.write_text(json.dumps(doc))
            with self.assertRaisesRegex(ValueError, "file status"):
                report.read_report(path, {name})


if __name__ == "__main__":
    unittest.main()
