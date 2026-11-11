#!/usr/bin/env bash
# Fails on any called govulncheck advisory except an unexpired accepted entry.
#
# Usage: scripts/go-vuln-gate.sh <govulncheck-binary> [args...]   (from the module root)
#        scripts/go-vuln-gate.sh accept-from-workflow <go-vuln.yml>
#
# Local twin of the inlined step in go-vuln.yml: the block between the
# `go-vuln-gate body` markers must stay byte-identical in both
# (internal/syncheck/go_vuln_gate_test.go compares them and runs the decision
# table through bash). hooks/go-vuln.sh calls this script with the list extracted
# from go-vuln.yml by `accept-from-workflow`.
#
# Env:
#   GO_VULN_ACCEPT        accepted list, one entry per line:
#                         <GO-ID> <expires YYYY-MM-DD> <issue URL> <reason...>
#                         Blank lines and `#` comments are ignored.
#   GO_VULN_TODAY         override today's date (UTC, YYYY-MM-DD); tests only.
#   GITHUB_STEP_SUMMARY   when set, a table of called advisories is appended.
# Exit codes: 0 clean or only accepted, 1 called advisory not accepted or acceptance
# expired, 2 scanner, list or argument error (fail closed).
#
# govulncheck cannot suppress an ID natively, and `-format json` exits 0 even with
# findings, so the gate parses the JSON itself. A finding counts as called only when
# trace[0].function is set (the same filter go-vuln-fix.yml uses).

set -euo pipefail

