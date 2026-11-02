#!/usr/bin/env bash
# Decides whether a pull_request changes only files that cannot alter build, tests,
# lint or the image: root *.md, docs/**/*.md and .release-please-manifest.json (a
# release PR is CHANGELOG.md plus the manifest). Writes `skip=true|false` to
# $GITHUB_OUTPUT; the caller's expensive steps run only when skip is not `true`.
#
# Local twin of the inlined steps in go-lint.yml, go-docker.yml, go-coverage.yml,
# python-docker.yml and templates/ci-test-job.yml: the block between the
# `pr-scope body` markers must stay byte-identical in all of them
# (internal/syncheck/prscope_test.go compares them and runs the decision table
# through bash and real git).
#
# Needs: EVENT_NAME, and a checkout with `fetch-depth: 2` so the PR merge commit
# and both its parents are present. Fails open: any anomaly sets skip=false, so a
# gate runs rather than silently passing.

# >>> pr-scope body
# Keep this block byte-identical between scripts/pr-scope.sh and every workflow
# that inlines it (governance tests compare them).
set +e
out="${GITHUB_OUTPUT:-/dev/stdout}"

# inert reports whether one path cannot affect any gate.
inert() {
  case "$1" in
    *testdata*) return 1 ;; # fixtures are test inputs, whatever their extension
    .release-please-manifest.json) return 0 ;;
    docs/*.md) return 0 ;;
    */*) return 1 ;;
    *.md) return 0 ;;
    *) return 1 ;;
  esac
}

decide() {
  [ "${EVENT_NAME:-}" = "pull_request" ] || { reason="event is ${EVENT_NAME:-unset}, not pull_request"; return 1; }
  # A merge commit has two parents: "<sha> <p1> <p2>".
  [ "$(git rev-list --parents -n 1 HEAD 2>/dev/null | wc -w | tr -d ' ')" = 3 ] ||
    { reason="HEAD is not a merge commit with both parents fetched (checkout needs fetch-depth: 2)"; return 1; }
  local list n=0 f bad=""
  list="$(mktemp)"
  git diff --name-only -z --no-renames HEAD^1 HEAD >"$list" 2>/dev/null || { rm -f "$list"; reason="git diff failed"; return 1; }
  while IFS= read -r -d '' f; do
    n=$((n + 1))
    if ! inert "$f"; then bad="$f"; break; fi
  done <"$list"
  rm -f "$list"
  # sanitised: the name lands in a workflow command
  [ -z "$bad" ] || { reason="${bad//[^[:alnum:]._\/-]/?} can affect a gate"; return 1; }
  [ "$n" -gt 0 ] || { reason="empty change set"; return 1; }
  reason="$n changed file(s), all docs or release metadata"
}

reason=""
if decide; then skip=true; else skip=false; fi
echo "skip=${skip}" >>"$out"
echo "::notice title=pr-scope::skip=${skip}: ${reason}"
# <<< pr-scope body
