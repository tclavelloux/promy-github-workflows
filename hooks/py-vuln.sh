#!/usr/bin/env bash
# Runs osv-scanner with the fleet-pinned version over every tracked uv.lock.
#
# Mirrors the reusable `python-vuln.yml` workflow locally so `make vuln` and
# pre-push fail on the same findings CI would: same scanner version, same
# config lookup (<project>/osv-scanner.toml, else ./osv-scanner.toml).
#
# Why warn vs block (same contract as go-vuln): advisories are time-dependent.
# A push that does not touch a lock, a pyproject.toml or an osv-scanner.toml
# must not be blocked by an advisory published today; a push that does is
# exactly where the fix (or a reasoned, expiring ignore) belongs, so it blocks.
#   - block: exit 1 on any reported vulnerability or scanner error (fail closed).
#   - warn:  always exit 0; print findings and a warning.
#
# Mode (first match wins):
#   1. PY_VULN_MODE=block|warn
#   2. PRE_COMMIT_FROM_REF and PRE_COMMIT_TO_REF both set (pre-push): block when the
#      pushed range changes uv.lock, pyproject.toml or osv-scanner.toml, else warn.
#      A failing git diff blocks.
#   3. Otherwise (manual stage, `make vuln`, --all-files): block.
#
# osv-scanner version (first match wins):
#   1. OSV_SCANNER_VERSION env
#   2. .osv-scanner-version at the root of this hook repo (the fleet-wide pin; Go
#      tests tie it to python-vuln.yml and policy.go OSVScannerVersion)
# Installed with `go install` (module checksums verified against sum.golang.org),
# cached per version in ${XDG_CACHE_HOME:-$HOME/.cache}/promy-py-vuln so the
# user's ~/go/bin is never touched. CI downloads the release binary instead and
# checks its pinned sha256; both are the same tagged source.
#
# Bypass: SKIP=py-vuln git push
set -euo pipefail

msg() { printf 'py-vuln: %s\n' "$*" >&2; }

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# --- version ---------------------------------------------------------------
version=${OSV_SCANNER_VERSION:-}
if [ -z "$version" ] && [ -f "$script_dir/../.osv-scanner-version" ]; then
  version=$(cat "$script_dir/../.osv-scanner-version")
fi
version=$(printf '%s' "$version" | tr -d '[:space:]')
version=${version#v}
if [ -z "$version" ]; then
  msg "osv-scanner version not set. Set \$OSV_SCANNER_VERSION or provide $script_dir/../.osv-scanner-version"
  exit 1
fi

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

# --- mode ------------------------------------------------------------------
mode=${PY_VULN_MODE:-}
if [ -z "$mode" ]; then
  if [ -n "${PRE_COMMIT_FROM_REF:-}" ] && [ -n "${PRE_COMMIT_TO_REF:-}" ]; then
    if ! changed=$(git diff --name-only "$PRE_COMMIT_FROM_REF" "$PRE_COMMIT_TO_REF" 2>&1); then
      msg "cannot diff $PRE_COMMIT_FROM_REF..$PRE_COMMIT_TO_REF ($(printf '%s' "$changed" | tr '\n' ' ')); failing closed"
      mode=block
    elif printf '%s\n' "$changed" | grep -Eq '(^|/)(uv\.lock|pyproject\.toml|osv-scanner\.toml)$'; then
      mode=block
    else
      mode=warn
    fi
  else
    mode=block
  fi
fi
case "$mode" in
  block | warn) ;;
  *)
    msg "invalid PY_VULN_MODE '$mode' (expected block or warn)"
    exit 1
    ;;
esac

locks=()
while IFS= read -r -d '' f; do
  case "$f" in
    uv.lock | */uv.lock) [ -f "$f" ] && locks+=("$f") ;;
  esac
done < <(git ls-files -z)
if [ "${#locks[@]}" -eq 0 ]; then
  msg "no tracked uv.lock; nothing to scan"
  exit 0
fi
msg "mode=$mode osv-scanner=v$version locks=${locks[*]}"

# --- install ---------------------------------------------------------------
bin_dir="${XDG_CACHE_HOME:-$HOME/.cache}/promy-py-vuln/v$version"
bin="$bin_dir/osv-scanner"
if [ ! -x "$bin" ]; then
  msg "installing osv-scanner v$version into $bin_dir (needs Go on PATH)"
  mkdir -p "$bin_dir"
  # GOTOOLCHAIN=auto: the scanner's go.mod may need a newer Go than the local one.
  if ! GOBIN="$bin_dir" GOTOOLCHAIN=auto go install "github.com/google/osv-scanner/v2/cmd/osv-scanner@v$version"; then
    if [ "$mode" = "block" ]; then
      msg "installing osv-scanner failed; failing closed. Offline or no Go are likely causes. Bypass with SKIP=py-vuln git push"
      exit 1
    fi
    msg "WARNING: installing osv-scanner failed; not blocking this push"
    exit 0
  fi
fi

# --- run -------------------------------------------------------------------
# osv-scanner exits 1 when it reports vulnerabilities; anything else non-zero is
# a scanner error (127 general, 128 no packages found).
found=0
failed=0
for lock in "${locks[@]}"; do
  dir=$(dirname "$lock")
  config=()
  if [ -f "$dir/osv-scanner.toml" ]; then
    config=(--config "$dir/osv-scanner.toml")
  elif [ -f osv-scanner.toml ]; then
    config=(--config osv-scanner.toml)
  fi
  msg "scanning $lock (config: ${config[*]-none})"
  rc=0
  "$bin" scan source ${config[@]+"${config[@]}"} --lockfile "$lock" || rc=$?
  case "$rc" in
    0) ;;
    1) found=1 ;;
    *) failed=1 ;;
  esac
done

if [ "$found" -eq 0 ] && [ "$failed" -eq 0 ]; then
  exit 0
fi

if [ "$mode" = "block" ]; then
  if [ "$found" -eq 1 ]; then
    msg "blocking: osv-scanner reports vulnerabilities (a lock, pyproject.toml or osv-scanner.toml changed in this push, or manual/forced run). Bump with 'uv lock --upgrade-package <name>', or add a reasoned [[IgnoredVulns]] entry with ignoreUntil at most 90 days ahead; bypass with SKIP=py-vuln git push"
  else
    msg "osv-scanner failed; failing closed. Offline is a likely cause. Bypass with SKIP=py-vuln git push"
  fi
  exit 1
fi

if [ "$found" -eq 1 ]; then
  msg "WARNING: known advisories affect a uv.lock; this push changes no lock, pyproject.toml or osv-scanner.toml so it is not blocked. Run \`make vuln\` and fix in a fix(deps) PR"
else
  msg "WARNING: osv-scanner failed; not blocking this push"
fi
exit 0
