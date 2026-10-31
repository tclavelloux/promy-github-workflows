"""Tests for scripts/python_vuln_fix.py. Run: python3 -m unittest discover -s scripts/tests

No network: osv-scanner and uv are fake executables replaying a JSON script, so the
tests cover the decisions (gates, refusals, cascades, rescan), not the real tools.
"""

import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "..", "python_vuln_fix.py")
FIXTURES = os.path.join(HERE, "fixtures", "python_vuln_fix")
sys.path.insert(0, os.path.join(HERE, ".."))
import python_vuln_fix as pvf  # noqa: E402

FAKE_OSV = f"""#!{sys.executable}
import json, os, sys
cfg = json.load(open(os.environ["FAKE_CFG"]))
args = sys.argv[1:]
state = os.environ["FAKE_STATE"]
lock = args[args.index("--lockfile") + 1]
counter = os.path.join(state, "osv-" + lock.replace("/", "_"))
n = int(open(counter).read()) if os.path.exists(counter) else 0
open(counter, "w").write(str(n + 1))
with open(os.path.join(state, "osv.log"), "a") as f:
    f.write(json.dumps(args) + "\\n")
seq = cfg["osv"].get(lock, [])
item = seq[min(n, len(seq) - 1)] if seq else None
if item is None:
    sys.exit(0)
if item == "error":
    print("scanner exploded", file=sys.stderr)
    sys.exit(3)
print(json.dumps(item))
sys.exit(1)
"""

FAKE_UV = f"""#!{sys.executable}
import json, os, re, sys
cfg = json.load(open(os.environ["FAKE_CFG"]))
args = sys.argv[1:]
with open(os.path.join(os.environ["FAKE_STATE"], "uv.log"), "a") as f:
    f.write(json.dumps({{"args": args, "cwd": os.getcwd()}}) + "\\n")
if args == ["--version"]:
    print("uv 0.12.18 (fake)")
    sys.exit(0)
pkg, ver = args[args.index("--upgrade-package") + 1].split("==")
rule = cfg["uv"].get(pkg, {{}})
mode = rule.get("mode", "bump")
if mode == "refuse":
    print("error: No solution found when resolving dependencies:\\n  cause: your project requirements are unsatisfiable.", file=sys.stderr)
    sys.exit(1)
if mode == "neterr":
    print("error: Failed to fetch: https://pypi.org/simple/x/\\n  Caused by: dns error", file=sys.stderr)
    sys.exit(2)
text = open("uv.lock").read()
def setv(name, v):
    global text
    text = re.sub('(name = "%s"\\\\nversion = )"[^"]*"' % re.escape(name), lambda m: m.group(1) + '"' + v + '"', text)
setv(pkg, rule.get("version", ver))
for name, v in rule.get("also", {{}}).items():
    setv(name, v)
open("uv.lock", "w").write(text)
"""


def make_lock(pkgs):
    out = 'version = 1\nrevision = 3\nrequires-python = "==3.12.*"\n'
    for name, ver in pkgs.items():
        out += (f'\n[[package]]\nname = "{name}"\nversion = "{ver}"\nsource = {{ registry = "https://pypi.org/simple" }}\n'
                f'sdist = {{ url = "https://files.pythonhosted.org/packages/00/00/{name}-{ver}.tar.gz", hash = "sha256:{"0" * 64}", size = 1 }}\n')
    return out


def fixture(name):
    with open(os.path.join(FIXTURES, name), encoding="utf-8") as f:
        return f.read()


