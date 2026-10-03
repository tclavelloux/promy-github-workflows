# Resume prompt — finish C3, then Phase F

Paste everything below the line into a fresh Claude Code session started in `~/Documents/1_dev`.

Assumes the GitHub Actions budget has reset. If it has not, only Phase F can proceed — it is documentation-only. See §1 of `FLEET-STATUS.md`.

---

Continue the promy fleet CI/security work. Full context: `promy-github-workflows/FLEET-STATUS.md`. **Read it first**, especially §1 (budget), §2 (exact pause state), §3 (the C3 specification) and §5 (landmines).

There are two jobs, in order: **finish C3 stage 3**, then **Phase F**. Do not start F until C3 is complete — several of the worst documentation items are hook documentation, and writing them against an unsettled mechanism means writing them twice.

## Job 1 — finish C3 stage 3

Phase C3 aligns pre-commit configuration across the five service repos. `promy-template-go` is already migrated (commit `74a5fec`) and is the reference implementation — read its `.pre-commit-config.yaml` and `Makefile` before touching anything.

State when this paused, per `FLEET-STATUS.md` §2. **Verify each rather than trusting it** — time has passed:

| Repo | Expected | Action |
|---|---|---|
| `promy-crm` | PR **#54** open, CI green | verify, merge |
| `promy-product` | PR **#91** open, red check was a **billing failure**, not a real one | re-run CI if budget is back, verify, merge |
| `promy-user` | **uncommitted** work on branch `chore/align-precommit-hooks` | review the diff, finish, commit, push, open PR |
| `promy-event-bus` | not started | do it — note it also needs `always_run: true` added to `check-coverage` |
| `promy-identifier` | not started | do it — same `always_run` gap |

`FLEET-STATUS.md` §3 has the complete change specification, the per-repo differences (§2a), the required validation steps, and the false-pass trap when testing the branch guard. Follow it exactly.

Two things most likely to go wrong:
- **`rev:` must be `hooks/v1.0.0`, never `hooks/v1`.** pre-commit resolves `rev:` once and caches it; a moving tag warns on every invocation and freezes every developer at first install. This is the inverse of the `uses:` convention used for workflows, and it is the error that already had to be corrected once.
- **Do not "fix" `promy-event-bus` and `promy-identifier` by adding a `test` pre-push hook.** They never had one, and that is correct — `make check-coverage` already runs the full race suite. The three repos that have one run the suite twice per push.

Delegate to a sonnet subagent, one repo at a time, stopping and reporting after each. Verify every claim independently before merging.

## Job 2 — Phase F: align documentation with what the fleet actually does

This session changed CI shape, coverage configuration, Go version, merge strategy, action pinning and the hook mechanism. The docs describe the previous world. Some do not merely go stale — they instruct actively wrong actions.

### Priority 1 — actively harmful

`promy-template-go/TEMPLATE_INSTRUCTION.md`:
- **L56-59** instructs **"Do NOT run `pre-commit install`"**. After C3, `make setup` *is* `pre-commit install --install-hooks`. This tells a reader not to do the one thing setup does.
- **L84** checklist verifies `core.hooksPath` prints `.githooks`. That command now returns empty and exits 1. Replace with `ls .git/hooks` showing `pre-commit`, `commit-msg`, `pre-push`.
- **L38-47** — the "Set Up Git Hooks" section describing `core.hooksPath` and the three `.githooks` files, all now deleted.
- **L49-53** — `brew install pre-commit` is framed as "so the delegated checks work" and sits *after* `make setup`, which now requires it. Move it before.
- **L61** — "Without `pre-commit` on PATH, the tracked hooks print a warning and continue." False: `make setup` now hard-fails. That is a behaviour improvement worth documenting.
- **L77, L88, L93** — first-commit example, `pre-commit run --all-files` checklist (now misses the pre-push stage), branch-guard mechanism description.

This file matters more than the rest because it is the **scaffold** — every future service is cut from it, so wrong instructions propagate into repos that do not exist yet.

`promy-template-go/scripts/setup-new-repo.sh`:
- **L58-64** — prints `🔧 Installing tracked git hooks...` and `✅ Hooks installed (git config core.hooksPath .githooks)`, asserting a config no longer set.
- **L69-71** — warns `Do NOT run 'pre-commit install': core.hooksPath is set`. Directly contradicts the new `make setup`.
- The pre-commit presence check at **L66-72** must move **above** the `make setup` call, which now genuinely requires it, and should hard-`exit 1` if absent.

### Priority 2 — factually false today

**Every repo's `.testcoverage.yml` header** claims the file is "read by both the pre-push hook and CI". Verify per repo and correct against what is true *after* C3, not what was intended:
- `promy-product`'s cites `.githooks/pre-push` — **a path that does not exist in that repo at all**.
- `promy-template-go`'s cited its own `.githooks/pre-push`, now deleted.

### Priority 3 — stale descriptions

`promy-template-go/HOWTO.md` — **L27** (the whole paragraph is inverted), **L29**, **L58**, **L83**, **L85-89** (the hook table), **L98**.
`promy-template-go/README.md` — **L62** (dead `.githooks/` table row and link), **L69**.

Per-repo `README`/`HOWTO` CI sections across all six: `promy-template-go` lost its `build` job; all six gained `vuln` and `pr-title`; five gained `docker` — not `promy-event-bus`, which is a library with no Dockerfile.

### Approach

- One PR per repo. Use the `/update-readme` skill per repo, then `/update-architecture` if cross-repo structure shifted.
- **Do `promy-template-go` last.** It is the scaffold; write it against the final settled state rather than revising it twice.
- Delegate to a sonnet subagent, one repo at a time, stopping and reporting after each.
- Documentation-only, so `[skip ci]` in the commit body is appropriate if budget is still a concern.

## Git rules — no exceptions

Branch off `main`, never commit on `main`. `git commit -S` always; never `--no-gpg-sign`, never `--no-verify`. Never `git add .` or `git add -u` — explicit paths only. Conventional Commits.

**Squash-merge only, fleet-wide.** The PR title becomes the commit on `main` and is the only thing release-please parses.

End commit bodies with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` and PR bodies with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

Prefix `gh` commands with `GH_TOKEN=` — the env token lacks `workflow` scope.

Do not merge anything without asking.

## After F

**D** (`workflow-sync-check`) then **E** (`promy-market-catalog`), both specified in `FLEET-STATUS.md` §6. Both need CI budget; F does not. D is not mechanical — it must distinguish governed invariants from deliberately measured per-repo values. E is the largest of the three: that repo has no dependency manifest and no tests, so CI cannot meaningfully be added until both exist.

Recommend one, with reasoning, rather than presenting a menu.
