# Phase F turnkey prompt

Paste everything below the line into a fresh Claude Code session started in `~/Documents/1_dev`.

Phase F is documentation-only, so it is safe to run while the GitHub Actions budget is constrained — but read the prerequisite first.

---

Continue the promy fleet CI/security work. Full context is in `promy-github-workflows/FLEET-STATUS.md` — **read it first**, especially the "STOP" and "Landmines" sections.

## Prerequisite — check this before doing anything

Phase F depends on **C3 stage 3**, which was interrupted mid-way. Verify its state and finish it if needed:

| Repo | Expected state | If not done |
|---|---|---|
| `promy-crm` | PR #54 merged | verify and merge |
| `promy-product` | PR #91 merged | verify and merge — its red check was a billing failure, not a real one |
| `promy-user` | migrated | **uncommitted work may still be on branch `chore/align-precommit-hooks`** — finish, commit, push |
| `promy-event-bus` | migrated | not started — do it |
| `promy-identifier` | migrated | not started — do it |

The stage 3 change per repo is specified in `FLEET-STATUS.md` under "The stage 3 change, per repo". Reference implementation: `promy-template-go` at `74a5fec`.

If the Actions budget is still exhausted, put `[skip ci]` on its own line in the **commit body** — never in the PR title, which becomes the squash commit and is what release-please parses.

Do not start Phase F until every repo's hook mechanism is settled. Several of the worst documentation items *are* hook documentation, and writing them against an unsettled mechanism means writing them twice.

## Phase F — align documentation with what the fleet actually does

This session changed CI shape, coverage configuration, Go version, merge strategy, action pinning and the hook mechanism. The docs describe the previous world. Some do not merely go stale — they instruct actively wrong actions.

### Priority 1 — actively harmful

`promy-template-go/TEMPLATE_INSTRUCTION.md`:
- **L56-59** instructs **"Do NOT run `pre-commit install`"**. After C3, `make setup` *is* `pre-commit install --install-hooks`. This tells a reader not to do the one thing setup does.
- **L84** checklist item verifies `core.hooksPath` prints `.githooks`. That command now returns empty and exits 1.
- **L38-47** — "Set Up Git Hooks" section describing `core.hooksPath` and the three `.githooks` files.
- **L49-53** — the `brew install pre-commit` step is framed as "so the delegated checks work" and sits *after* `make setup`, which now depends on it. Move it before.
- **L61** — "Without `pre-commit` on PATH, the tracked hooks print a warning and continue." False: `make setup` now hard-fails. That is an improvement worth documenting.
- **L77, L88, L93** — first-commit example, `pre-commit run --all-files` checklist (now misses the pre-push stage), branch-guard mechanism description.

This matters more than the others because it is the **scaffold**: every future service is cut from it, so wrong instructions propagate into repos that do not exist yet.

`scripts/setup-new-repo.sh` — **L58-64, L69-71**: prints `✅ Hooks installed (git config core.hooksPath .githooks)` and warns `Do NOT run 'pre-commit install'`. The pre-commit presence check at L66-72 must move **above** the `make setup` call, which now requires it.

### Priority 2 — factually false today

**Every repo's `.testcoverage.yml` header** claims the file is "read by both the pre-push hook and CI". Verify per repo and correct:
- `promy-product`'s cites `.githooks/pre-push` — **a path that does not exist in that repo at all**.
- `promy-template-go`'s cited its own `.githooks/pre-push`, now deleted.
- The four repos without an installed pre-push hook were CI-only; C3 fixes the mechanism, so confirm what is true *after* stage 3 rather than describing intent.

### Priority 3 — stale descriptions

`promy-template-go/HOWTO.md` — L27 (whole paragraph inverted), L29, L58, L83, L85-89 (hook table), L98.
`promy-template-go/README.md` — L62 (dead `.githooks/` table row and link), L69.

Per-repo `README`/`HOWTO` CI sections across all six: `promy-template-go` lost its `build` job; all six gained `vuln` and `pr-title`; five gained `docker` (not `promy-event-bus`, a library with no Dockerfile).

### Approach

- One PR per repo. Squash-merge only; the PR title is the commit on `main` and what release-please parses.
- Use the `/update-readme` skill per repo, then `/update-architecture` if cross-repo structure shifted.
- **Do `promy-template-go` last.** It is the scaffold, so write it against the final settled state rather than revising it twice.
- Delegate implementation to a sonnet subagent, one repo at a time, stopping and reporting after each.
- **Verify every claim independently before merging.** Subagent reports have been accurate on substance but have overstated what was exercised — one claimed a code path was tested when only the other branch had run.

### Git rules — no exceptions

Branch off `main`, never commit on `main`. `git commit -S` always; never `--no-gpg-sign`, never `--no-verify`. Never `git add .` or `git add -u` — explicit paths only. Conventional Commits. End commit bodies with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` and PR bodies with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`. Prefix `gh` commands with `GH_TOKEN=` — the env token lacks `workflow` scope. Do not merge without asking.

### After F

Next is **D** (`workflow-sync-check`) then **E** (`promy-market-catalog`). Both are specified in `FLEET-STATUS.md`. Both need CI budget; F does not. Recommend one, with reasoning, rather than presenting a menu.
