#!/usr/bin/env bash
# Pre-merge gate of .github/workflows/dependabot-automerge.yml: branch, update
# type, ecosystem and excluded-dependency checks. The PR-commit author check, the
# check-run verification and the merge stay in the workflow.
# Local twin of the inlined step: the block between the `automerge-gate body`
# markers must stay byte-identical in both (internal/syncheck/automerge_test.go
# compares them and runs the decision table through bash).
# Pure local logic, no GitHub API calls.
#
# Source it, then call `gate`:
#   HEAD_REF UPDATE_TYPE ALLOWED ECOSYSTEM ECOSYSTEMS DEPENDENCIES EXCLUDE gate
# gate prints the refusal reason on stdout and returns 1, or returns 0 to proceed.

# >>> automerge-gate body
# Keep this block byte-identical between scripts/automerge-gate.sh and
# .github/workflows/dependabot-automerge.yml (governance tests compare them).

# Lowercase and map '.' and '_' to '-' (PEP 503), so the pattern `typing_extensions*`
# matches the dependency `typing-extensions`. Applied to both sides.
norm_name() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[._]/-/g'; }

gate() {
  local t permitted=false eco_list eco eco_ok=false pat name hits="" bad n g

  case "$HEAD_REF" in
    release-please--*) echo "release-please branches are never auto-merged"; return 1 ;;
  esac

  # Hard-reject majors even if a caller put them in the allowlist.
  if [ -z "$UPDATE_TYPE" ]; then
    echo "fetch-metadata reported no update-type"; return 1
  fi
  if [ "$UPDATE_TYPE" = "version-update:semver-major" ]; then
    echo "major updates need a human review"; return 1
  fi
  for t in $ALLOWED; do
    if [ "$t" = "$UPDATE_TYPE" ]; then permitted=true; fi
  done
  if [ "$permitted" != "true" ]; then
    echo "update-type '$UPDATE_TYPE' is not in the allowlist '$ALLOWED'"; return 1
  fi

  # fetch-metadata derives the ecosystem from the branch name
  # (dependabot/<ecosystem>/...): `go_modules` for Go, `uv` for uv,
  # `github_actions` for workflow bumps.
  if [ "$ECOSYSTEM" = "github_actions" ]; then
    echo "github-actions updates need a human merge: GITHUB_TOKEN cannot merge workflow-file changes"; return 1
  fi
  eco_list="$(printf '%s' "$ECOSYSTEMS" | tr -d '[:space:]')"
  if ! printf '%s' "$eco_list" | grep -Eq '^[a-z_-]+(,[a-z_-]+)*$'; then
    echo "invalid 'ecosystems' input: want comma-separated names matching [a-z_-]+ (e.g. go_modules,uv)"; return 1
  fi
  while IFS= read -r eco; do
    if [ "$eco" = "$ECOSYSTEM" ]; then eco_ok=true; fi
  done <<< "$(printf '%s' "$eco_list" | tr ',' '\n')"
  if [ "$eco_ok" != "true" ]; then
    echo "package-ecosystem '$ECOSYSTEM' is not in the allowed ecosystems '$eco_list'"; return 1
  fi

  # Excluded dependencies: any name of a (grouped) PR matching any glob refuses
  # the whole PR. Patterns are validated first, so the unquoted `case` pattern
  # below cannot expand anything but a glob.
  if [ -n "$(printf '%s' "$EXCLUDE" | tr -d '[:space:],')" ]; then
    bad="$(printf '%s\n' "$EXCLUDE" | tr ',' '\n' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//' | grep -Ev '^$|^[A-Za-z0-9._@/*?+-]+$' || true)"
    if [ -n "$bad" ]; then
      echo "invalid 'exclude-dependencies' entry: only [A-Za-z0-9._@/*?+-] are allowed in a glob"; return 1
    fi
    if [ -z "$(printf '%s' "$DEPENDENCIES" | tr -d '[:space:],')" ]; then
      echo "fetch-metadata reported no dependency-names; cannot check exclude-dependencies"; return 1
    fi
    while IFS= read -r name; do
      [ -n "$name" ] || continue
      while IFS= read -r pat; do
        [ -n "$pat" ] || continue
        n="$(norm_name "$name")"
        g="$(norm_name "$pat")"
        # shellcheck disable=SC2254 # the glob is the point
        case "$n" in
          $g) hits="${hits:+$hits, }'$name' (pattern '$pat')"; break ;;
        esac
      done <<< "$(printf '%s\n' "$EXCLUDE" | tr ',' '\n' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
    done <<< "$(printf '%s\n' "$DEPENDENCIES" | tr ',' '\n' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
    if [ -n "$hits" ]; then
      echo "excluded dependencies need a human review: $hits"; return 1
    fi
  fi
  return 0
}
# <<< automerge-gate body
