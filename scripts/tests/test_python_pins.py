"""Fixture tests for scripts/python_pins.py. Run: python3 -m unittest discover -s scripts/tests"""

import io
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import python_pins as pp  # noqa: E402

PIN = "0.11.13"
RUFF = "0.17.0"

PRECOMMIT = f"""\
repos:
  - repo: https://github.com/astral-sh/uv-pre-commit
    rev: {PIN}
    hooks:
      - id: uv-lock
  - repo: https://github.com/astral-sh/ruff-pre-commit
    rev: v{RUFF}  # keep equal to .ruff-version
    hooks:
      - id: ruff-format
"""


def pyproject(floor=f">={PIN}", requires=">=3.12,<3.13"):
    uv = "[tool.uv]\npackage = false\n" + (f'required-version = "{floor}"\n' if floor else "")
    return f'[project]\nname = "x"\nrequires-python = "{requires}"\n\n{uv}'


def dockerfile(uv=PIN, py="3.12-slim-bookworm"):
    return f"FROM python:{py}\nCOPY --from=ghcr.io/astral-sh/uv:{uv} /uv /usr/local/bin/uv\n"


def good_tree():
    files = {
        ".uv-version": PIN + "\n",
        ".ruff-version": RUFF + "\n",
        ".pre-commit-config.yaml": PRECOMMIT,
        "ruff.toml": f'required-version = ">={RUFF}"\nline-length = 120\n',
    }
    for d in ("src/leaflet", "src/store", "src/template/{{cookiecutter.project_slug}}"):
        files[f"{d}/pyproject.toml"] = pyproject()
        files[f"{d}/Dockerfile"] = dockerfile()
        files[f"{d}/.python-version"] = "3.12\n"
    files["src/leaflet/uv.lock"] = "version = 1\n"
    files["src/store/uv.lock"] = "version = 1\n"
    return files


class Tree:
    def __init__(self, files):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        for rel, content in files.items():
            p = os.path.join(self.root, rel)
            os.makedirs(os.path.dirname(p), exist_ok=True)
            with open(p, "w", encoding="utf-8") as fh:
                fh.write(content)

    def run(self, *args):
        out = io.StringIO()
        code = pp.main(["--root", self.root, *args], out)
        return code, out.getvalue()


def errors(out):
    return [ln for ln in out.splitlines() if ln.startswith("::error")]


def run(files, *args):
    t = Tree(files)
    try:
        return t.run(*args)
    finally:
        t.tmp.cleanup()


