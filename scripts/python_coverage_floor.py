#!/usr/bin/env python3
"""Print a project's coverage floor, or fail when it is missing or meaningless.

Local twin of the step inlined in .github/workflows/python-test.yml: the block
between the `python-coverage-floor body` markers must stay byte-identical in both
(internal/syncheck/governance_test.go compares them).

Usage: scripts/python_coverage_floor.py <project-dir>
Prints the floor on stdout (one number); annotations go to stderr, which the
Actions runner parses the same way. Exit: 0 floor printed, 1 error annotation, 2 usage.
"""

# >>> python-coverage-floor body
# Keep this block byte-identical between scripts/python_coverage_floor.py and
# .github/workflows/python-test.yml (governance CI diffs them).
import os
import sys

if sys.version_info < (3, 11):
    sys.exit("::error::python-coverage-floor needs Python >= 3.11 (tomllib)")
import tomllib


def annotate(level, file, msg):
    msg = msg.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
    file = file.replace("%", "%25").replace(":", "%3A").replace(",", "%2C")
    print(f"::{level} file={file}::{msg}", file=sys.stderr)


def coverage_floor(project):
    """(floor, None) or (None, error message). Missing, zero, negative or > 100 is an error."""
    rel = os.path.join(project, "pyproject.toml")
    try:
        with open(rel, "rb") as fh:
            data = tomllib.load(fh)
    except (OSError, tomllib.TOMLDecodeError) as exc:
        return None, f"cannot read {rel}: {exc}"
    cov = data.get("tool", {}).get("coverage", {})
    floor = cov.get("report", {}).get("fail_under")
    if floor is None:
        return None, (
            f"[tool.coverage.report] fail_under is missing in {rel}. A missing floor is an error, never zero: "
            "set it to floor(measured coverage)."
        )
    if isinstance(floor, bool) or not isinstance(floor, (int, float)):
        return None, f"[tool.coverage.report] fail_under must be a number, found {floor!r}"
    if not 0 < floor <= 100:
        return None, f"[tool.coverage.report] fail_under = {floor} must be in (0, 100]; zero is not a gate"
    run = cov.get("run", {})
    if not (run.get("source") or run.get("source_pkgs")):
        annotate("warning", rel, "[tool.coverage.run] has no source: unexecuted files are not measured and inflate coverage")
    return floor, None


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    if len(argv) != 1:
        print("usage: python_coverage_floor.py <project-dir>", file=sys.stderr)
        return 2
    floor, err = coverage_floor(argv[0])
    if err:
        annotate("error", os.path.join(argv[0], "pyproject.toml"), err)
        return 1
    print(floor)
    return 0


if __name__ == "__main__":
    sys.exit(main())
# <<< python-coverage-floor body