def report(*pkgs):
    """An osv-scanner JSON report from (name, version, [advisory dicts])."""
    packages = []
    for name, version, advs in pkgs:
        groups, vulns = [], []
        for a in advs:
            groups.append({"ids": a["ids"], "aliases": a["ids"], "max_severity": a.get("sev", "7.5")})
            for i in a["ids"]:
                events = [{"introduced": a.get("intro", "0")}]
                if a.get("fixed"):
                    events.append({"fixed": a["fixed"]})
                vulns.append({"id": i, "aliases": [], "affected": [
                    {"package": {"ecosystem": "PyPI", "name": name}, "ranges": [{"type": "ECOSYSTEM", "events": events}]}]})
        packages.append({"package": {"name": name, "version": version, "ecosystem": "PyPI"}, "groups": groups, "vulnerabilities": vulns})
    return {"results": [{"source": {"path": "uv.lock", "type": "lockfile"}, "packages": packages}]}


def adv(ident, fixed, **kw):
    return dict(ids=[ident], fixed=fixed, **kw)


class Workspace:
    """A temp repo with fake osv-scanner and uv; `run` drives the real script."""

    def __init__(self, test, locks, osv=None, uv=None, pin="0.12.18"):
        self.test = test
        self.tmp = tempfile.mkdtemp()
        test.addCleanup(shutil.rmtree, self.tmp, True)
        self.root = os.path.join(self.tmp, "repo")
        self.state = os.path.join(self.tmp, "state")
        self.out = os.path.join(self.tmp, "out")
        os.makedirs(self.state)
        for project, text in locks.items():
            os.makedirs(os.path.join(self.root, project), exist_ok=True)
            with open(os.path.join(self.root, project, "uv.lock"), "w") as f:
                f.write(text)
        if pin is not None:
            os.makedirs(self.root, exist_ok=True)
            with open(os.path.join(self.root, ".uv-version"), "w") as f:
                f.write(pin + "\n")
        self.osv_bin = os.path.join(self.tmp, "osv-scanner")
        self.uv_bin = os.path.join(self.tmp, "uv")
        for path, body in ((self.osv_bin, FAKE_OSV), (self.uv_bin, FAKE_UV)):
            with open(path, "w") as f:
                f.write(body)
            os.chmod(path, 0o755)
        self.cfg = os.path.join(self.tmp, "cfg.json")
        with open(self.cfg, "w") as f:
            json.dump({"osv": {f"{p}/uv.lock": seq for p, seq in (osv or {}).items()}, "uv": uv or {}}, f)
        self.before = {p: self.lock(p) for p in locks}

    def lock(self, project):
        with open(os.path.join(self.root, project, "uv.lock")) as f:
            return f.read()

    def run(self, projects, exclude="", allow_major=None, uv=None):
        cmd = [sys.executable, SCRIPT, "run", "--projects", json.dumps(projects), "--out", self.out, "--root", self.root,
               "--osv-scanner", self.osv_bin, "--uv", uv or self.uv_bin, "--exclude", exclude]
        if allow_major is not None:
            cmd += ["--allow-major", allow_major]
        env = dict(os.environ, FAKE_CFG=self.cfg, FAKE_STATE=self.state, PYTHONDONTWRITEBYTECODE="1")
        self.proc = subprocess.run(cmd, capture_output=True, text=True, env=env)
        return self.proc.returncode

    def read(self, name):
        with open(os.path.join(self.out, name), encoding="utf-8") as f:
            return f.read()

    def rows(self, name):
        return [line.split("\t") for line in self.read(name).splitlines()]

    def uv_calls(self):
        path = os.path.join(self.state, "uv.log")
        if not os.path.exists(path):
            return []
        with open(path) as f:
            return [json.loads(line) for line in f]

    def osv_calls(self):
        path = os.path.join(self.state, "osv.log")
        if not os.path.exists(path):
            return []
        with open(path) as f:
            return [json.loads(line) for line in f]

    def lock_calls(self):
        return [c for c in self.uv_calls() if c["args"] != ["--version"]]


STORE = lambda: fixture("store.uv.lock")  # noqa: E731
LEAFLET = lambda: fixture("leaflet.uv.lock")  # noqa: E731
CERTIFI = json.loads(fixture("osv-store-certifi.json"))
PILLOW = json.loads(fixture("osv-leaflet-pillow12.json"))


