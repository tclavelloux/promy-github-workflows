#!/bin/sh
# Blocks commits made directly on a protected branch.
#
# The promy-* repos are private on a free GitHub plan, so the branch-protection API
# returns 403 and `main` cannot be protected server-side. This hook is the only control.
#
# Protected branches default to `main`; override per caller with `args:`.
set -eu

if [ "$#" -gt 0 ]; then
  protected="$*"
else
  protected="main"
fi

# --quiet exits non-zero on a detached HEAD instead of printing "HEAD".
if ! branch=$(git symbolic-ref --quiet --short HEAD); then
  echo "no-direct-commit-to-main: detached HEAD — cannot determine the current branch." >&2
  echo "no-direct-commit-to-main: check out a branch before committing." >&2
  exit 1
fi

for candidate in $protected; do
  if [ "$branch" = "$candidate" ]; then
    echo "Direct commits to '$candidate' are blocked. Create a feature branch instead." >&2
    exit 1
  fi
done

exit 0
