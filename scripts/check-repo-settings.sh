#!/usr/bin/env bash
# Checks the repo settings the fleet's CI wiring depends on.
#
# Run by hand from a developer machine, with the keyring token:
#   GH_TOKEN= scripts/check-repo-settings.sh [owner/repo ...]
# The GH_TOKEN env var in a shell usually lacks the admin scopes these endpoints
# need. CI cannot run this: the workflow GITHUB_TOKEN cannot read Actions
# permissions, merge settings or vulnerability-alert state, and a PAT is refused
# by design (docs/dependency-automation.md).
#
# Exit 0: all ok. Exit 1: at least one FAIL. Exit 2: gh itself failed.
# GH=<path> overrides the gh binary (tests stub it).
set -euo pipefail

GH=${GH:-gh}
DEFAULT_REPOS=(
  tclavelloux/promy-template-go
  tclavelloux/promy-crm
  tclavelloux/promy-product
  tclavelloux/promy-user
  tclavelloux/promy-event-bus
  tclavelloux/promy-identifier
)
if [ "$#" -gt 0 ]; then
  REPOS=("$@")
else
  REPOS=("${DEFAULT_REPOS[@]}")
fi

failed=0

# check <repo> <name> <want> <endpoint> <jq expr>
# Fetches in the main shell (not a $(...) subshell) so a gh failure can exit 2.
check() {
  local got
  if ! got="$("$GH" api "$4" --jq "$5" 2>&1)"; then
    echo "check-repo-settings: gh api $4 failed: $got" >&2
    exit 2
  fi
  if [ "$got" = "$3" ]; then
    echo "ok   $1 $2 (got $got)"
  else
    echo "FAIL $1 $2 (got $got, want $3)"
    failed=1
  fi
}

for repo in "${REPOS[@]}"; do
  # Read default is why caller jobs need explicit permissions blocks.
  # can_approve_pull_request_reviews gates "Actions may create PRs":
  # dependabot-automerge and go-vuln-fix need it.
  perms="repos/$repo/actions/permissions/workflow"
  check "$repo" default_workflow_permissions read "$perms" .default_workflow_permissions
  check "$repo" can_approve_pull_request_reviews true "$perms" .can_approve_pull_request_reviews

  # Squash-only: the PR title is the commit release-please parses.
  check "$repo" allow_squash_merge true "repos/$repo" .allow_squash_merge
  check "$repo" allow_merge_commit false "repos/$repo" .allow_merge_commit
  check "$repo" allow_rebase_merge false "repos/$repo" .allow_rebase_merge

  # 204 when alerts are on, 404 when off: a non-zero exit is the answer here,
  # not an error.
  if "$GH" api "repos/$repo/vulnerability-alerts" >/dev/null 2>&1; then
    alerts=enabled
  else
    alerts=disabled
  fi
  if [ "$alerts" = enabled ]; then
    echo "ok   $repo vulnerability_alerts (got enabled)"
  else
    echo "FAIL $repo vulnerability_alerts (got disabled, want enabled)"
    failed=1
  fi

  check "$repo" automated_security_fixes true "repos/$repo/automated-security-fixes" .enabled
done

exit "$failed"
