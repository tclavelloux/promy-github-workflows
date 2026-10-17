#!/usr/bin/env bash
# Patch called govulncheck advisories: module advisories by bumping to the fixed
# versions, stdlib advisories by bumping the go directive to a same-minor patch
# release (opt out with GO_VULN_FIX_GO_PATCH=false).
# Local twin of the inlined step in .github/workflows/go-vuln-fix.yml: the
# block between the `go-vuln-fix body` markers must stay byte-identical in both.
# Pure local logic, no GitHub API calls.
#
# Usage: [GO_VULN_FIX_GO_PATCH=false] scripts/go-vuln-fix.sh <govulncheck-binary> <outdir>   (from module root)
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
# Env: GO_VULN_FIX_GO_PATCH=false disables the same-minor go patch bump (default true).
# Return codes: 0 scan completed (fixed or nothing to fix), 2 scanner error,
# 3 fix aborted (go.mod/go.sum restored, reason in <outdir>/abort.txt).
# Stdlib advisories fixed in a newer patch of the go directive's minor are fixed
# by bumping the directive; everything else (other minors, no fixed version, a
# toolchain directive, go.work, patch- or digest-pinned Dockerfiles, other
# version pins) stays in stdlib.tsv for a human, reason in stdlib-refused.txt.
go_vuln_fix() {
  local bin="$1" out="$2"
  local called='.[] | select(.finding) | .finding | select(.trace[0].function != null)'
  local not_std='select(.trace[0].module != "stdlib" and .trace[0].module != "toolchain")'
  local go0 tc0 go1 tc1 mod goexp gotc target refuse minor0 id v other

  mkdir -p "$out"
  rm -f "$out/abort.txt"
  : > "$out/module.tsv"; : > "$out/stdlib.tsv"; : > "$out/unfixable.tsv"
  : > "$out/ids.txt"; : > "$out/targets.txt"
  : > "$out/stdlib-fixed.tsv"; : > "$out/stdlib-refused.txt"; : > "$out/go-bump.txt"
  : > "$out/stdlib-claimed.tsv"
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

  # Stdlib target: highest fixed version in the same minor as the go directive.
  target=""
  gotc=local
  refuse=""
  if [ -s "$out/stdlib.tsv" ]; then
    if [ "${GO_VULN_FIX_GO_PATCH:-true}" = "false" ]; then
      refuse="the go patch bump is disabled (go-patch-bump: false)"
    elif ! [[ "$go0" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      refuse="the go directive (${go0:-none}) has no patch component"
    else
      minor0="${go0%.*}"
      while IFS=$'\t' read -r id v; do
        v="${v#v}"; v="${v#go}"
        case "$v" in
          "$minor0".*)
            if [ "$v" != "$go0" ] && [ "$(printf '%s\n%s\n' "$go0" "$v" | sort -V | tail -n 1)" = "$v" ]; then
              printf '%s\t%s\n' "$id" "$v" >> "$out/stdlib-claimed.tsv"
            fi ;;
        esac
      done < "$out/stdlib.tsv"
      if [ -s "$out/stdlib-claimed.tsv" ]; then
        if [ -n "$tc0" ]; then
          refuse="go.mod has a toolchain directive ($tc0)"
        elif [ -e go.work ]; then
          refuse="a go.work file exists"
        elif [ -n "$(git ls-files -z -- '*Dockerfile*' | xargs -0 -r grep -El 'golang:[0-9]+\.[0-9]+\.[0-9]+|golang:[^[:space:]]*@sha256:' || true)" ]; then
          refuse="a Dockerfile pins a golang patch version or digest, so its image would not carry the new patch"
        elif [ -e .go-version ] || grep -Eq '^[[:space:]]*(golang|go)[[:space:]]' .tool-versions 2> /dev/null; then
          refuse="a .go-version or .tool-versions file pins Go"
        else
          target=$(cut -f2 "$out/stdlib-claimed.tsv" | sort -V | tail -n 1)
        fi
      fi
    fi
    if [ -n "$refuse" ]; then
      echo "$refuse" > "$out/stdlib-refused.txt"
      : > "$out/stdlib-claimed.tsv"
    else
      other=$(awk -F'\t' 'FILENAME == ARGV[1] { c[$1] = 1; next } !($1 in c) && $2 != "" { print $2 }' "$out/stdlib-claimed.tsv" "$out/stdlib.tsv" | sed 's/^v//; s/^go//' | sort -V | tail -n 1)
      if [ -n "$other" ]; then
        echo "fixed in go$other, a different minor from go$go0; minor bumps stay human" >> "$out/stdlib-refused.txt"
      fi
      if awk -F'\t' 'FILENAME == ARGV[1] { c[$1] = 1; next } !($1 in c) && $2 == "" { found = 1 } END { exit !found }' "$out/stdlib-claimed.tsv" "$out/stdlib.tsv"; then
        echo "some advisories have no fixed version" >> "$out/stdlib-refused.txt"
      fi
    fi
  fi

  if [ -s "$out/module.tsv" ] || [ -n "$target" ]; then
    goexp="$go0"
    if [ -n "$target" ]; then
      go mod edit -go="$target"
      gotc="go$target"
      goexp="$target"
    fi

    if [ -s "$out/module.tsv" ]; then
      # One target per module at the highest fixed_version any advisory demands.
      cut -f2 "$out/module.tsv" | sort -u | while read -r mod; do
        printf '%s@%s\n' "$mod" "$(awk -F'\t' -v m="$mod" '$2 == m { print $3 }' "$out/module.tsv" | sort -V | tail -n 1)"
      done > "$out/targets.txt"

      # Word-split targets into args; module paths and versions contain no spaces.
      # shellcheck disable=SC2046
      if ! GOTOOLCHAIN="$gotc" go get $(cat "$out/targets.txt") > "$out/go-get.log" 2>&1; then
        git checkout -- go.mod go.sum
        { echo "go get failed:"; tail -n 20 "$out/go-get.log"; } > "$out/abort.txt"
        return 3
      fi
    fi
    if ! GOTOOLCHAIN="$gotc" go mod tidy >> "$out/go-get.log" 2>&1; then
      git checkout -- go.mod go.sum
      { echo "go mod tidy failed:"; tail -n 20 "$out/go-get.log"; } > "$out/abort.txt"
      return 3
    fi

    go1=$(GOTOOLCHAIN="$gotc" go mod edit -json | jq -r '.Go // ""')
    tc1=$(GOTOOLCHAIN="$gotc" go mod edit -json | jq -r '.Toolchain // ""')
    if [ "$goexp" != "$go1" ] || [ "$tc0" != "$tc1" ]; then
      git checkout -- go.mod go.sum
      echo "fix would change the go directive (${go0:-none} -> ${goexp:-none} expected, ${go1:-none} resulting) or toolchain directive (${tc0:-none} -> ${tc1:-none}); left to a human" > "$out/abort.txt"
      return 3
    fi

    if ! GOTOOLCHAIN="$gotc" go build ./... > "$out/build.log" 2>&1; then
      git checkout -- go.mod go.sum
      { echo "go build failed after the bump:"; tail -n 20 "$out/build.log"; } > "$out/abort.txt"
      return 3
    fi

    if ! GOTOOLCHAIN="$gotc" "$bin" -format json ./... > "$out/scan2.json"; then
      git checkout -- go.mod go.sum
      echo "govulncheck failed on the verification rescan" > "$out/abort.txt"
      return 3
    fi
    # Every claimed ID must be gone, stdlib ones included: if govulncheck does
    # not see the new toolchain, the run aborts instead of claiming a fix.
    { cat "$out/ids.txt"; cut -f1 "$out/stdlib-claimed.tsv"; } | sort -u > "$out/ids-claimed.txt"
    jq -r -s "$called | .osv" "$out/scan2.json" | sort -u > "$out/remaining.txt"
    if [ -n "$(grep -Fxf "$out/ids-claimed.txt" "$out/remaining.txt" || true)" ]; then
      git checkout -- go.mod go.sum
      echo "advisories still called after the bump: $(grep -Fxf "$out/ids-claimed.txt" "$out/remaining.txt" | paste -sd, -)" > "$out/abort.txt"
      return 3
    fi

    if [ -n "$target" ]; then
      cp "$out/ids-claimed.txt" "$out/ids.txt"
      cp "$out/stdlib-claimed.tsv" "$out/stdlib-fixed.tsv"
      awk -F'\t' 'FILENAME == ARGV[1] { c[$1] = 1; next } !($1 in c)' "$out/stdlib-claimed.tsv" "$out/stdlib.tsv" > "$out/stdlib-left.tsv"
      mv "$out/stdlib-left.tsv" "$out/stdlib.tsv"
      echo "$go0 $target" > "$out/go-bump.txt"
    fi
  fi

  # Dedup key: same advisory set -> same branch -> no duplicate PR.
  if command -v sha256sum > /dev/null 2>&1; then
    sha256sum "$out/ids.txt" | cut -c1-12 > "$out/hash.txt"
  else
    shasum -a 256 "$out/ids.txt" | cut -c1-12 > "$out/hash.txt"
  fi
  if git diff --quiet -- go.mod go.sum; then echo false > "$out/changed.txt"; else echo true > "$out/changed.txt"; fi

  echo "module advisories fixed: $(cut -f1 "$out/module.tsv" | sort -u | wc -l | tr -d ' ') (changed=$(cat "$out/changed.txt"))"
  echo "stdlib advisories fixed by go bump: $(cut -f1 "$out/stdlib-fixed.tsv" | sort -u | wc -l | tr -d ' ')"
  echo "stdlib advisories (human): $(cut -f1 "$out/stdlib.tsv" | sort -u | wc -l | tr -d ' ')"
  echo "unfixable module advisories (human): $(cut -f1 "$out/unfixable.tsv" | sort -u | wc -l | tr -d ' ')"
  return 0
}
# <<< go-vuln-fix body

# Not wrapped in `|| ...`: that would disable errexit inside the function.
# A non-zero return (2 or 3) ends the script with that exit code.
go_vuln_fix "$1" "$2"
