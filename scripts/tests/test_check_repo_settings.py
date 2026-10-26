"""Tests for scripts/check-repo-settings.sh, driven through a fake gh binary."""

import os
import stat
import subprocess
import tempfile
import unittest

SCRIPT = os.path.join(os.path.dirname(__file__), "..", "check-repo-settings.sh")

# Healthy answers for every endpoint; `repo list` output and status come from env.
FAKE_GH = r"""#!/usr/bin/env bash
case "$1 $2" in
  "repo list")
    printf '%s' "$FAKE_LIST"
    exit "${FAKE_LIST_STATUS:-0}" ;;
esac
endpoint="$2"
echo "$endpoint" >> "$FAKE_CALLS"
case "$endpoint" in
  */actions/permissions/workflow)
    case "$4" in
      .default_workflow_permissions) echo read ;;
      *) echo false ;;
    esac ;;
  */vulnerability-alerts) exit 0 ;;
  */automated-security-fixes) echo true ;;
  *)
    case "$4" in
      .allow_squash_merge) echo true ;;
      *) echo false ;;
    esac ;;
esac
"""


def run(args=(), listing="", list_status=0):
    with tempfile.TemporaryDirectory() as d:
        gh = os.path.join(d, "gh")
        with open(gh, "w", encoding="utf-8") as fh:
            fh.write(FAKE_GH)
        os.chmod(gh, os.stat(gh).st_mode | stat.S_IXUSR)
        calls = os.path.join(d, "calls")
        env = dict(
            os.environ,
            GH=gh,
            FAKE_LIST=listing,
            FAKE_LIST_STATUS=str(list_status),
            FAKE_CALLS=calls,
        )
        p = subprocess.run(
            ["bash", SCRIPT, *args], env=env, capture_output=True, text=True, check=False
        )
        seen = open(calls, encoding="utf-8").read() if os.path.exists(calls) else ""
        return p.returncode, p.stdout, p.stderr, seen


class CheckRepoSettings(unittest.TestCase):
    def test_no_args_uses_discovered_list(self):
        code, out, _, seen = run(listing="tclavelloux/promy-a\ntclavelloux/promy-b\n")
        self.assertEqual(code, 0)
        self.assertIn("ok   tclavelloux/promy-a", out)
        self.assertIn("ok   tclavelloux/promy-b", out)
        self.assertIn("repos/tclavelloux/promy-b/", seen)

    def test_args_override_discovery(self):
        code, out, _, _ = run(args=["tclavelloux/promy-x"], listing="tclavelloux/promy-a\n")
        self.assertEqual(code, 0)
        self.assertIn("tclavelloux/promy-x", out)
        self.assertNotIn("promy-a", out)

    def test_args_skip_listing_even_when_it_would_fail(self):
        code, _, _, _ = run(args=["tclavelloux/promy-x"], list_status=1)
        self.assertEqual(code, 0)

    def test_empty_listing_exits_2(self):
        code, out, err, _ = run(listing="")
        self.assertEqual(code, 2)
        self.assertEqual(out, "")
        self.assertIn("no promy-* repos", err)

    def test_failed_listing_exits_2(self):
        code, out, err, _ = run(listing="boom", list_status=1)
        self.assertEqual(code, 2)
        self.assertEqual(out, "")
        self.assertIn("gh repo list failed: boom", err)


if __name__ == "__main__":
    unittest.main()