class UvPins(unittest.TestCase):
    def test_pass(self):
        code, out = run(good_tree(), "uv-pins")
        self.assertEqual((code, errors(out)), (0, []), out)
        self.assertIn(f"uv {PIN}:", out)

    def test_hook_rev_mismatch(self):
        f = good_tree()
        f[".pre-commit-config.yaml"] = PRECOMMIT.replace(f"rev: {PIN}", "rev: 0.11.12")
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        self.assertEqual(len(errors(out)), 1, out)
        self.assertIn("file=.pre-commit-config.yaml,line=3", out)
        self.assertIn("rev is 0.11.12, expected 0.11.13", out)

    def test_dockerfile_tag_mismatch_one_annotation_per_file(self):
        f = good_tree()
        f["src/store/Dockerfile"] = dockerfile(uv="0.11.12")
        f["src/leaflet/Dockerfile"] = dockerfile(uv="0.10.0")
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        errs = errors(out)
        self.assertEqual(len(errs), 2, out)
        self.assertTrue(any("file=src/store/Dockerfile,line=2" in e and "expected 0.11.13" in e for e in errs))
        self.assertTrue(any("file=src/leaflet/Dockerfile" in e and "0.10.0" in e for e in errs))

    def test_pin_file_drift(self):
        f = good_tree()
        f[".uv-version"] = "0.12.0\n"
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        # hook rev, 3 Dockerfiles, and the 3 floors >=0.11.13 still pass for 0.12.0
        self.assertEqual(len(errors(out)), 4, out)

    def test_floor_not_satisfied(self):
        f = good_tree()
        f["src/store/pyproject.toml"] = pyproject(floor=">=0.12.0")
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        self.assertEqual(len(errors(out)), 1, out)
        self.assertIn("file=src/store/pyproject.toml", out)
        self.assertIn("not satisfied by .uv-version 0.11.13", out)

    def test_upper_bound_not_satisfied(self):
        f = good_tree()
        f["src/store/pyproject.toml"] = pyproject(floor=">=0.11.0,<0.11.13")
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1, out)

    def test_exact_equal_passes_with_warning(self):
        f = good_tree()
        f["src/store/pyproject.toml"] = pyproject(floor=f"=={PIN}")
        code, out = run(f, "uv-pins")
        self.assertEqual((code, errors(out)), (0, []), out)
        self.assertIn("::warning file=src/store/pyproject.toml", out)

    def test_missing_floor_in_project_with_lock_is_error(self):
        f = good_tree()
        f["src/leaflet/pyproject.toml"] = pyproject(floor=None)
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        self.assertIn("file=src/leaflet/pyproject.toml", out)
        self.assertIn("required-version is missing", out)

    def test_missing_floor_without_tool_uv_table_still_error_when_lock_present(self):
        f = good_tree()
        f["src/leaflet/pyproject.toml"] = '[project]\nname = "x"\nrequires-python = ">=3.12,<3.13"\n'
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        self.assertIn("file=src/leaflet/pyproject.toml", out)

    def test_missing_uv_version_file(self):
        code, out = run({**good_tree(), ".uv-version": ""}, "uv-pins")
        self.assertEqual(code, 1)
        self.assertIn("file=.uv-version", out)
        f = good_tree()
        del f[".uv-version"]
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        self.assertIn(".uv-version is missing", out)

    def test_dockerfile_without_uv_copy(self):
        f = good_tree()
        f["src/store/Dockerfile"] = "FROM python:3.12-slim-bookworm\nRUN pip install uv\n"
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        self.assertIn("file=src/store/Dockerfile", out)
        self.assertIn("no COPY --from=ghcr.io/astral-sh/uv", out)

    def test_digest_suffix_ignored_and_untagged_flagged(self):
        f = good_tree()
        f["src/store/Dockerfile"] = f"FROM python:3.12\nCOPY --from=ghcr.io/astral-sh/uv:{PIN}@sha256:abc /uv /bin/uv\n"
        f["src/leaflet/Dockerfile"] = "FROM python:3.12\nCOPY --from=ghcr.io/astral-sh/uv /uv /bin/uv\n"
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        errs = errors(out)
        self.assertEqual(len(errs), 1, out)
        self.assertIn("src/leaflet/Dockerfile", errs[0])

    def test_template_project_is_checked(self):
        f = good_tree()
        tpl = "src/template/{{cookiecutter.project_slug}}"
        f[f"{tpl}/Dockerfile"] = dockerfile(uv="0.9.0")
        f[f"{tpl}/pyproject.toml"] = pyproject(floor=">=0.99.0")
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        self.assertEqual(len(errors(out)), 2, out)
        self.assertIn("src/template/{{cookiecutter.project_slug}}/Dockerfile", out)

    def test_hook_entry_missing(self):
        f = good_tree()
        f[".pre-commit-config.yaml"] = "repos: []\n"
        code, out = run(f, "uv-pins")
        self.assertEqual(code, 1)
        self.assertIn("no astral-sh/uv-pre-commit entry", out)

    def test_no_uv_project_is_a_notice(self):
        code, out = run({"README.md": "x"}, "uv-pins")
        self.assertEqual(code, 0)
        self.assertIn("::notice", out)

    def test_annotation_escaping(self):
        rep = pp.Report(io.StringIO())
        rep.error("a,b:c", "x\ny%", "t")
        self.assertEqual(rep.out.getvalue().strip(), "::error file=a%2Cb%3Ac,title=t::x%0Ay%25")


