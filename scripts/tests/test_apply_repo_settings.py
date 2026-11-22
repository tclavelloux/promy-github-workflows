"""Tests for scripts/apply-repo-settings.sh, driven through a fake gh binary."""

import os
import stat
import subprocess
import tempfile
import unittest

SCRIPT = os.path.join(os.path.dirname(__file__), "..", "apply-repo-settings.sh")

# Logs every call (args, then stdin for writes) to $FAKE_CALLS. Ruleset ids come from
# env: FAKE_REF_RULESET (reference repo) and FAKE_OWN_RULESET (target repo), empty = none.
# Answers the read-backs of check-repo-settings.sh with healthy values.
FAKE_GH = r"""#!/usr/bin/env bash
echo "ARGS $*" >> "$FAKE_CALLS"
case "$*" in
  *"--input -"*|"secret set"*) echo "STDIN $(cat)" >> "$FAKE_CALLS" ;;
esac
case "$*" in
  "api repos/tclavelloux/promy-ref/rulesets --jq"*) echo "${FAKE_REF_RULESET-}" ; exit 0 ;;
  "api repos/tclavelloux/promy-new/rulesets --jq"*) echo "${FAKE_OWN_RULESET-}" ; exit 0 ;;
  "api repos/tclavelloux/promy-ref/rulesets/"*) echo '{"name":"protect-main","rules":[]}' ; exit 0 ;;
  "api -X "*) exit 0 ;;
  "secret set"*) exit 0 ;;
esac
endpoint="$2"
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
      .allow_squash_merge|.delete_branch_on_merge) echo true ;;
      *) echo false ;;
    esac ;;
esac
"""

NEW = "tclavelloux/promy-new"
REF = "tclavelloux/promy-ref"


def run(args=(NEW, REF), extra_env=None):
    with tempfile.TemporaryDirectory() as d:
        gh = os.path.join(d, "gh")
        with open(gh, "w", encoding="utf-8") as fh:
            fh.write(FAKE_GH)
        os.chmod(gh, os.stat(gh).st_mode | stat.S_IXUSR)
        calls = os.path.join(d, "calls")
        env = {k: v for k, v in os.environ.items() if not k.startswith("APP_")}
        env.update(GH=gh, FAKE_CALLS=calls, FAKE_REF_RULESET="11", FAKE_OWN_RULESET="")
        env.update(extra_env or {})
        p = subprocess.run(
            ["bash", SCRIPT, *args], env=env, capture_output=True, text=True, check=False
        )
        seen = open(calls, encoding="utf-8").read() if os.path.exists(calls) else ""
        return p.returncode, p.stdout, p.stderr, seen


class ApplyRepoSettings(unittest.TestCase):
    def test_usage_without_repo_exits_2(self):
        code, _, err, seen = run(args=())
        self.assertEqual(code, 2)
        self.assertIn("usage:", err)
        self.assertEqual(seen, "")

    def test_applies_settings_then_verifies(self):
        code, out, _, seen = run()
        self.assertEqual(code, 0, out)
        self.assertIn(f"ARGS api -X PATCH repos/{NEW} ", seen)
        self.assertIn('"allow_squash_merge": true', seen)
        self.assertIn('"secret_scanning_push_protection": {"status": "enabled"}', seen)
        self.assertIn(
            f"api -X PUT repos/{NEW}/actions/permissions/workflow --silent "
            "-f default_workflow_permissions=read -F can_approve_pull_request_reviews=false",
            seen,
        )
        self.assertIn(f"api -X PUT repos/{NEW}/vulnerability-alerts", seen)
        self.assertIn(f"api -X PUT repos/{NEW}/automated-security-fixes", seen)
        self.assertIn(f"ok   {NEW} delete_branch_on_merge (got true)", out)

    def test_ruleset_created_when_absent(self):
        code, _, _, seen = run()
        self.assertEqual(code, 0)
        self.assertIn(f"ARGS api -X POST repos/{NEW}/rulesets --silent --input -", seen)
        self.assertIn('STDIN {"name":"protect-main","rules":[]}', seen)

    def test_ruleset_updated_when_present(self):
        code, _, _, seen = run(extra_env={"FAKE_OWN_RULESET": "42"})
        self.assertEqual(code, 0)
        self.assertIn(f"ARGS api -X PUT repos/{NEW}/rulesets/42 --silent --input -", seen)
        self.assertNotIn("-X POST", seen)

    def test_reference_without_ruleset_exits_2(self):
        code, _, err, _ = run(extra_env={"FAKE_REF_RULESET": ""})
        self.assertEqual(code, 2)
        self.assertIn(f"{REF} has no protect-main ruleset", err)

    def test_secrets_skipped_without_env(self):
        code, out, _, seen = run()
        self.assertEqual(code, 0)
        self.assertIn(f"SKIP {NEW} app secrets", out)
        self.assertNotIn("secret set", seen)

    def test_secrets_set_for_actions_and_dependabot_via_stdin(self):
        with tempfile.NamedTemporaryFile("w", suffix=".pem", delete=False) as pem:
            pem.write("PEMBODY")
        try:
            code, _, _, seen = run(
                extra_env={"APP_CLIENT_ID": "Iv1.abc", "APP_PRIVATE_KEY_FILE": pem.name}
            )
        finally:
            os.unlink(pem.name)
        self.assertEqual(code, 0)
        for app in ("actions", "dependabot"):
            self.assertIn(f"ARGS secret set APP_CLIENT_ID --app {app} -R {NEW}", seen)
            self.assertIn(f"ARGS secret set APP_PRIVATE_KEY --app {app} -R {NEW}", seen)
        self.assertIn("STDIN Iv1.abc", seen)
        self.assertIn("STDIN PEMBODY", seen)
        self.assertNotIn("PEMBODY", "".join(l for l in seen.splitlines() if l.startswith("ARGS")))

    def test_unreadable_key_file_exits_2(self):
        code, _, err, _ = run(
            extra_env={"APP_CLIENT_ID": "Iv1.abc", "APP_PRIVATE_KEY_FILE": "/nonexistent.pem"}
        )
        self.assertEqual(code, 2)
        self.assertIn("cannot read APP_PRIVATE_KEY_FILE", err)


if __name__ == "__main__":
    unittest.main()
