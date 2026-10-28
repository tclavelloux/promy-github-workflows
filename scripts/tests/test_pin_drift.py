"""Offline tests for scripts/pin_drift.py (fake fetcher, temp checkout)."""

import contextlib
import datetime
import io
import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import pin_drift as pd  # noqa: E402


def make_fetch(releases=None, tags=None, fail=()):
    """releases: repo -> tag_name; tags: repo -> list of full ref names."""
    releases = releases or {}
    tags = tags or {}

    def fetch(path):
        for repo in fail:
            if f"/repos/{repo}/" in path:
                raise OSError("network down")
        for repo, tag in releases.items():
            if path == f"/repos/{repo}/releases/latest":
                return {"tag_name": tag}
        for repo, refs in tags.items():
            if path.startswith(f"/repos/{repo}/git/matching-refs/tags/"):
                if "?" in path:
                    raise OSError(f"matching-refs takes no query string: {path}")
                return [{"ref": r} for r in refs]
        raise OSError(f"unexpected path {path}")

    return fetch


class Checkout:
    def __init__(self, files):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        for rel, text in files.items():
            full = os.path.join(self.root, rel)
            os.makedirs(os.path.dirname(full), exist_ok=True)
            with open(full, "w", encoding="utf-8") as fh:
                fh.write(text)

    def close(self):
        self.tmp.cleanup()


def pin(name, locs, repo="o/r", kind="release", **extra):
    return {"name": name, "upstream": {"repo": repo, "kind": kind, **extra}, "locations": locs}


class ResolveTests(unittest.TestCase):
    def setUp(self):
        self.co = Checkout({
            ".tool-version": " v1.2.3\n",
            "policy.go": 'const (\n\tFooVersion = "2.0.1" // c\n\tBarVersion = "3.0.0"\n)\n',
            "cfg.yaml": "- repo: https://x/o/r\n  rev: v4.5.6\n- repo: https://x/o/other\n  rev: v9.9.9\n",
            "twice.yaml": "rev: v1.0.0\nrev: v1.0.1\n",
            "empty": "\n",
        })
        self.addCleanup(self.co.close)

    def resolve(self, loc):
        return pd.resolve_location(loc, self.co.root)

    def test_whole_file_strips_v_and_whitespace(self):
        self.assertEqual(self.resolve({"file": ".tool-version"}), "1.2.3")

    def test_const(self):
        self.assertEqual(self.resolve({"file": "policy.go", "const": "FooVersion"}), "2.0.1")
        self.assertEqual(self.resolve({"file": "policy.go", "const": "BarVersion"}), "3.0.0")

    def test_pattern(self):
        loc = {"file": "cfg.yaml", "pattern": r"o/r\n\s*rev:\s*v?(\S+)"}
        self.assertEqual(self.resolve(loc), "4.5.6")

    def test_pattern_zero_matches_is_error(self):
        with self.assertRaisesRegex(pd.ManifestError, "matched 0 times"):
            self.resolve({"file": "cfg.yaml", "pattern": r"nope\n\s*rev:\s*v?(\S+)"})

    def test_pattern_two_matches_is_error(self):
        with self.assertRaisesRegex(pd.ManifestError, "matched 2 times"):
            self.resolve({"file": "twice.yaml", "pattern": r"rev:\s*v(\S+)"})

    def test_pattern_needs_one_group(self):
        with self.assertRaisesRegex(pd.ManifestError, "capture group"):
            self.resolve({"file": "cfg.yaml", "pattern": r"rev:\s*v\S+"})

    def test_missing_const_and_file_and_empty(self):
        with self.assertRaises(pd.ManifestError):
            self.resolve({"file": "policy.go", "const": "NopeVersion"})
        with self.assertRaises(pd.ManifestError):
            self.resolve({"file": "missing"})
        with self.assertRaises(pd.ManifestError):
            self.resolve({"file": "empty"})


class LatestTests(unittest.TestCase):
    def test_release_strips_v(self):
        up = {"repo": "o/r", "kind": "release"}
        self.assertEqual(pd.latest_version(up, make_fetch(releases={"o/r": "v1.2.3"})), "1.2.3")

    def test_tag_semver_max_numeric_ignores_prerelease(self):
        refs = ["refs/tags/v1.9.0", "refs/tags/v1.10.0", "refs/tags/v2.0.0-rc.1", "refs/tags/v1", "refs/tags/vnext"]
        up = {"repo": "o/r", "kind": "tag", "prefix": "v"}
        self.assertEqual(pd.latest_version(up, make_fetch(tags={"o/r": refs})), "1.10.0")

    def test_tag_prefix_with_slash(self):
        refs = ["refs/tags/hooks/v1", "refs/tags/hooks/v1.5.1", "refs/tags/hooks/v1.6.0"]
        up = {"repo": "o/r", "kind": "tag", "prefix": "hooks/v"}
        self.assertEqual(pd.latest_version(up, make_fetch(tags={"o/r": refs})), "1.6.0")

    def test_tag_many_refs_single_request(self):
        refs = [f"refs/tags/v0.{i}.0" for i in range(250)] + ["refs/tags/v3.0.0"]
        up = {"repo": "o/r", "kind": "tag", "prefix": "v"}
        self.assertEqual(pd.latest_version(up, make_fetch(tags={"o/r": refs})), "3.0.0")

    def test_tag_none_matching(self):
        up = {"repo": "o/r", "kind": "tag", "prefix": "v"}
        with self.assertRaises(ValueError):
            pd.latest_version(up, make_fetch(tags={"o/r": ["refs/tags/v1.0.0-rc1"]}))