class Pep440Test(unittest.TestCase):
    def lt(self, a, b):
        self.assertLess(pvf.parse_version(a), pvf.parse_version(b), f"{a} < {b}")

    def test_ordering(self):
        self.lt("1.0rc1", "1.0")
        self.lt("1.0", "1.0.post1")
        self.lt("1.0.dev1", "1.0a1")
        self.lt("1.0a1", "1.0b1")
        self.lt("1.0b2", "1.0rc1")
        self.lt("1.0", "1.0.1")
        self.lt("2.0", "1!0.5")

    def test_epoch_wins(self):
        self.assertLess(pvf.parse_version("9.9"), pvf.parse_version("1!0.1"))

    def test_trailing_zeros_equal(self):
        self.assertEqual(pvf.parse_version("1.0"), pvf.parse_version("1.0.0"))
        self.assertEqual(pvf.parse_version("2026.10.19"), pvf.parse_version("2026.10.19"))

    def test_calver_zero_padding(self):
        self.assertEqual(pvf.parse_version("2024.06.02"), pvf.parse_version("2024.6.2"))

    def test_local_segment_ignored(self):
        self.assertEqual(pvf.parse_version("2.14.1+cpu"), pvf.parse_version("2.14.1"))
        self.lt("2.14.1+cpu", "2.14.2")
        self.lt("2.13.9", "2.14.1+cpu")

    def test_unparsable(self):
        self.assertIsNone(pvf.parse_version("not-a-version"))
        self.assertIsNone(pvf.parse_version(""))

    def test_major(self):
        self.assertTrue(pvf.is_major("12.3.0", "13.0.0"))
        self.assertFalse(pvf.is_major("12.3.0", "12.4.0"))
        self.assertFalse(pvf.is_major("2024.2.2", "2026.10.19"))
        self.assertTrue(pvf.is_major("999.0", "1000.0"))
        self.assertTrue(pvf.is_major("1.9", "2024.1"))
        self.assertTrue(pvf.is_major("1.0", "x"))


class ExcludeTest(unittest.TestCase):
    def test_globs(self):
        ex = pvf.parse_excludes("torch*\nultralytics*, numpy\n\n  openai ")
        self.assertEqual(pvf.excluded_by("torchvision", ex), "torch*")
        self.assertEqual(pvf.excluded_by("numpy", ex), "numpy")
        self.assertIsNone(pvf.excluded_by("numpy-financial", ex))
        self.assertIsNone(pvf.excluded_by("certifi", ex))

    def test_pep503_both_sides(self):
        ex = pvf.parse_excludes("typing_extensions*")
        self.assertEqual(pvf.excluded_by("typing-extensions", ex), "typing_extensions*")

    def test_invalid_glob(self):
        with self.assertRaises(pvf.UsageError):
            pvf.parse_excludes("torch*; rm -rf")
        with self.assertRaises(pvf.UsageError):
            pvf.parse_excludes("[abc]")


