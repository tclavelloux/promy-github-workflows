#!/usr/bin/env bash
# Sync GoTestCoverageVersion in policy.go to the go-test-coverage version pinned in
# go-coverage.yml (the `# vX.Y.Z` comment). Rewrites only that const line.
# Usage: coverage-pin-sync.sh <go-coverage.yml> <policy.go>
# Writes version= and changed= to $GITHUB_OUTPUT.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <go-coverage.yml> <policy.go>" >&2
  exit 2
fi
workflow=$1
policy=$2
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"

re='vladopajic/go-test-coverage@[0-9a-f]{40}[[:space:]]+#[[:space:]]*v([^[:space:]]+)'
matches=$(grep -Eo "$re" "$workflow" || true)
count=$(printf '%s' "$matches" | grep -c . || true)
if [ "$count" -ne 1 ]; then
  echo "::error::$workflow: expected exactly one 'vladopajic/go-test-coverage@<sha> # vX.Y.Z' line, found $count" >&2
  exit 1
fi
version=$(printf '%s' "$matches" | sed -E 's/.*#[[:space:]]*v//')

const_re='^([[:space:]]*GoTestCoverageVersion[[:space:]]*=[[:space:]]*)"[^"]*"'
const_count=$(grep -Ec "$const_re" "$policy" || true)
if [ "$const_count" -ne 1 ]; then
  echo "::error::$policy: expected exactly one GoTestCoverageVersion line, found $const_count" >&2
  exit 1
fi

current=$(grep -E "$const_re" "$policy" | sed -E 's/^[^"]*"([^"]*)".*/\1/')
if [ "$current" = "$version" ]; then
  {
    echo "version=$version"
    echo "changed=false"
  } >> "$GITHUB_OUTPUT"
  exit 0
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
sed -E "s/$const_re/\\1\"$version\"/" "$policy" > "$tmp"
changed_lines=$(diff "$policy" "$tmp" | grep -c '^>' || true)
if [ "$changed_lines" -ne 1 ]; then
  echo "::error::expected exactly one changed line in $policy, got $changed_lines" >&2
  exit 1
fi
cat "$tmp" > "$policy"
{
  echo "version=$version"
  echo "changed=true"
} >> "$GITHUB_OUTPUT"
