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

# Ecosystem allowlist. Reads ECOSYSTEM and ECOSYSTEMS; prints the reason and returns 1 to refuse.
check_ecosystem() {
  local eco_list eco eco_ok=false
  # fetch-metadata derives the ecosystem from the branch name
  # (dependabot/<ecosystem>/...): `go_modules` for Go, `uv` for uv,
  # `github_actions` for workflow bumps.
  if [ "$ECOSYSTEM" = "github_actions" ]; then
    echo "github-actions updates need a human merge: GITHUB_TOKEN cannot merge workflow-file changes"; return 1
  fi
  # shellcheck disable=SC2153 # ECOSYSTEMS (the allowlist) is not a typo of ECOSYSTEM
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
  return 0
}

# Excluded dependencies: any name of a (grouped) PR matching any glob refuses
# the whole PR. Reads EXCLUDE and DEPENDENCIES. Patterns are validated first, so
# the unquoted `case` pattern below cannot expand anything but a glob.
check_excluded() {
  local pat name hits="" bad n g
  if [ -n "$(printf '%s' "$EXCLUDE" | tr -d '[:space:],')" ]; then
    bad="$(printf '%s\n' "$EXCLUDE" | tr ',' '\n' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//' | grep -Ev '^$|^[A-Za-z0-9._@/*?+-]+$' || true)"
    if [ -n "$bad" ]; then
      echo "invalid 'exclude-dependencies' entry: only [A-Za-z0-9._@/*?+-] are allowed in a glob"; return 1
    fi
    if [ -z "$(printf '%s' "$DEPENDENCIES" | tr -d '[:space:],')" ]; then
      echo "no dependency-names reported; cannot check exclude-dependencies"; return 1
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

# Dependabot PRs. Reads HEAD_REF UPDATE_TYPE ALLOWED ECOSYSTEM ECOSYSTEMS DEPENDENCIES EXCLUDE.
gate() {
  local t permitted=false

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

  check_ecosystem || return 1
  check_excluded || return 1
  return 0
}

# Normalise a manifest to `<kind> <name> <version>` lines so base and head can be
# compared entry by entry. go.mod: `go` and require lines; every other line is
# kept verbatim as `raw`. uv.lock: one `pkg` line per [[package]]; the rest is ignored.
manifest_entries() {
  case "$1" in
    go.mod|*/go.mod)
      awk '
        /^[[:space:]]*\/\// { next }
        { sub(/[[:space:]]*\/\/.*$/, "") }
        /^[[:space:]]*$/ { next }
        $1 == "require" && $2 == "(" { inreq = 1; next }
        inreq && $1 == ")" { inreq = 0; next }
        inreq && NF == 2 { print "req", $1, $2; next }
        $1 == "require" && NF == 3 { print "req", $2, $3; next }
        $1 == "go" && NF == 2 { print "go", "go", $2; next }
        { print "raw", $0 }
      ' "$2" ;;
    uv.lock|*/uv.lock)
      awk '
        /^\[\[package\]\]/ { name = ""; next }
        /^name = "/ { name = $3; gsub(/"/, "", name); next }
        /^version = "/ && name != "" { v = $3; gsub(/"/, "", v); print "pkg", name, v; name = "" }
      ' "$2" ;;
  esac
}

# Compares two manifest_entries streams (base, head). Prints one `BUMP<TAB>kind<TAB>name<TAB>old<TAB>new`
# per changed version, or `ERR<TAB>reason` for an entry added or removed and for any
# other changed line.
manifest_diff() {
  awk -v OFS='\t' '
    FNR == NR { if ($1 == "raw") oraw[$0] = 1; else o[$1 " " $2] = $3; next }
    $1 == "raw" { nraw[$0] = 1; if (!($0 in oraw)) print "ERR", "line changed outside the version entries: " substr($0, 5); next }
    { k = $1 " " $2; seen[k] = 1
      if (!(k in o)) print "ERR", $2 " was added"
      else if (o[k] != $3) print "BUMP", $1, $2, o[k], $3 }
    END {
      for (k in o) if (!(k in seen)) print "ERR", substr(k, index(k, " ") + 1) " was removed"
      for (r in oraw) if (!(r in nraw)) print "ERR", "line removed outside the version entries: " substr(r, 5)
    }
  ' "$1" "$2"
}