class RuffPins(unittest.TestCase):
    def test_pass(self):
        code, out = run(good_tree(), "ruff-pins")
        self.assertEqual((code, errors(out)), (0, []), out)

    def test_hook_rev_mismatch(self):
        f = good_tree()
        f[".pre-commit-config.yaml"] = PRECOMMIT.replace(f"v{RUFF}", "v0.16.0")
        code, out = run(f, "ruff-pins")
        self.assertEqual(code, 1)
        self.assertIn("rev is v0.16.0, expected v0.17.0", out)

    def test_rev_without_v_prefix_is_a_mismatch(self):
        f = good_tree()
        f[".pre-commit-config.yaml"] = PRECOMMIT.replace(f"v{RUFF}", RUFF)
        code, _ = run(f, "ruff-pins")
        self.assertEqual(code, 1)

    def test_floor_not_satisfied(self):
        f = good_tree()
        f["ruff.toml"] = 'required-version = ">=0.18.0"\n'
        code, out = run(f, "ruff-pins")
        self.assertEqual(code, 1)
        self.assertIn("file=ruff.toml", out)
        self.assertIn("not satisfied by .ruff-version 0.17.0", out)

    def test_pin_file_drift(self):
        f = good_tree()
        f[".ruff-version"] = "0.17.1\n"
        code, out = run(f, "ruff-pins")
        self.assertEqual(code, 1)
        self.assertEqual(len(errors(out)), 1, out)  # hook rev; floor >=0.17.0 still satisfied

    def test_missing_ruff_version(self):
        f = good_tree()
        del f[".ruff-version"]
        code, out = run(f, "ruff-pins")
        self.assertEqual(code, 1)
        self.assertIn(".ruff-version is missing", out)

    def test_missing_required_version_in_ruff_toml(self):
        f = good_tree()
        f["ruff.toml"] = "line-length = 120\n"
        code, out = run(f, "ruff-pins")
        self.assertEqual(code, 1)
        self.assertIn("file=ruff.toml", out)
        self.assertIn("required-version is missing", out)

    def test_no_ruff_config_at_all(self):
        f = good_tree()
        del f["ruff.toml"]
        code, out = run(f, "ruff-pins")
        self.assertEqual(code, 1)
        self.assertIn("no ruff.toml", out)

    def test_pyproject_tool_ruff_floor(self):
        f = good_tree()
        del f["ruff.toml"]
        f["pyproject.toml"] = '[tool.ruff]\nrequired-version = ">=0.18"\n'
        code, out = run(f, "ruff-pins")
        self.assertEqual(code, 1)
        self.assertIn("file=pyproject.toml", out)

    def test_hook_entry_missing(self):
        f = good_tree()
        f[".pre-commit-config.yaml"] = PRECOMMIT.split("  - repo: https://github.com/astral-sh/ruff")[0]
        code, out = run(f, "ruff-pins")
        self.assertEqual(code, 1)
        self.assertIn("no astral-sh/ruff-pre-commit entry", out)

    def test_workflow_version_drift_is_a_warning(self):
        code, out = run(good_tree(), "ruff-pins", "--workflow-version", "0.18.0")
        self.assertEqual((code, errors(out)), (0, []), out)
        self.assertIn("::warning file=.ruff-version", out)
        code, out = run(good_tree(), "ruff-pins", "--workflow-version", "v0.17.0")
        self.assertNotIn("::warning", out)