class ScanTests(unittest.TestCase):
    def setUp(self):
        self.co = Checkout({"a": "1.0.0\n", "b": "1.9.0\n", "c": "2.0.0\n"})
        self.addCleanup(self.co.close)

    def scan(self, pins, fetch):
        return pd.scan({"pins": pins}, self.co.root, fetch)

    def test_behind_current_unknown(self):
        pins = [
            pin("behind", [{"file": "a"}], repo="x/a"),
            pin("current", [{"file": "c"}], repo="x/c"),
            pin("unknown", [{"file": "b"}], repo="x/u"),
        ]
        fetch = make_fetch(releases={"x/a": "v1.1.0", "x/c": "v2.0.0"}, fail=["x/u"])
        got = {r["name"]: r for r in self.scan(pins, fetch)}
        self.assertEqual(got["behind"]["status"], "behind")
        self.assertEqual(got["current"]["status"], "current")
        self.assertEqual(got["unknown"]["status"], "unknown")
        self.assertIn("network down", got["unknown"]["error"])

    def test_disagreeing_locations_use_minimum(self):
        pins = [pin("multi", [{"file": "b"}, {"file": "a"}], repo="x/m")]
        (r,) = self.scan(pins, make_fetch(releases={"x/m": "1.0.0"}))
        self.assertEqual(r["current"], "1.0.0")
        self.assertTrue(r["disagree"])
        self.assertEqual(r["status"], "current")
        report, _, _ = pd.render([r], datetime.date(2026, 1, 1))
        self.assertIn("(1.9.0)", report)
        self.assertIn("(1.0.0)", report)
        self.assertIn("locations disagree", report)

    def test_unresolvable_location_is_manifest_error(self):
        with self.assertRaisesRegex(pd.ManifestError, "gone"):
            self.scan([pin("gone", [{"file": "nope"}])], make_fetch())

    def test_render_counts_and_marker(self):
        pins = [pin("x", [{"file": "a"}], repo="x/a"), pin("y", [{"file": "b"}], repo="x/u")]
        res = self.scan(pins, make_fetch(releases={"x/a": "2.0.0"}, fail=["x/u"]))
        report, behind, unknown = pd.render(res, datetime.date(2026, 1, 2))
        self.assertEqual((behind, unknown), (1, 1))
        self.assertTrue(report.startswith(pd.MARKER))
        self.assertIn("2026-01-02", report)
        self.assertIn(pd.DOCS_URL, report)
        self.assertIn("Lookup failures", report)


class MainTests(unittest.TestCase):
    def test_main_writes_report_and_outputs(self):
        co = Checkout({"a": "1.0.0\n"})
        self.addCleanup(co.close)
        manifest = os.path.join(co.root, "m.json")
        with open(manifest, "w", encoding="utf-8") as fh:
            json.dump({"pins": [pin("x", [{"file": "a"}], repo="x/a")]}, fh)
        out = os.path.join(co.root, "out.md")
        gh_out = os.path.join(co.root, "gh_out")
        old = os.environ.get("GITHUB_OUTPUT")
        os.environ["GITHUB_OUTPUT"] = gh_out
        self.addCleanup(lambda: os.environ.pop("GITHUB_OUTPUT", None) if old is None
                        else os.environ.__setitem__("GITHUB_OUTPUT", old))
        with contextlib.redirect_stdout(io.StringIO()):
            rc = pd.main(["--manifest", manifest, "--out", out, "--root", co.root],
                         fetch=make_fetch(releases={"x/a": "v1.0.1"}))
        self.assertEqual(rc, 0)
        with open(gh_out, encoding="utf-8") as fh:
            self.assertEqual(fh.read(), "behind=1\nunknown=0\n")
        with open(out, encoding="utf-8") as fh:
            self.assertIn(pd.MARKER, fh.read())

    def test_main_manifest_error_exits_2(self):
        co = Checkout({})
        self.addCleanup(co.close)
        manifest = os.path.join(co.root, "m.json")
        with open(manifest, "w", encoding="utf-8") as fh:
            json.dump({"pins": [pin("x", [{"file": "missing"}])]}, fh)
        with contextlib.redirect_stderr(io.StringIO()):
            rc = pd.main(["--manifest", manifest, "--out", os.path.join(co.root, "o"), "--root", co.root],
                         fetch=make_fetch())
        self.assertEqual(rc, 2)


if __name__ == "__main__":
    unittest.main()
