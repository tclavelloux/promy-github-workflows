#!/usr/bin/env bash
# Applies the repo settings the fleet's CI wiring depends on to one promy-* repo,
# then verifies them with check-repo-settings.sh.
#
# Run by hand from a developer machine, with the keyring token:
#   GH_TOKEN= scripts/apply-repo-settings.sh <owner/repo> [reference owner/repo]
#
# - reference (default tclavelloux/promy-event-bus): the repo whose `protect-main`
#   ruleset is copied. Pick one with the same CI job set: promy-event-bus for a Go
#   library, promy-template-go for a Go service.
# - App secrets: set APP_CLIENT_ID and APP_PRIVATE_KEY_FILE (path to the
#   promy-fleet-bot .pem, or <(pbpaste) to read it from the clipboard) to store
#   both as Actions and Dependabot secrets. Unset, the step is skipped and reported.
#   From the clipboard, clear it afterwards:
#     GH_TOKEN= APP_CLIENT_ID=<id> APP_PRIVATE_KEY_FILE=<(pbpaste) \
#       scripts/apply-repo-settings.sh <owner/repo>; pbcopy </dev/null
# - The App installation cannot be scripted with a user token; the script prints
#   the manual step.
#
# Idempotent: rerunning converges to the same state.
# Exit 0: applied and verified. Exit 1: a verification failed. Exit 2: usage or gh error.
# GH=<path> overrides the gh binary (tests stub it).
set -euo pipefail

GH=${GH:-gh}
HERE=$(cd "$(dirname "$0")" && pwd)

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: GH_TOKEN= $0 <owner/repo> [reference owner/repo]" >&2
  exit 2
fi
REPO=$1
REF=${2:-tclavelloux/promy-event-bus}
RULESET=protect-main

ghx() {
  if ! "$GH" "$@"; then
    echo "apply-repo-settings: gh $* failed" >&2
    exit 2
  fi
}

# Squash-only with the PR title as the commit: release-please parses it.
# Secret scanning and push protection stop a leaked key before it lands.
echo "apply $REPO merge and security settings"
ghx api -X PATCH "repos/$REPO" --silent --input - <<'JSON'
{
  "allow_squash_merge": true,
  "allow_merge_commit": false,
  "allow_rebase_merge": false,
  "delete_branch_on_merge": true,
  "squash_merge_commit_title": "PR_TITLE",
  "squash_merge_commit_message": "PR_BODY",
  "has_wiki": false,
  "security_and_analysis": {
    "secret_scanning": {"status": "enabled"},
    "secret_scanning_push_protection": {"status": "enabled"}
  }
}
JSON

# Read-only default token; Actions may not approve PRs (the App opens fleet bot PRs).
echo "apply $REPO actions workflow permissions"
ghx api -X PUT "repos/$REPO/actions/permissions/workflow" --silent \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false

echo "apply $REPO vulnerability alerts and automated security fixes"
ghx api -X PUT "repos/$REPO/vulnerability-alerts" --silent
ghx api -X PUT "repos/$REPO/automated-security-fixes" --silent

# Copy the reference ruleset verbatim (required checks included), create or update.
echo "apply $REPO $RULESET ruleset from $REF"
ref_id=$("$GH" api "repos/$REF/rulesets" --jq ".[] | select(.name == \"$RULESET\") | .id") || exit 2
if [ -z "$ref_id" ]; then
  echo "apply-repo-settings: $REF has no $RULESET ruleset" >&2
  exit 2
fi
body=$("$GH" api "repos/$REF/rulesets/$ref_id" \
  --jq '{name, target, enforcement, conditions, rules, bypass_actors}') || exit 2
own_id=$("$GH" api "repos/$REPO/rulesets" --jq ".[] | select(.name == \"$RULESET\") | .id") || exit 2
if [ -n "$own_id" ]; then
  printf '%s' "$body" | ghx api -X PUT "repos/$REPO/rulesets/$own_id" --silent --input -
else
  printf '%s' "$body" | ghx api -X POST "repos/$REPO/rulesets" --silent --input -
fi

# Dependabot-triggered runs do not see Actions secrets: store both sets.
if [ -n "${APP_CLIENT_ID:-}" ] && [ -n "${APP_PRIVATE_KEY_FILE:-}" ]; then
  # Read once: a process substitution such as <(pbpaste) can only be read once.
  if [ ! -r "$APP_PRIVATE_KEY_FILE" ] || ! key=$(cat "$APP_PRIVATE_KEY_FILE") || [ -z "$key" ]; then
    echo "apply-repo-settings: cannot read APP_PRIVATE_KEY_FILE, or it is empty" >&2
    exit 2
  fi
  for app in actions dependabot; do
    echo "apply $REPO $app secrets APP_CLIENT_ID APP_PRIVATE_KEY"
    printf '%s' "$APP_CLIENT_ID" | ghx secret set APP_CLIENT_ID --app "$app" -R "$REPO"
    printf '%s\n' "$key" | ghx secret set APP_PRIVATE_KEY --app "$app" -R "$REPO"
  done
  unset key
else
  echo "SKIP $REPO app secrets: export APP_CLIENT_ID and APP_PRIVATE_KEY_FILE, then rerun"
fi

echo "TODO $REPO promy-fleet-bot installation: https://github.com/settings/installations" \
  "-> promy-fleet-bot -> Configure -> Repository access: add $REPO (nothing to do on 'All repositories')"

exec "$HERE/check-repo-settings.sh" "$REPO"
