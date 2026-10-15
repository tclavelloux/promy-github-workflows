"""Fixture tests for scripts/python_coverage_floor.py."""

import contextlib
import io
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import python_coverage_floor as cf  # noqa: E402


def run(pyproject):
    with tempfile.TemporaryDirectory() as d:
        if pyproject is not None:
            with open(os.path.join(d, "pyproject.toml"), "w", encoding="utf-8") as fh:
                fh.write(pyproject)
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = cf.main([d])
        return code, out.getvalue().strip(), err.getvalue()


RUN = '[tool.coverage.run]\nsource = ["src/job"]\n'


class CoverageFloor(unittest.TestCase):
    def test_int_floor(self):
        self.assertEqual(run(RUN + "[tool.coverage.report]\nfail_under = 62\n")[:2], (0, "62"))

    def test_float_floor(self):
        self.assertEqual(run(RUN + "[tool.coverage.report]\nfail_under = 62.5\n")[:2], (0, "62.5"))

    def test_missing_fail_under_is_error(self):
        code, out, err = run(RUN + "[tool.coverage.report]\nshow_missing = true\n")
        self.assertEqual((code, out), (1, ""))
        self.assertIn("::error file=", err)
        self.assertIn("fail_under is missing", err)

    def test_missing_coverage_section_is_error(self):
        self.assertEqual(run('[project]\nname = "x"\n')[0], 1)

    def test_zero_negative_and_over_100_are_errors(self):
        for v in ("0", "-1", "101", "0.0"):
            code, out, err = run(RUN + f"[tool.coverage.report]\nfail_under = {v}\n")
            self.assertEqual((code, out), (1, ""), v)
            self.assertIn("(0, 100]", err)

    def test_non_numeric_and_bool_are_errors(self):
        for v in ('"80"', "true"):
            self.assertEqual(run(RUN + f"[tool.coverage.report]\nfail_under = {v}\n")[0], 1, v)

    def test_no_pyproject_is_error(self):
        self.assertEqual(run(None)[0], 1)

    def test_missing_source_warns_but_passes(self):
        code, out, err = run("[tool.coverage.report]\nfail_under = 50\n")
        self.assertEqual((code, out), (0, "50"))
        self.assertIn("::warning", err)

    def test_usage(self):
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(cf.main([]), 2)


if __name__ == "__main__":
    unittest.main()
