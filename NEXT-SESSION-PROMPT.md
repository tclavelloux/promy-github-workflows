# Resume prompt — crm#36, Dependabot triage, then Phase E

Paste everything below the line into a fresh Claude Code session started in `~/Documents/1_dev`.

Replaces `PHASE-F-PROMPT.md` (Phase F is finished; see FLEET-STATUS §2).

---

Continue the promy fleet CI/security work. Read `promy-github-workflows/FLEET-STATUS.md` first, all of it, especially §2 (current state and the blocker), §5 (landmines) and §8 (git rules). Then verify the live state yourself before acting — the snapshot is dated 2026-10-04:

- `gh pr list -R tclavelloux/<repo> --state open` for the six repos (template-go, crm, event-bus, identifier, product, user) and the governance repo. Dependabot has been opening PRs since "Check for updates" was triggered for five repos.
- crm PR #69 (`ci: automate dependency bumps and vuln fixes`): its `test` job fails while UTC is inside 22:00–08:00 (crm#36).
- `gh api repos/tclavelloux/promy-github-workflows/rulesets` and the same for `promy-event-bus`: `protect-main` should be active with only the Admin role bypassing.

## Do, in order

1. **crm #69.** After 08:00 UTC re-run all its jobs (not `--failed`), confirm every check is green, ask before merging.
2. **crm#36, as its own PR.** Production bug AND test bug: `IsInQuietHours(time.Now())` (internal/domain/message/service.go:238) ignores the preference's `Timezone`, and the tests depend on the real clock and on seeded 22:00–08:00 quiet hours (`db/seeds/communication_preferences.go`, `pref_user_001`). Inject a clock; evaluate quiet hours in the user's timezone; make the tests independent of the time of day. Prove it by running the crm suite with the clock inside and outside the window.
3. **crm "Check for updates"** is a UI-only step the maintainer does (Insights → Dependency graph → Dependabot). Ask them once #36 is merged.
4. **Dependabot triage.** For each repo where the grouped PR has opened: read the first grouped PR's `automerge` job summary (expect a merge or a precise reason); close the superseded individual minor/patch PRs with a `@dependabot close` comment; leave majors (gitleaks-action v3, release-please-action v5 need human validation) and release-please PRs open.
5. **Make each `automerge` gate fail once** (a green check is not verification): a Dependabot PR with a failing check, a major bump, a human commit pushed onto a Dependabot branch, a `github-actions` PR. Each must leave `automerge` skipped with a precise reason.
6. **Phase E (`promy-market-catalog`).** Spec in FLEET-STATUS §6 E. It is the largest remaining phase: no dependency manifest and zero first-party tests. Recommend an order and ask before building.
7. **Optional follow-ups** (FLEET-STATUS §6 D and §7): require `workflow-sync` on the two rulesets, run all hygiene hooks in CI, the `make vuln` unstaged-config limitation (needs `hooks/v1.3.0`), identifier#57, template-go `release_please/` duplicates, local debris.

## Working agreement

- **One PR = one business purpose** (a whole feature, fix or chore, however many commits). Squash-merge only; the PR title is the commit on `main` and the only thing release-please parses.
- **Docs-only PRs carry `[skip ci]` on its own line in the commit body** (never in the title). A PR that changes workflows, Makefiles or code runs CI. On the two protected repos a `[skip ci]` PR cannot satisfy the required checks and needs `gh pr merge --admin` — say so and wait for the maintainer's explicit word before using it.
- Never merge without the maintainer's explicit approval for that PR. Never `--no-verify`, never `git add .` / `-u`, always `-S`. Prefix `gh` with `GH_TOKEN=`. Commit trailer: the one from the session's attribution reminder.
- Delegate implementation to a sonnet subagent, one repo or piece at a time; plan with an opus subagent when the design is open. **Verify every claim yourself** (see below).
- Work in git worktrees under the session scratchpad, never in the maintainers' dirty checkouts (`promy-crm`, `-product`, `-user` still sit on stale `chore/align-precommit-hooks` branches; `promy-template-go` holds two superseded stashes).

## Landmines found in the last session (FLEET-STATUS §5 has the CI ones)

- **crm tests are wall-clock dependent.** Between 22:00 and 08:00 the pre-push `check-coverage` hook and CI `test` fail (409 in `TestDeleteCommunicationHandler`). The push then fails with `error: failed to push some refs` and no reason: read the hook output above it. Dependabot's Monday 05:00 Paris run is 03:00 UTC, inside the window.
- **`[skip ci]` in the HEAD commit suppresses every workflow of a PR**, including the one you are trying to prove. Any occurrence of the text counts, even in a message that only talks about it (a commit explaining the problem re-suppressed its own checks). A throwaway validation PR must not carry it.
- **Never make a path-filtered check required** (event-bus `validate` runs only on `registry/**`).
- **`GITHUB_TOKEN`-created PRs and merges start no workflows** (vuln-fix PRs have no CI until closed and reopened; an automerge squash does not fire release-please).
- **Reusable-workflow run logs have no step names** (`UNKNOWN STEP`); `::notice` output is in the check-run annotations (`gh api repos/<r>/check-runs/<job>/annotations`).
- **Each pre-push hook runs the full race suite** (minutes) and concurrent hooks race on `~/go/bin/go-test-coverage`. Run pushes in the background and serialise them (a `mkdir` lock around `git push` + `gh pr create` worked).
- **The checker accepts only `go-lint/v1` (moving major) for workflows and an immutable `hooks/vX.Y.Z` for hooks.** Validating through `go-lint/v1.2.0` or a SHA rev adds noise violations.
- **Sonnet subagents are not reliable reporters.** One returned only "placeholder"; others overstated what they ran, invented a rationale, wrongly claimed a section was absent, or reported a bash-only recipe as portable. Verify mechanically: byte-compare generated files with templates, token-diff an old README against the new one, re-run the checker, read the real diff, check a claim against the code.
- **zsh and macOS:** unquoted variables do not word-split (`git add $files`, `for x in $list` — use arrays or `${=var}`); `?`/`*` in URLs and paths glob; no `grep -P`; BSD `sed -i` needs a suffix; awk quoting differs; `rm`/`mv` of a symlinked skills dir hits dotfiles. Branch names with `/` create nested worktree directories (use flat worktree paths).
- **`git add -- <already-staged deleted path>` fails** ("pathspec did not match") and, chained with `&&`, silently skips the commit. Print `git diff --cached --name-status` before committing.
- **`gh` quirks:** `gh pr checks` exits 8 while pending; `gh run rerun` replays the pinned reusable-workflow SHA and re-running only a failed `coverage` job fails once the 1-day `coverage-profile` artifact expires (re-run all jobs); a 2-second job with 0 steps and `runner_id` 0 is a billing failure.
- **IDE diagnostics ("could not import", "undefined") in scratch worktrees are gopls workspace noise**: trust `go build` / `go vet` / `go test`.
- **Dependabot "Check for updates" is UI-only**; there is no CLI trigger. Version updates and security updates are separate (both enabled).
- **The governance repo and promy-event-bus are public**, so rulesets work there. The other five are private on the free plan: no server-side protection, no required checks, so a red check never blocks a human merge — the local `no-direct-commit-to-main` hook is the only control and `--no-verify` bypasses it.