class FixTest(unittest.TestCase):
    def test_fix_within_constraints(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]})
        self.assertEqual(w.run(["store"]), 0, w.proc.stderr)
        self.assertIn('name = "certifi"\nversion = "2026.10.19"', w.lock("store"))
        self.assertNotEqual(w.lock("store"), w.before["store"])
        self.assertEqual(w.read("changed.txt"), "true\n")
        self.assertEqual(w.read("locks.txt"), "store/uv.lock\n")
        self.assertEqual(w.rows("fixed.tsv"), [[
            "store", "certifi", "2024.2.2", "2026.10.19",
            "GHSA-248v-346w-9cwc,GHSA-6q48-6c7w-9gw2,PYSEC-2024-230,PYSEC-2026-4320", "9.1"]])
        self.assertEqual(w.read("unfixed.tsv"), "")
        self.assertEqual(w.read("unfixed.txt"), "false\n")
        self.assertRegex(w.read("hash.txt"), r"^[0-9a-f]{12}\n$")
        self.assertEqual(w.read("title.txt"), "fix(deps): patch GHSA-248v-346w-9cwc, GHSA-6q48-6c7w-9gw2\n")
        self.assertEqual(w.read("ids.txt").split(), [
            "GHSA-248v-346w-9cwc", "GHSA-6q48-6c7w-9gw2", "PYSEC-2024-230", "PYSEC-2026-4320"])

    def test_highest_minimal_fix_across_advisories(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]})
        w.run(["store"])
        calls = w.lock_calls()
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["args"], ["lock", "--upgrade-package", "certifi==2026.10.19"])
        self.assertTrue(calls[0]["cwd"].endswith(os.sep + "store"), calls[0]["cwd"])

    def test_pr_body(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]})
        w.run(["store"])
        body = w.read("pr-body.md")
        self.assertIn("### store", body)
        self.assertIn("`certifi` `2024.2.2` -> `2026.10.19`", body)
        self.assertIn("[GHSA-6q48-6c7w-9gw2](https://osv.dev/vulnerability/GHSA-6q48-6c7w-9gw2)", body)
        self.assertIn("max severity 9.1", body)
        self.assertIn("uv 0.12.18 (fake)", body)
        self.assertIn("osv-scanner re-verified these IDs are gone", body)
        self.assertIn("never auto-merged", body)

    def test_scanner_invocation_and_rescan(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]})
        w.run(["store"])
        calls = w.osv_calls()
        self.assertEqual(len(calls), 2)
        self.assertEqual(calls[0], ["scan", "source", "--lockfile", "store/uv.lock", "--format", "json"])

    def test_config_lookup(self):
        w = Workspace(self, {"store": STORE(), "leaflet": LEAFLET()})
        with open(os.path.join(w.root, "store", "osv-scanner.toml"), "w") as f:
            f.write("")
        with open(os.path.join(w.root, "osv-scanner.toml"), "w") as f:
            f.write("")
        w.run(["store", "leaflet"])
        by_lock = {c[c.index("--lockfile") + 1]: c for c in w.osv_calls()}
        self.assertEqual(by_lock["store/uv.lock"][2:4], ["--config", "store/osv-scanner.toml"])
        self.assertEqual(by_lock["leaflet/uv.lock"][2:4], ["--config", "osv-scanner.toml"])

    def test_clean_scan_is_a_noop(self):
        w = Workspace(self, {"store": STORE()}, osv={})
        self.assertEqual(w.run(["store"]), 0, w.proc.stderr)
        self.assertEqual(w.lock_calls(), [])
        self.assertEqual(w.lock("store"), w.before["store"])
        self.assertEqual(w.read("changed.txt"), "false\n")
        self.assertEqual(w.read("unfixed.txt"), "false\n")
        self.assertEqual(w.read("locks.txt"), "")
        self.assertEqual(w.read("pr-body.md"), "")
        self.assertRegex(w.read("hash.txt"), r"^[0-9a-f]{12}\n$")

    def test_uv_refusal_leaves_lock_untouched(self):
        w = Workspace(self, {"leaflet": LEAFLET()}, osv={"leaflet": [PILLOW]}, uv={"pillow": {"mode": "refuse"}})
        self.assertEqual(w.run(["leaflet"], allow_major="true"), 0, w.proc.stderr)
        self.assertEqual(w.lock("leaflet"), w.before["leaflet"])
        self.assertEqual(len(w.lock_calls()), 1)
        rows = w.rows("unfixed.tsv")
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0][:3], ["leaflet", "pillow", "12.3.0"])
        self.assertEqual(rows[0][5:], ["13.0.0", "outside constraints"])
        self.assertEqual(len(rows[0][3].split(",")), 28)
        self.assertEqual(w.read("changed.txt"), "false\n")
        self.assertEqual(w.read("unfixed.txt"), "true\n")
        issue = w.read("issue-body.md")
        self.assertTrue(issue.startswith("<!-- python-vuln-fix:unfixed -->\n"))
        self.assertIn("| project | package installed | ids | max severity | fixed | reason |", issue)
        self.assertIn("unsatisfiable", issue)
        self.assertIn("~~~", issue)

    def test_no_fixed_version(self):
        rep = report(("certifi", "2024.2.2", [adv("PYSEC-1", None, sev="5.0")]))
        w = Workspace(self, {"store": STORE()}, osv={"store": [rep]})
        self.assertEqual(w.run(["store"]), 0)
        self.assertEqual(w.lock_calls(), [])
        self.assertEqual(w.rows("unfixed.tsv"), [["store", "certifi", "2024.2.2", "PYSEC-1", "5", "", "no-fixed-version"]])

    def test_advisory_not_covering_installed_version_has_no_fix(self):
        rep = report(("certifi", "2024.2.2", [adv("PYSEC-1", "2023.1.1")]))
        w = Workspace(self, {"store": STORE()}, osv={"store": [rep]})
        w.run(["store"])
        self.assertEqual(w.lock_calls(), [])
        self.assertEqual(w.rows("unfixed.tsv")[0][-1], "no-fixed-version")

    def test_git_ranges_are_ignored(self):
        rep = report(("certifi", "2024.2.2", [adv("PYSEC-1", "2025.1.1")]))
        vuln = rep["results"][0]["packages"][0]["vulnerabilities"][0]
        vuln["affected"][0]["ranges"].insert(0, {"type": "GIT", "events": [{"introduced": "0"}, {"fixed": "bd8153872e9c6fc98f4023df9c2deaffea2fa463"}]})
        w = Workspace(self, {"store": STORE()}, osv={"store": [rep, None]})
        w.run(["store"])
        self.assertEqual(w.lock_calls()[0]["args"][-1], "certifi==2025.1.1")

    def test_excluded_dependency_never_reaches_uv(self):
        rep = report(("torchvision", "0.29.1+cpu", [adv("GHSA-t", "0.29.2")]))
        w = Workspace(self, {"leaflet": LEAFLET()}, osv={"leaflet": [rep]})
        self.assertEqual(w.run(["leaflet"], exclude="torch*\nultralytics*\nnumpy"), 0)
        self.assertEqual(w.lock_calls(), [])
        self.assertEqual(w.rows("unfixed.tsv")[0][-1], "excluded (pattern 'torch*')")

    def test_exclusion_is_an_exact_glob_match(self):
        rep = report(("numpy-financial", "1.0.0", [adv("GHSA-n", "1.0.1")]))
        w = Workspace(self, {"leaflet": LEAFLET()}, osv={"leaflet": [rep, None]})
        self.assertEqual(w.run(["leaflet"], exclude="numpy, torch*"), 0)
        self.assertEqual(len(w.lock_calls()), 1)
        self.assertEqual(w.rows("fixed.tsv")[0][:4], ["leaflet", "numpy-financial", "1.0.0", "1.0.1"])

    def test_major_never_reaches_uv(self):
        w = Workspace(self, {"leaflet": LEAFLET()}, osv={"leaflet": [PILLOW]})
        self.assertEqual(w.run(["leaflet"]), 0)
        self.assertEqual(w.lock_calls(), [])
        rows = w.rows("unfixed.tsv")
        self.assertEqual(rows[0][-1], "major 12.3.0 -> 13.0.0")
        self.assertEqual(w.lock("leaflet"), w.before["leaflet"])

    def test_allow_major_lets_it_through(self):
        w = Workspace(self, {"leaflet": LEAFLET()}, osv={"leaflet": [PILLOW, None]})
        self.assertEqual(w.run(["leaflet"], allow_major="true"), 0, w.proc.stderr)
        self.assertEqual(w.lock_calls()[0]["args"][-1], "pillow==13.0.0")
        self.assertIn('name = "pillow"\nversion = "13.0.0"', w.lock("leaflet"))
        self.assertEqual(w.rows("fixed.tsv")[0][:4], ["leaflet", "pillow", "12.3.0", "13.0.0"])

    def test_calver_is_not_a_major(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]})
        w.run(["store"], allow_major="false")
        self.assertEqual(len(w.lock_calls()), 1)
        self.assertEqual(w.read("unfixed.tsv"), "")

    def test_resolver_picks_another_version(self):
        rep = report(("certifi", "2024.2.2", [adv("PYSEC-1", "2025.1.1")]))
        w = Workspace(self, {"store": STORE()}, osv={"store": [rep]}, uv={"certifi": {"version": "2024.9.9"}})
        self.assertEqual(w.run(["store"]), 0)
        self.assertEqual(w.lock("store"), w.before["store"])
        self.assertEqual(w.rows("unfixed.tsv")[0][-1], "resolver chose 2024.9.9")
        self.assertEqual(w.read("changed.txt"), "false\n")

    def test_cascade_into_excluded_is_restored(self):
        rep = report(("certifi", "2026.10.19", [adv("PYSEC-1", "2027.1.1")]))
        w = Workspace(self, {"leaflet": LEAFLET()}, osv={"leaflet": [rep]}, uv={"certifi": {"also": {"torch": "2.15.0+cpu"}}})
        self.assertEqual(w.run(["leaflet"], exclude="torch*"), 0)
        self.assertEqual(w.lock("leaflet"), w.before["leaflet"])
        self.assertTrue(w.rows("unfixed.tsv")[0][-1].startswith("cascade into torch (excluded"), w.rows("unfixed.tsv"))

    def test_cascade_into_major_is_restored(self):
        rep = report(("certifi", "2024.2.2", [adv("PYSEC-1", "2025.1.1")]))
        w = Workspace(self, {"store": STORE()}, osv={"store": [rep]}, uv={"certifi": {"also": {"idna": "4.0"}}})
        w.run(["store"])
        self.assertEqual(w.lock("store"), w.before["store"])
        self.assertEqual(w.rows("unfixed.tsv")[0][-1], "cascade into idna (major 3.7 -> 4.0)")

    def test_benign_cascade_is_kept_and_listed(self):
        rep = report(("certifi", "2024.2.2", [adv("PYSEC-1", "2025.1.1")]))
        w = Workspace(self, {"store": STORE()}, osv={"store": [rep, None]}, uv={"certifi": {"also": {"idna": "3.8"}}})
        self.assertEqual(w.run(["store"]), 0, w.proc.stderr)
        self.assertIn('name = "idna"\nversion = "3.8"', w.lock("store"))
        self.assertEqual(w.rows("cascade.tsv"), [["store", "certifi", "idna", "3.7", "3.8"]])
        self.assertIn("`idna` `3.7` -> `3.8` (pulled by `certifi`)", w.read("pr-body.md"))

    def test_rescan_still_reporting_restores_the_project(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, CERTIFI]})
        self.assertEqual(w.run(["store"]), 0)
        self.assertEqual(w.lock("store"), w.before["store"])
        self.assertEqual(w.read("changed.txt"), "false\n")
        self.assertEqual(w.read("fixed.tsv"), "")
        self.assertEqual(w.read("aborted.txt"), "true\n")
        self.assertEqual(w.read("unfixed.txt"), "true\n")
        abort = w.rows("abort.tsv")
        self.assertEqual(abort[0][0], "store")
        self.assertTrue(abort[0][1].startswith("still reported GHSA-248v-346w-9cwc"), abort)
        self.assertTrue(w.rows("unfixed.tsv")[0][-1].startswith("aborted: still reported"))
        self.assertIn("Automatic fix aborted", w.read("issue-body.md"))

    def test_uv_error_that_is_not_a_refusal_is_hard(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI]}, uv={"certifi": {"mode": "neterr"}})
        self.assertEqual(w.run(["store"]), 4)
        self.assertEqual(w.lock("store"), w.before["store"])
        self.assertIn("uv lock failed", w.proc.stderr)

    def test_scanner_error_is_hard(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": ["error"]})
        self.assertEqual(w.run(["store"]), 2)
        self.assertEqual(w.lock_calls(), [])

    def test_hard_error_restores_every_project(self):
        w = Workspace(self, {"leaflet": LEAFLET(), "store": STORE()},
                      osv={"leaflet": [report(("numpy-financial", "1.0.0", [adv("GHSA-n", "1.0.1")])), None], "store": [CERTIFI]},
                      uv={"certifi": {"mode": "neterr"}})
        self.assertEqual(w.run(["leaflet", "store"]), 4)
        self.assertEqual(w.lock("leaflet"), w.before["leaflet"])
        self.assertEqual(w.lock("store"), w.before["store"])

    def test_projects_are_isolated(self):
        w = Workspace(self, {"leaflet": LEAFLET(), "store": STORE()},
                      osv={"leaflet": [PILLOW, None], "store": [CERTIFI, None]}, uv={"pillow": {"mode": "refuse"}})
        self.assertEqual(w.run(["store", "leaflet"], allow_major="true"), 0, w.proc.stderr)
        self.assertEqual(w.lock("leaflet"), w.before["leaflet"])
        self.assertNotEqual(w.lock("store"), w.before["store"])
        self.assertEqual(w.read("locks.txt"), "store/uv.lock\n")
        self.assertEqual([r[:2] for r in w.rows("fixed.tsv")], [["store", "certifi"]])
        self.assertEqual([r[:2] for r in w.rows("unfixed.tsv")], [["leaflet", "pillow"]])
        self.assertEqual(w.read("changed.txt"), "true\n")
        self.assertEqual(w.read("unfixed.txt"), "true\n")

    def test_hostile_values_never_reach_an_output(self):
        rep = report(("certifi", "2024.2.2", [dict(ids=["GHSA-$(touch pwned)", "GHSA-good-1234"], fixed="2025.1.1")]),
                     ("evil`name`", "1.0", [adv("GHSA-evil-0001", "2.0")]),
                     ("idna", "3.7;rm", [adv("GHSA-evil-0002", "3.9")]),
                     ("idna", "3.7", [adv("`id`", "3.9")]))
        w = Workspace(self, {"store": STORE()}, osv={"store": [rep, None]})
        self.assertEqual(w.run(["store"]), 0, w.proc.stderr)
        self.assertFalse(os.path.exists(os.path.join(w.root, "pwned")))
        self.assertFalse(os.path.exists(os.path.join(w.root, "store", "pwned")))
        self.assertEqual(w.rows("fixed.tsv")[0][4], "GHSA-good-1234")
        self.assertIn("dropped", w.proc.stderr)
        for name in os.listdir(w.out):
            text = w.read(name)
            for bad in ("$(", "`id`", "`name`", "touch", "evil", "pwned", ";rm"):
                self.assertNotIn(bad, text, f"{bad!r} leaked into {name}")

    def test_uv_output_is_sanitised_in_the_issue(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI]}, uv={"certifi": {"mode": "refuse"}})
        w.run(["store"])
        issue = w.read("issue-body.md")
        self.assertNotIn("\x1b", issue)
        self.assertEqual(issue.count("~~~"), 2)

    def test_hash_ignores_input_order(self):
        def go(order):
            w = Workspace(self, {"leaflet": LEAFLET(), "store": STORE()},
                          osv={"leaflet": [report(("numpy-financial", "1.0.0", [adv("GHSA-n", "1.0.1")])), None],
                               "store": [CERTIFI, None]})
            self.assertEqual(w.run(order), 0, w.proc.stderr)
            return w.read("hash.txt"), w.read("title.txt"), w.read("locks.txt")

        self.assertEqual(go(["store", "leaflet"]), go(["leaflet", "store", "store"]))

    def test_hash_changes_with_the_advisory_set(self):
        a = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]})
        a.run(["store"])
        b = Workspace(self, {"store": STORE()}, osv={"store": [report(("certifi", "2024.2.2", [adv("GHSA-other", "2025.1.1")])), None]})
        b.run(["store"])
        self.assertNotEqual(a.read("hash.txt"), b.read("hash.txt"))

    def test_long_title_falls_back_to_a_count(self):
        advs = [adv(f"GHSA-aaaa-bbbb-{i:04d}", "2025.1.1") for i in range(6)]
        w = Workspace(self, {"store": STORE()}, osv={"store": [report(("certifi", "2024.2.2", advs)), None]})
        w.run(["store"])
        self.assertEqual(w.read("title.txt"), "fix(deps): patch 6 Python advisories\n")

    def test_title_is_at_most_72_chars_when_it_lists_ids(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]})
        w.run(["store"])
        self.assertLessEqual(len(w.read("title.txt").strip()), 72)