# >>> go-vuln-gate body
# Keep this block byte-identical between scripts/go-vuln-gate.sh and
# .github/workflows/go-vuln.yml (governance CI diffs them).
# Usage: go_vuln_gate <govulncheck-binary> [args...]   (run from the module root)
# Env: GO_VULN_ACCEPT (list), GO_VULN_TODAY (optional), GITHUB_STEP_SUMMARY (optional).
# Return codes: 0 clean or only accepted, 1 called advisory not accepted or expired,
# 2 scanner, list or argument error.
go_vuln_gate() {
  local bin="$1"
  shift
  local called='.[] | select(.finding) | .finding | select(.trace[0].function != null)'
  local today="${GO_VULN_TODAY:-$(date -u +%F)}"
  local entries="" bad=0 n=0 line id exp url reason a y m d dim json ids detail
  local status expires issue fail=0 summary="" rows="" reached=""

  command -v jq > /dev/null || {
    echo "::error title=go-vuln gate::jq is required"
    return 2
  }

  for a in "$@"; do
    case "$a" in
      -format | -format=* | --format | --format=* | -json | -json=* | --json | --json=*)
        echo "::error title=go-vuln gate::the gate owns the output format; remove '$a' from args"
        return 2
        ;;
    esac
  done

  while IFS= read -r line; do
    n=$((n + 1))
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    case "$line" in '' | '#'*) continue ;; esac
    read -r id exp url reason <<< "$line" || true
    if ! [[ "$id" =~ ^GO-[0-9]{4}-[0-9]{4,}$ ]]; then
      echo "::error title=go-vuln accept list::line $n: invalid advisory ID '$id'"
      bad=1
      continue
    fi
    if [[ "$exp" =~ ^([0-9]{4})-([0-9]{2})-([0-9]{2})$ ]]; then
      y=$((10#${BASH_REMATCH[1]}))
      m=$((10#${BASH_REMATCH[2]}))
      d=$((10#${BASH_REMATCH[3]}))
      case "$m" in
        1 | 3 | 5 | 7 | 8 | 10 | 12) dim=31 ;;
        4 | 6 | 9 | 11) dim=30 ;;
        2) if { [ $((y % 4)) -eq 0 ] && [ $((y % 100)) -ne 0 ]; } || [ $((y % 400)) -eq 0 ]; then dim=29; else dim=28; fi ;;
        *) dim=0 ;;
      esac
      if [ "$d" -lt 1 ] || [ "$d" -gt "$dim" ]; then
        echo "::error title=go-vuln accept list::line $n ($id): '$exp' is not a real date"
        bad=1
        continue
      fi
    else
      echo "::error title=go-vuln accept list::line $n ($id): expiry '$exp' is not YYYY-MM-DD"
      bad=1
      continue
    fi
    if ! [[ "$url" =~ ^https://github\.com/tclavelloux/[A-Za-z0-9._-]+/issues/[0-9]+$ ]]; then
      echo "::error title=go-vuln accept list::line $n ($id): tracking link '$url' is not a https://github.com/tclavelloux/<repo>/issues/<n> URL"
      bad=1
      continue
    fi
    if [ -z "$reason" ]; then
      echo "::error title=go-vuln accept list::line $n ($id): reason is empty"
      bad=1
      continue
    fi
    if printf '%s' "$entries" | awk -v id="$id" '$1 == id { f = 1 } END { exit !f }'; then
      echo "::error title=go-vuln accept list::line $n: duplicate entry for $id"
      bad=1
      continue
    fi
    entries="${entries}${id} ${exp} ${url}"$'\n'
  done <<< "${GO_VULN_ACCEPT:-}"
  if [ "$bad" -ne 0 ]; then
    return 2
  fi

  # govulncheck -format json exits 0 even when it finds vulnerabilities, so a
  # non-zero exit here is a scanner failure, never a finding.
  if ! json=$("$bin" -format json "$@"); then
    echo "::error title=govulncheck failed::scanner error, failing closed"
    return 2
  fi
  if ! ids=$(printf '%s\n' "$json" | jq -r -s "$called | .osv" | sort -u); then
    echo "::error title=go-vuln gate::cannot parse govulncheck JSON output"
    return 2
  fi

  while IFS= read -r id; do
    [ -n "$id" ] || continue
    reached="${reached}${id}"$'\n'
    expires="" issue=""
    read -r _ expires issue < <(printf '%s' "$entries" | awk -v id="$id" '$1 == id { print; exit }') || true
    if [ -z "${expires:-}" ]; then
      status="not accepted"
      expires="-"
      issue="-"
      fail=1
      detail=$(printf '%s\n' "$json" | jq -r -s --arg id "$id" "[$called | select(.osv == \$id)]
        | map(\"  vulnerable: \" + ((.trace[0].package // .trace[0].module) + \".\" + .trace[0].function)
          + \" reached from \" + ((.trace[1:] | map(select(.position)) | first // null)
          | if . == null then \"(no positioned caller)\" else (.position.filename + \":\" + (.position.line | tostring)) end))
        | unique | .[]" | awk 'NR <= 10')
      echo "::error title=vulnerable dependency::$id is called and not accepted: https://pkg.go.dev/vuln/$id"
      printf '%s\n' "$detail"
    elif [[ "$expires" < "$today" ]]; then
      status="expired"
      fail=1
      echo "::error title=accepted advisory expired::$id acceptance expired $expires ($issue); renew by PR or fix"
    else
      status="accepted"
      echo "::notice title=accepted advisory::$id accepted until $expires ($issue)"
    fi
    rows="${rows}| $id | $status | $expires | $issue |"$'\n'
  done <<< "$ids"

  while read -r id expires issue; do
    [ -n "$id" ] || continue
    if ! printf '%s' "$reached" | grep -qx "$id"; then
      rows="${rows}| $id | not reached | $expires | $issue |"$'\n'
    fi
  done <<< "$entries"

  if [ -n "${GITHUB_STEP_SUMMARY:-}" ] && [ -n "$rows" ]; then
    summary="### Accepted advisories"$'\n\n'"| ID | Status | Expires | Issue |"$'\n'"|---|---|---|---|"$'\n'"$rows"
    printf '%s\n' "$summary" >> "$GITHUB_STEP_SUMMARY"
  fi

  return "$fail"
}
# <<< go-vuln-gate body

# go_vuln_accept_from_workflow prints the `GO_VULN_ACCEPT: |` literal block of a
# workflow file, de-indented, exactly as a YAML parser yields it. Kept outside the
# inlined body: only the hook needs it. awk is always present; yq is not.
go_vuln_accept_from_workflow() {
  awk '
    function indent(s) { match(s, /^ */); return RLENGTH }
    !inblock && $0 ~ /^[ ]*GO_VULN_ACCEPT:[ ]*\|[ ]*$/ { key = indent($0); inblock = 1; ind = -1; next }
    inblock {
      if ($0 ~ /^[ ]*$/) { pending = pending "\n"; next }
      i = indent($0)
      if (i <= key) exit
      if (ind < 0) ind = i
      printf "%s%s\n", pending, substr($0, ind + 1)
      pending = ""
    }
  ' "$1"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  if [ "${1:-}" = "accept-from-workflow" ]; then
    go_vuln_accept_from_workflow "${2:?usage: go-vuln-gate.sh accept-from-workflow <go-vuln.yml>}"
  else
    go_vuln_gate "$@" & wait "$!"
  fi
fi