class PythonVersion(unittest.TestCase):
    def test_pass(self):
        code, out = run(good_tree(), "python-version")
        self.assertEqual((code, errors(out)), (0, []), out)

    def test_from_mismatch(self):
        f = good_tree()
        f["src/store/Dockerfile"] = dockerfile(py="3.11-slim-bookworm")
        code, out = run(f, "python-version")
        self.assertEqual(code, 1)
        self.assertEqual(len(errors(out)), 1, out)
        self.assertIn("file=src/store/Dockerfile,line=1", out)
        self.assertIn("expected python:3.12", out)

    def test_every_stage_is_checked(self):
        f = good_tree()
        f["src/store/Dockerfile"] = "FROM python:3.12 AS build\nFROM python:3.13-slim\n"
        code, out = run(f, "python-version")
        self.assertEqual(code, 1)
        self.assertIn("line=2", out)

    def test_patch_level_ignored(self):
        f = good_tree()
        f["src/store/.python-version"] = "3.12.4\n"
        f["src/store/Dockerfile"] = dockerfile(py="3.12.9-slim")
        code, out = run(f, "python-version")
        self.assertEqual((code, errors(out)), (0, []), out)

    def test_missing_python_version_file(self):
        f = good_tree()
        del f["src/store/.python-version"]
        code, out = run(f, "python-version")
        self.assertEqual(code, 1)
        self.assertIn("src/store/.python-version is missing", out)

    def test_untagged_from(self):
        f = good_tree()
        f["src/store/Dockerfile"] = "FROM python\n"
        code, _ = run(f, "python-version")
        self.assertEqual(code, 1)

    def test_requires_python_not_satisfied(self):
        f = good_tree()
        f["src/store/pyproject.toml"] = pyproject(requires=">=3.13")
        code, out = run(f, "python-version")
        self.assertEqual(code, 1)
        self.assertIn("requires-python >=3.13 is not satisfied", out)

    def test_dockerfile_without_python_base_is_notice(self):
        f = good_tree()
        f["src/store/Dockerfile"] = "FROM debian:bookworm\n"
        code, out = run(f, "python-version")
        self.assertEqual(code, 0)
        self.assertIn("::notice file=src/store/Dockerfile", out)


class Projects(unittest.TestCase):
    def test_pass(self):
        code, out = run(good_tree(), "projects", '["src/leaflet","src/store"]')
        self.assertEqual((code, errors(out)), (0, []), out)

    def test_listed_but_missing(self):
        code, out = run(good_tree(), "projects", '["src/leaflet","src/store","src/nope"]')
        self.assertEqual(code, 1)
        self.assertIn("src/nope/uv.lock", out)

    def test_uv_lock_not_listed(self):
        code, out = run(good_tree(), "projects", '["src/leaflet"]')
        self.assertEqual(code, 1)
        self.assertIn("file=src/store/uv.lock", out)

    def test_bad_json_and_bad_shapes(self):
        for raw in ("not json", "[]", '"src/store"', "[1]", '["../x"]', '["/abs"]'):
            code, _ = run(good_tree(), "projects", raw)
            self.assertEqual(code, 1, raw)

    def test_dot_slash_normalised(self):
        code, out = run(good_tree(), "projects", '["./src/leaflet/","src/store"]')
        self.assertEqual((code, errors(out)), (0, []), out)


class Usage(unittest.TestCase):
    def test_usage_errors(self):
        for args in ([], ["nope"], ["uv-pins", "extra"], ["projects"]):
            code, _ = run(good_tree(), *args)
            self.assertEqual(code, 2, args)

    def test_satisfies(self):
        self.assertEqual(pp.satisfies("0.11.13", ">=0.11.13"), (True, None))
        self.assertEqual(pp.satisfies("0.11.13", ">=0.11.14"), (False, None))
        self.assertEqual(pp.satisfies("3.12", ">=3.12,<3.13"), (True, None))
        self.assertEqual(pp.satisfies("1.0", "==1.0.0"), (True, None))
        self.assertIsNotNone(pp.satisfies("1.0", "~=1.0")[1])
        self.assertIsNotNone(pp.satisfies("1.0rc1", ">=1.0")[1])


if __name__ == "__main__":
    unittest.main()