# Succeeds when $2 is a release newer than or equal to $1 within the same
# major.minor (the third and later components may rise). Pre-releases,
# pseudo-versions and anything non-numeric fail.
patch_only() {
  local a="${1#v}" b="${2#v}" i x y
  local av bv
  a="${a%+incompatible}"; b="${b%+incompatible}"
  [[ "$a" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)*$ && "$b" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)*$ ]] || return 1
  IFS=. read -ra av <<< "$a"
  IFS=. read -ra bv <<< "$b"
  [ "$((10#${av[0]}))" -eq "$((10#${bv[0]}))" ] && [ "$((10#${av[1]}))" -eq "$((10#${bv[1]}))" ] || return 1
  for ((i = 2; i < 8; i++)); do
    x="$((10#${av[i]:-0}))"; y="$((10#${bv[i]:-0}))"
    if [ "$y" -gt "$x" ]; then return 0; fi
    if [ "$y" -lt "$x" ]; then return 1; fi
  done
  return 0
}

# promy-fleet-bot vuln-fix PRs. Verifies the diff itself, never the title or body.
# Reads HEAD_REF, FLEET_FILES (newline-separated `<status><TAB><path>`), BASE_DIR and
# HEAD_DIR (the changed files at the merge base and at the head, under their repo
# paths), ECOSYSTEMS and EXCLUDE. On success prints the merge reason; otherwise
# prints the refusal reason and returns 1.
fleet_gate() {
  local status file kind="" k ECOSYSTEM DEPENDENCIES="" summary=""
  local tag kd name old new

  case "$HEAD_REF" in
    fix/vuln-*) kind=go_modules ;;
    fix/py-vuln-*) kind=uv ;;
    *) echo "branch '$HEAD_REF' is not a vuln-fix branch (fix/vuln-*, fix/py-vuln-*)"; return 1 ;;
  esac
  if [ -z "$FLEET_FILES" ]; then echo "PR changes no files"; return 1; fi

  while IFS=$'\t' read -r status file; do
    [ -n "$file" ] || continue
    if [ "$status" != "modified" ]; then echo "'$file' is $status; only modified files are allowed"; return 1; fi
    if ! printf '%s' "$file" | grep -Eq '^[A-Za-z0-9._/-]+$' || [[ "$file" == /* || "$file" == *..* ]]; then
      echo "unexpected file path '$file'"; return 1
    fi
    case "${file##*/}" in
      go.mod|go.sum) k=go_modules ;;
      uv.lock) k=uv ;;
      *) echo "'$file' is outside the files a vuln-fix PR may change (go.mod, go.sum, uv.lock)"; return 1 ;;
    esac
    if [ "$k" != "$kind" ]; then echo "'$file' does not belong to a '$HEAD_REF' PR"; return 1; fi
    if [ "${file##*/}" = "go.sum" ]; then continue; fi

    # Same set of names, versions only moving within the same minor, nothing else changed.
    while IFS=$'\t' read -r tag kd name old new; do
      [ -n "$tag" ] || continue
      if [ "$tag" = ERR ]; then echo "$file: $kd"; return 1; fi
      if ! patch_only "$old" "$new"; then
        echo "$file: $name $old -> $new is not a patch-level bump within the same minor"; return 1
      fi
      summary="${summary:+$summary, }$name $old -> $new"
      if [ "$kd" != go ]; then DEPENDENCIES="${DEPENDENCIES:+$DEPENDENCIES, }$name"; fi
    done < <(manifest_diff <(manifest_entries "$file" "$BASE_DIR/$file") <(manifest_entries "$file" "$HEAD_DIR/$file"))
  done <<< "$FLEET_FILES"

  if [ -z "$summary" ]; then echo "no version bump found in the diff"; return 1; fi
  ECOSYSTEM="$kind"
  check_ecosystem || return 1
  check_excluded || return 1
  printf 'patch-level bumps within the same minor, verified from the diff (%s): %s' "$ECOSYSTEM" "$summary"
  return 0
}
# <<< automerge-gate body