class UvPinTest(unittest.TestCase):
    def test_mismatch_exits_2_before_any_scan(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]}, pin="0.13.5")
        self.assertEqual(w.run(["store"]), 2)
        self.assertIn(
            '::error file=.uv-version::uv 0.12.18 does not match .uv-version 0.13.5; a different uv rewrites '
            'the lock format. Rerun with --uv "uvx uv@0.13.5".', w.proc.stdout)
        self.assertEqual(w.osv_calls(), [])
        self.assertEqual(w.lock_calls(), [])
        self.assertEqual(w.lock("store"), w.before["store"])

    def test_leading_v_in_pin_is_ignored(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]}, pin="v0.12.18")
        self.assertEqual(w.run(["store"]), 0)

    def test_no_pin_file_means_no_check(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]}, pin=None)
        self.assertEqual(w.run(["store"]), 0)

    def test_command_string_is_split_with_shlex(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI, None]})
        self.assertEqual(w.run(["store"], uv=f"{shlex.quote(sys.executable)} {shlex.quote(w.uv_bin)}"), 0)
        self.assertTrue(w.lock_calls())
        self.assertEqual(w.lock_calls()[0]["args"][0], "lock")

    def test_bad_command_string_exits_2(self):
        w = Workspace(self, {"store": STORE()})
        self.assertEqual(w.run(["store"], uv="'unterminated"), 2)


