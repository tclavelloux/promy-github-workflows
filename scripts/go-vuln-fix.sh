#!/usr/bin/env bash
# Patch called govulncheck module advisories by bumping to the fixed versions.
# Local twin of the inlined step in .github/workflows/go-vuln-fix.yml: the
# block between the `go-vuln-fix body` markers must stay byte-identical in both.
# Pure local logic, no GitHub API calls.
#
# Usage: scripts/go-vuln-fix.sh <govulncheck-binary> <outdir>   (from module root)
# Exit: 0 scan completed, 2 scanner error, 3 fix aborted (go.mod/go.sum restored).
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <govulncheck-binary> <outdir>" >&2
  exit 64
fi

# >>> go-vuln-fix body
# Keep this block byte-identical between scripts/go-vuln-fix.sh and
# .github/workflows/go-vuln-fix.yml (governance CI diffs them).
# Usage: go_vuln_fix <govulncheck-binary> <outdir>   (run from the module root)
# Return codes: 0 scan completed (fixed or nothing to fix), 2 scanner error,
# 3 fix aborted (go.mod/go.sum restored, reason in <outdir>/abort.txt).
go_vuln_fix() {
  local bin="$1" out="$2"
  local called='.[] | select(.finding) | .finding | select(.trace[0].function != null)'
  local not_std='select(.trace[0].module != "stdlib" and .trace[0].module != "toolchain")'
  local go0 tc0 go1 tc1 mod

  mkdir -p "$out"
  rm -f "$out/abort.txt"
  : > "$out/module.tsv"; : > "$out/stdlib.tsv"; : > "$out/unfixable.tsv"
  : > "$out/ids.txt"; : > "$out/targets.txt"
  echo false > "$out/changed.txt"

  go0=$(go mod edit -json | jq -r '.Go // ""')
  tc0=$(go mod edit -json | jq -r '.Toolchain // ""')

  # govulncheck -format json exits 0 even when it finds vulnerabilities, so a
  # non-zero exit here is a scanner failure, never a finding.
  if ! "$bin" -format json ./... > "$out/scan1.json"; then
    echo "govulncheck failed" > "$out/abort.txt"
    return 2
  fi

  jq -r -s "$called | $not_std | select(.fixed_version != null)
    | [.osv, .trace[0].module, .fixed_version] | @tsv" "$out/scan1.json" | sort -u > "$out/module.tsv"
  jq -r -s "$called | select(.trace[0].module == \"stdlib\" or .trace[0].module == \"toolchain\")
    | [.osv, (.fixed_version // \"\")] | @tsv" "$out/scan1.json" | sort -u > "$out/stdlib.tsv"
  jq -r -s "$called | $not_std | select(.fixed_version == null)
    | [.osv, .trace[0].module] | @tsv" "$out/scan1.json" | sort -u > "$out/unfixable.tsv"
  cut -f1 "$out/module.tsv" | sort -u > "$out/ids.txt"

  if [ -s "$out/module.tsv" ]; then
    # One target per module at the highest fixed_version any advisory demands.
    cut -f2 "$out/module.tsv" | sort -u | while read -r mod; do
      printf '%s@%s\n' "$mod" "$(awk -F'\t' -v m="$mod" '$2 == m { print $3 }' "$out/module.tsv" | sort -V | tail -n 1)"
    done > "$out/targets.txt"

    # Word-split targets into args; module paths and versions contain no spaces.
    # shellcheck disable=SC2046
    if ! GOTOOLCHAIN=local go get $(cat "$out/targets.txt") > "$out/go-get.log" 2>&1; then
      git checkout -- go.mod go.sum
      { echo "go get failed:"; tail -n 20 "$out/go-get.log"; } > "$out/abort.txt"
      return 3
    fi
    if ! GOTOOLCHAIN=local go mod tidy >> "$out/go-get.log" 2>&1; then
      git checkout -- go.mod go.sum
      { echo "go mod tidy failed:"; tail -n 20 "$out/go-get.log"; } > "$out/abort.txt"
      return 3
    fi

    go1=$(go mod edit -json | jq -r '.Go // ""')
    tc1=$(go mod edit -json | jq -r '.Toolchain // ""')
    if [ "$go0" != "$go1" ] || [ "$tc0" != "$tc1" ]; then
      git checkout -- go.mod go.sum
      echo "fix would change the go directive (${go0:-none} -> ${go1:-none}) or toolchain directive (${tc0:-none} -> ${tc1:-none}); left to a human" > "$out/abort.txt"
      return 3
    fi

    if ! GOTOOLCHAIN=local go build ./... > "$out/build.log" 2>&1; then
      git checkout -- go.mod go.sum
      { echo "go build failed after the bump:"; tail -n 20 "$out/build.log"; } > "$out/abort.txt"
      return 3
    fi

    if ! "$bin" -format json ./... > "$out/scan2.json"; then
      git checkout -- go.mod go.sum
      echo "govulncheck failed on the verification rescan" > "$out/abort.txt"
      return 3
    fi
    jq -r -s "$called | $not_std | .osv" "$out/scan2.json" | sort -u > "$out/remaining.txt"
    if [ -n "$(grep -Fxf "$out/ids.txt" "$out/remaining.txt" || true)" ]; then
      git checkout -- go.mod go.sum
      echo "advisories still called after the bump: $(grep -Fxf "$out/ids.txt" "$out/remaining.txt" | paste -sd, -)" > "$out/abort.txt"
      return 3
    fi
  fi

  # Dedup key: same advisory set -> same branch -> no duplicate PR.
  if command -v sha256sum > /dev/null 2>&1; then
    sha256sum "$out/ids.txt" | cut -c1-12 > "$out/hash.txt"
  else
    shasum -a 256 "$out/ids.txt" | cut -c1-12 > "$out/hash.txt"
  fi
  if git diff --quiet -- go.mod go.sum; then echo false > "$out/changed.txt"; else echo true > "$out/changed.txt"; fi

  echo "module advisories fixed: $(wc -l < "$out/ids.txt" | tr -d ' ') (changed=$(cat "$out/changed.txt"))"
  echo "stdlib advisories (human): $(cut -f1 "$out/stdlib.tsv" | sort -u | wc -l | tr -d ' ')"
  echo "unfixable module advisories (human): $(cut -f1 "$out/unfixable.tsv" | sort -u | wc -l | tr -d ' ')"
  return 0
}
# <<< go-vuln-fix body

# Not wrapped in `|| ...`: that would disable errexit inside the function.
# A non-zero return (2 or 3) ends the script with that exit code.
go_vuln_fix "$1" "$2"
