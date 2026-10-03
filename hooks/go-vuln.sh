#!/usr/bin/env bash
# Runs govulncheck with the fleet-pinned version and CI's Go toolchain.
#
# Mirrors the reusable `go-vuln.yml` workflow locally so `make vuln` and pre-push
# fail on the same findings CI would.
#
# Why warn vs block: advisories are time-dependent. A push that does not touch
# go.mod/go.sum must not be blocked by an advisory published today, but a push that
# edits go.mod/go.sum is exactly where a dependency fix belongs, so it blocks.
#   - block: exit 1 on called vulnerabilities or scanner error (fail closed).
#   - warn:  always exit 0; print findings and a warning.
#
# Mode (first match wins):
#   1. GO_VULN_MODE=block|warn
#   2. PRE_COMMIT_FROM_REF and PRE_COMMIT_TO_REF both set (pre-push): block when the
#      pushed range changes go.mod/go.sum, else warn. A failing git diff blocks.
#   3. Otherwise (manual stage, `make vuln`, --all-files): block.
#
# govulncheck version (first match wins):
#   1. GOVULNCHECK_VERSION env
#   2. .govulncheck-version at the root of this hook repo (the fleet-wide pin)
# The binary is cached per version in ${XDG_CACHE_HOME:-$HOME/.cache}/promy-go-vuln
# so the user's ~/go/bin is never touched.
#
# Go toolchain (first match wins), matching actions/setup-go with go-version-file:
#   1. GO_VULN_TOOLCHAIN env
#   2. go.mod `toolchain` directive, else `go` directive
# GOTOOLCHAIN is exported only for a full x.y.z version; otherwise it is left alone.
#
# Bypass: SKIP=go-vuln git push
set -euo pipefail

msg() { printf 'go-vuln: %s\n' "$*" >&2; }

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# --- version ---------------------------------------------------------------
version=${GOVULNCHECK_VERSION:-}
if [ -z "$version" ] && [ -f "$script_dir/../.govulncheck-version" ]; then
  version=$(cat "$script_dir/../.govulncheck-version")
fi
version=$(printf '%s' "$version" | tr -d '[:space:]')
version=${version#v}
if [ -z "$version" ]; then
  msg "govulncheck version not set. Set \$GOVULNCHECK_VERSION or provide $script_dir/../.govulncheck-version"
  exit 1
fi

# --- repo root + toolchain -------------------------------------------------
repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

toolchain=${GO_VULN_TOOLCHAIN:-}
if [ -z "$toolchain" ] && [ -f go.mod ]; then
  toolchain=$(sed -n 's/^toolchain[[:space:]][[:space:]]*\([^[:space:]]*\).*$/\1/p' go.mod | head -n1)
  if [ -z "$toolchain" ]; then
    go_directive=$(sed -n 's/^go[[:space:]][[:space:]]*\([^[:space:]]*\).*$/\1/p' go.mod | head -n1)
    [ -n "$go_directive" ] && toolchain="go$go_directive"
  fi
fi
if [[ "$toolchain" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  export GOTOOLCHAIN="$toolchain"
else
  msg "note: toolchain '${toolchain:-<none>}' is not a full x.y.z version; leaving GOTOOLCHAIN untouched"
fi

# --- mode ------------------------------------------------------------------
mode=${GO_VULN_MODE:-}
if [ -z "$mode" ]; then
  if [ -n "${PRE_COMMIT_FROM_REF:-}" ] && [ -n "${PRE_COMMIT_TO_REF:-}" ]; then
    if ! changed=$(git diff --name-only "$PRE_COMMIT_FROM_REF" "$PRE_COMMIT_TO_REF" 2>&1); then
      msg "cannot diff $PRE_COMMIT_FROM_REF..$PRE_COMMIT_TO_REF ($(printf '%s' "$changed" | tr '\n' ' ')); failing closed"
      mode=block
    elif printf '%s\n' "$changed" | grep -Eq '(^|/)go\.(mod|sum)$'; then
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
    msg "invalid GO_VULN_MODE '$mode' (expected block or warn)"
    exit 1
    ;;
esac
msg "mode=$mode govulncheck=v$version toolchain=${GOTOOLCHAIN:-default}"

# --- install ---------------------------------------------------------------
bin_dir="${XDG_CACHE_HOME:-$HOME/.cache}/promy-go-vuln/v$version"
bin="$bin_dir/govulncheck"
if [ ! -x "$bin" ]; then
  msg "installing govulncheck v$version into $bin_dir"
  mkdir -p "$bin_dir"
  # GOTOOLCHAIN=auto: the tool's own go.mod may need a newer Go than the repo's pin.
  if ! GOBIN="$bin_dir" GOTOOLCHAIN=auto go install "golang.org/x/vuln/cmd/govulncheck@v$version"; then
    # Same contract as a scanner error: fail closed only in block mode.
    if [ "$mode" = "block" ]; then
      msg "installing govulncheck failed; failing closed. Offline is a likely cause. Bypass with SKIP=go-vuln git push"
      exit 1
    fi
    msg "WARNING: installing govulncheck failed; not blocking this push"
    exit 0
  fi
fi

# --- run -------------------------------------------------------------------
rc=0
"$bin" ./... || rc=$?

if [ "$rc" -eq 0 ]; then
  exit 0
fi

if [ "$mode" = "block" ]; then
  if [ "$rc" -eq 3 ]; then
    msg "blocking: govulncheck reports called vulnerabilities (go.mod/go.sum changed in this push, or manual/forced run) — fix before pushing; bypass with SKIP=go-vuln git push"
  else
    msg "govulncheck failed (exit $rc); failing closed. Offline is a likely cause. Bypass with SKIP=go-vuln git push"
  fi
  exit 1
fi

if [ "$rc" -eq 3 ]; then
  msg "WARNING: newly published advisories affect this module; this push does not change go.mod/go.sum so it is not blocked. Run \`make vuln\` / open a fix(deps) PR"
else
  msg "WARNING: govulncheck failed (exit $rc); not blocking this push"
fi
exit 0