class InputTest(unittest.TestCase):
    def check(self, projects_arg, locks=None, exclude=""):
        w = Workspace(self, locks if locks is not None else {"store": STORE()})
        cmd = [sys.executable, SCRIPT, "run", "--projects", projects_arg, "--out", w.out, "--root", w.root,
               "--osv-scanner", w.osv_bin, "--uv", w.uv_bin, "--exclude", exclude]
        env = dict(os.environ, FAKE_CFG=w.cfg, FAKE_STATE=w.state, PYTHONDONTWRITEBYTECODE="1")
        proc = subprocess.run(cmd, capture_output=True, text=True, env=env)
        return proc.returncode, w

    def test_bad_projects(self):
        for arg in ("store", '"store"', "[]", "[1]", '["/abs"]', '["../x"]', '["a/../b"]', '["st ore"]', '["st;ore"]', '["nope"]'):
            with self.subTest(arg=arg):
                rc, w = self.check(arg)
                self.assertEqual(rc, 2)
                self.assertEqual(w.osv_calls(), [])

    def test_good_projects(self):
        self.assertEqual(self.check('["./store/"]')[0], 0)

    def test_bad_exclude(self):
        self.assertEqual(self.check('["store"]', exclude="a;b")[0], 2)

    def test_missing_binary(self):
        w = Workspace(self, {"store": STORE()}, osv={"store": [CERTIFI]})
        cmd = [sys.executable, SCRIPT, "run", "--projects", '["store"]', "--out", w.out, "--root", w.root,
               "--osv-scanner", os.path.join(w.tmp, "missing"), "--uv", w.uv_bin]
        self.assertEqual(subprocess.run(cmd, capture_output=True, text=True).returncode, 2)


if __name__ == "__main__":
    unittest.main()
