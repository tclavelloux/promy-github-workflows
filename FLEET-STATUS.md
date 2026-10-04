# promy fleet CI/security — status and resume plan

Updated **2026-10-04**. C3, Phase F and Phase D are done. Dependency and vuln automation is live in five repos (crm pending). Open: the crm#36 blocker, Dependabot triage, Phase E.

This document is self-contained. A fresh session should be able to resume from it without prior context. Its resume prompt is `NEXT-SESSION-PROMPT.md`; `PHASE-F-PROMPT.md` is historical (Phase F is done).

**Scope:** six Go repos — `promy-template-go` (scaffold), `promy-crm`, `promy-event-bus` (library), `promy-identifier`, `promy-product`, `promy-user` — plus this governance repo, which holds every reusable workflow and shared hook. `promy-frontend` (Flutter) and `promy-market-catalog` (Python) are out of scope except where noted.

The work began as a `ci.yml` audit. It found a live coverage-gate hole, then reachable CVEs in all six services, which took priority over the remaining CI work.

---

## 1. Read before interpreting any red check

- **The Actions budget reset on 2026-10-03**; normal CI verification applies again.
- **Billing signature (if it ever recurs):** a **2-second failed job with `runner_id` 0 and zero steps** is a billing failure, not a broken change (message: "recent account payments have failed or your spending limit needs to be increased"). Confirm before reading any failure as real.
- **Time of day matters for promy-crm:** its tests fail between 22:00 and 08:00 (wall clock; UTC in CI). A red crm `test` at night is crm#36, not your change. See §2.
- **`[skip ci]` rule:** docs-only PRs carry `[skip ci]` on its own line in the **commit body**, never in the PR title (under squash-merge the title becomes the commit on `main` and is the only thing release-please parses). A PR that changes workflows, Makefiles or code runs CI. `ci.yml` has no `push: main` trigger, so merging costs nothing.

---


## 2. Current state

Verified 2026-10-04.

- **DONE:** C3 (all six), Phase F docs alignment, fleet bump for GO-2026-6505 / GO-2026-6348, go-test-coverage pinned to v2.19.0 in all six Makefiles, release guide deleted, PR-scope rule written (one PR = one business purpose; in the fleet docs and the maintainer's user-level CLAUDE.md).
- **DONE — Phase D `workflow-sync`:** merged (#24); tags `hooks/v1.2.0`, `go-lint/v1.2.0`, `go-lint/v1` moved. Wired in all six: template-go #41, identifier #58, crm #68, product #106, user #91, event-bus #53. Proven on Actions: event-bus's wiring-only commit turned `lint` red with exactly the four known `registry.yaml` findings; the next commit (hardened `registry.yaml`) turned it green.
- **DONE — dependency and vuln automation** (grouped Dependabot, `automerge` as last `ci.yml` job, `vuln-fix.yml`, `go-vuln` hook, `make vuln`): merged in event-bus #55, identifier #60, product #107, user #93, template-go #43. **crm: PR #69 open** (see the blocker).
- **DONE — branch protection** on the two public repos (`promy-github-workflows`, `promy-event-bus`): ruleset `protect-main`, active on the default branch: PR required (0 approvals), no force-push, no deletion, required checks, bypass = repository Admin role only. Governance requires `actionlint`, `zizmor`; event-bus requires `lint / lint`, `vuln / vuln`, `test`, `coverage / coverage`, `gitleaks`, `pr-title / pr-title`. The five private repos cannot be protected (403 on the free plan).
  - A `[skip ci]` PR reports no checks, so it cannot satisfy the required checks: merge it with `gh pr merge --admin`. Release-please PRs and any `GITHUB_TOKEN`-created PR behave the same.
  - `workflow-sync` is deliberately not required yet; add it after a few green runs.
- **Proven on Actions:** `workflow-sync` red then green; the hardened `registry.yaml` (pinned yq, sha256 verified); a manual `Vuln fix` run on event-bus (`vuln-record: 2026-10-03 tclavelloux/promy-event-bus none no-pr`).
- **Dependabot "Check for updates"** was triggered 2026-10-04 by the maintainer for product, user, identifier, event-bus and template-go. NOT crm.
- **BLOCKER — promy-crm tests depend on the wall clock (crm#36).** Seed `pref_user_001` has quiet hours 22:00–08:00 and `internal/domain/message/service.go:238` rejects message creation with 409 inside them (`IsInQuietHours(time.Now())`, which also ignores `Timezone`). The local pre-push gate and CI `test` both fail between 22:00 and 08:00 (CI runs UTC, i.e. 00:00–10:00 Paris). Dependabot's Monday 05:00 Paris run is 03:00 UTC, inside the window: crm's grouped PR cannot pass `test`, so `automerge` cannot merge crm bumps until #36 is fixed.
- **Remaining, in order:**
  1. crm #69: its `test` job fails while UTC is 22:00–08:00. Re-run all jobs after 08:00 UTC (`gh run rerun <id>` without `--failed`), merge when green.
  2. Fix crm#36 as its own PR: inject a clock, honour the `Timezone` field, stop depending on seeded 22:00–08:00 windows in tests.
  3. Then trigger crm's "Check for updates" (UI: Insights → Dependency graph → Dependabot).
  4. Dependabot triage: after each grouped PR opens, close the superseded individual minor/patch PRs with a `@dependabot close` comment; leave majors and release-please PRs. Watch the first grouped PR's `automerge` job summary.
  5. Make each `automerge` gate fail once: a failing check, a major bump, a human commit pushed onto a Dependabot branch, a `github-actions` PR.
  6. Phase E.
  7. Optional follow-ups: §6 D and §7.

---

## 3. C3 reference

- Reference implementation: `promy-template-go` at `74a5fec`.
- `rev:` must be immutable (`hooks/v1.0.0`, never `hooks/v1`) — see §5.
- Validation that worked: throwaway copy of the repo; `make setup` yields all three hook files (`pre-commit`, `commit-msg`, `pre-push`); commit on `main` blocked; non-conventional message rejected; no `[WARNING] ... mutable reference`.
- Now enforced by workflow-sync rules `precommit-*` and `make-*`.

---

## 4. What shipped

### Plan 1 — `go-coverage.yml` v2

Thresholds and exclusions come from each caller's own `.testcoverage.yml`; the inputs that duplicated them are gone. Inputs cut 7 → 3 (`post-pr-comment`, `coverage-artifact`, `profile-filename`) — none can contradict the source of truth. `.testcoverage.yml` is mandatory; the job errors out rather than falling back to a zero-threshold config that would pass everything. Exclusions are applied once, by `go-test-coverage` itself, against file paths — the old profile-line pre-filter and its incompatible regex dialect are gone. Sticky PR comment (PATCH in place, not delete-and-repost), statement-weighted per-package rollup, `❌` header with uncovered line ranges on failure. The gate runs `continue-on-error` so the comment posts first; a final step re-reads `steps.coverage.outcome` and re-raises.

**Root bug:** `promy-crm` gated CI at 55% while its pre-push hook gated at 88%, on a different denominator, since PR #34.

### Plan 2 Phase A — governance hardening

`go-lint.yml` gained `golangci-lint fmt --diff`, `config verify` and `go mod tidy -diff` — all three existed as local hooks and were absent from CI, so anyone pushing without hooks bypassed them. All action refs SHA-pinned with version comments, `persist-credentials: false` on every checkout, `dependabot.yml`, and a blocking `zizmor` job (15 findings → 0).

### Tag scheme correction

Phase A shipped **inert**. Git tags are repo-wide: freezing bare `v1` for go-coverage's breaking change also froze `go-lint.yml@v1`, which all six callers referenced, so the new gates reached nobody. Per-workflow namespaces now exist, all signed:

```
go-lint/v1  ·  go-vuln/v1  ·  go-coverage/v2  ·  go-docker/v1  ·  pr-title/v1  ·  dependabot-automerge/v1  ·  go-vuln-fix/v1  ·  hooks/v1.0.0  ·  hooks/v1.1.0
```

### Phase S — CVE remediation, 6/6 complete

| Repo | Called CVEs (CI) | After |
|---|---|---|
| promy-template-go | 30 | 0 |
| promy-identifier | 17 | 0 |
| promy-user | 15 | 0 |
| promy-crm | 14 | 0 |
| promy-product | 9 | 0 |
| promy-event-bus | 8 | 0 |

All on `go 1.26.8`, Dockerfiles and dev.Dockerfiles aligned to `golang:1.26`, `vuln:` job live on `go-vuln/v1`. Six of event-bus's eight CVEs were **stdlib**, fixable only by the toolchain bump — 1.24/1.25 are outside Go's support window and never receive backports. `promy-event-bus` went first because crm, user and product all depend on it; consumers bump the library and let MVS resolve transitives.

### Phase B — Docker validation + `ci.yml` hygiene, 6/6 complete

`go-docker.yml` (static `Dockerfile`-vs-`go.mod` consistency gate plus `docker build`), duplicate `push: main` trigger dropped, workflow-level `concurrency` + `cancel-in-progress`, per-repo measured `timeout-minutes`, draft-PR skip, `pull-requests` scoped per job, artifact `retention-days: 1` + `if-no-files-found: error`, `-v` dropped from `go test`.

Docker validation was promoted from Phase C after `promy-crm`'s Railway deploy failed on a stale base image — CI had never built the image, so `go.mod` and the Dockerfiles drifted until a deploy broke.

### Phase C — supply chain + PR titles

- **C1**, 6/6: caller actions SHA-pinned (`checkout@v7.0.1`, `setup-go@v7.0.0`, `upload-artifact@v7.0.1`, `gitleaks-action@v2.3.9`), `persist-credentials: false`, `dependabot.yml` (`gomod` + `github-actions`).
- **C2**, 6/6: `pr-title.yml` on `pr-title/v1`, trigger `pull_request` with `types: [opened, edited, reopened, synchronize]`.
- `release-please.yml` pinned 6/6 to `@5c625bf # v4.4.1`. **Not validated pre-merge and cannot be** — it fires only on `push: main`. The claim that holds is behaviour-neutral by construction.
- **C3 stages 1 and 2 done**: shared hook published and tagged; `promy-template-go` migrated off `.githooks/`.

### Since 2026-09-13

- C3 stage 3 in the five service repos.
- Phase F docs alignment.
- GO-2026-6505 / GO-2026-6348 bump fleet-wide.
- Dependency + vuln automation built and tagged:
  - `dependabot-automerge.yml`, `go-vuln-fix.yml`
  - go-vuln hook in `hooks/v1.1.0`
  - grouped `templates/dependabot.yml`
  - `ci.yml` guard keeping `.govulncheck-version`, `go-vuln.yml`, `go-vuln-fix.yml` and the fix body in sync
- go-test-coverage pins (v2.19.0) in all six Makefiles.
- Release guide removed.
- Phase D `workflow-sync` merged, tagged and wired in all six (see §6).
- Dependency and vuln automation rolled out to five repos (crm pending, see §2).
- Branch protection (`protect-main`) on the two public repos.
- Wording: one PR per business purpose (README of template-go, HOWTO of user/identifier/template-go, §8, user-level CLAUDE.md).
- event-bus `registry.yaml` hardened (SHA-pinned checkout, `persist-credentials: false`, `permissions: contents: read`, yq v4.54.1 verified by sha256); `events:subscriptions` owner fixed to `promy-subscription`; event-bus `CLAUDE.md` event contract / DLQ / new-event steps corrected.

---

## 5. Landmines — each cost real time to find

**`rev: hooks/v1` is wrong for pre-commit.** Workflows and pre-commit resolve refs oppositely:

| | resolution | correct ref |
|---|---|---|
| `uses:` in a workflow | re-resolved **every run** | moving alias — `go-lint/v1` |
| `rev:` in `.pre-commit-config.yaml` | resolved **once**, cached in `~/.cache/pre-commit`, never re-resolved | immutable — `hooks/v1.0.0` |

A moving tag warns on every invocation and freezes each developer at whatever commit they first installed. Moving it ships them nothing.

**`startup_failure` = zero jobs, no log.** Every repo's default workflow token is `contents: read`. A called workflow can only *narrow* the caller's token, so a caller job requesting `pull-requests: read` without its own `permissions:` block is an escalation — refused before any job starts. Discriminator: job count. A real failure has jobs; a startup failure has none.

**`gitleaks-action@v2` needs `pull-requests: read`.** It calls `GET /pulls/{n}/commits` to scope its scan and was silently living off a workflow-level `write`. Removing the grant entirely 403s the job in ~8s.

**`setup-go@v7` changed `GOTOOLCHAIN` from `auto` to `local`**, breaking `go install` of any tool requiring a newer Go. `actionlint` and `zizmor` both passed on the broken version — only a real caller surfaced it.

**`gh run rerun` does NOT re-resolve reusable-workflow refs.** It replays the SHA pinned at dispatch, so re-running to pick up a `@main` change silently re-tests the old code and looks exactly like a failed fix. Push a commit instead.

**Local CVE counts understate CI.** `setup-go` resolves the toolchain from `go.mod`, so CI ran older, more vulnerable stdlibs than local scans. `promy-template-go`: 8 called locally on Go 1.26.3, **30** in CI.

**`gh pr checks --json state` exits 8 while checks are pending** and kills naive poll loops. Poll `gh run view <id> --json status -q .status` until `completed`.

**Checking out `main` in a scratch repo also checks out `main`'s old config**, producing a false pass when testing the branch guard.

**Do not normalise the `timeout-minutes` values.** They are 15 / 25 / 20 / 10 / 8 / 6, each measured from that repo's own runtimes. Two separate agents have proposed unifying them. The inconsistency is the correctness.

**A green check is not verification.** Recurring theme: Phase A shipped inert and passed everything; the docker gate, the coverage failure path and the branch guard each had to be deliberately made to *fail* before being trusted.

**The governance repo is public, not private.** Several docs said private.
- Check: `gh api repos/tclavelloux/promy-github-workflows -q .visibility` → `public`.
- The real constraint on reusable workflows reading their own files: no documented context exposes the called workflow's own ref, so a checkout would read `main`, unpinned.

**A workflow-sync exemption must sit on or directly above the line the finding reports.**
- For an absent key, the finding reports the parent key (e.g. `gitleaks:`).
- A marker placed below it is reported as `stale-exemption`.

**crm tests are wall-clock dependent.** See §2 blocker: quiet hours 22:00–08:00 make `TestDeleteCommunicationHandler` (and any test creating a message for `user_001`) return 409. The pre-push `check-coverage` hook blocks the push with `error: failed to push some refs` and no reason: read the hook output above it. Do not bypass with `--no-verify`.

**`[skip ci]` in the HEAD commit suppresses every workflow of a PR**, including the one you are trying to prove. A throwaway validation PR must not carry it. On protected repos a `[skip ci]` PR reports no checks and needs `gh pr merge --admin`.

**Never require a path-filtered check.** event-bus `validate` (registry) runs only on `registry/**` PRs; as a required check it would block everything else.

**`GITHUB_TOKEN`-created PRs and merges start no workflows**: vuln-fix PRs have no CI until closed and reopened; an automerge squash does not fire release-please.

**Reusable-workflow logs have no step names** (all `UNKNOWN STEP`) and `::notice` output lives in the check-run annotations (`gh api repos/<r>/check-runs/<job>/annotations`), not in `gh run view --log`.

**Concurrent pre-push hooks race on `~/go/bin/go-test-coverage`.** Serialise pushes (a `mkdir`-lock around push + `gh pr create` worked) and run them in the background: each hook runs the full race suite and takes minutes.

**The checker accepts only the moving major (`go-lint/v1`) for workflows and an immutable `hooks/vX.Y.Z` for hooks.** Validating through `go-lint/v1.2.0` or a SHA rev adds noise violations.

**Sonnet subagents:** one returned only "placeholder", others overstated what they ran, invented a rationale, claimed a section was absent when it was present, or applied a bash-only recipe wrongly. Verify mechanically: byte-compare with templates, token-diff an old README against the new one, re-run the checker, read the real diff.

**zsh and macOS:** unquoted variables do not word-split (`git add $files`, `for x in $list`); `?` and `*` in URLs/paths glob; no `grep -P`; BSD `sed -i` needs a suffix; awk quoting differs. Branch names containing `/` create nested worktree directories (use flat worktree paths). `git add -- <already-staged deleted path>` errors "pathspec did not match" and, chained with `&&`, silently skips the commit: print the index before committing.

**IDE diagnostics such as "could not import" / "undefined" in scratch worktrees are gopls workspace noise**: trust `go build` / `go vet` / `go test`.

---

## 6. Remaining phases — D follow-ups, then E

### F — done (2026-10)

### D — `workflow-sync` (DONE; verification partial)

Every drift this session began with was a documented guarantee with nothing enforcing it. `workflow-sync` checks each repo against a policy kept in this repo.

- **Delivery:** pre-commit hook `workflow-sync` (Go, `language: golang`) in this repo. The caller pins it by immutable `rev:`, so policy and checker version together and a policy change never turns six repos red at once.
- **CI:** opt-in `with: workflow-sync: true` on the caller's `lint` job. Last step of `go-lint.yml` runs `pre-commit run workflow-sync --all-files` (pre-commit 4.6.0 via pipx). Folded into lint because Actions bills each job rounded up to a minute; `automerge` already `needs: lint`.
- **Governed invariants vs measured locals.** Full catalogue (41 rule ids) in `docs/workflow-sync.md`. Groups:
  - workflow-wide: `sha-pin`, `governance-ref`, `persist-credentials`, `workflow-permissions`, `dangerous-trigger`, `unpinned-tool`
  - `ci.yml`: `ci-*`
  - pr-title / release-please / vuln-fix / dependabot workflows
  - pre-commit: `precommit-*`, `vuln-target`
  - Makefile: `make-setup`, `make-hookspath`, `make-coverage-pin`
  - repo files: `no-githooks`, `golangci-version`, `testcoverage`
- **Measured locals, never flagged:** `timeout-minutes` values, docker job presence (derived: required iff root `Dockerfile`), `services`, `env`, coverage floors, dependency lists, exact third-party SHAs.
- **Exemptions:** `# sync-exempt: <rule> <reason>` / `# sync-exempt-file:`. Reason mandatory. Stale or bad markers fail. Every exemption is printed.
- **Duplication guard:** `internal/syncheck/governance_test.go` ties policy constants to `go-coverage.yml` (go-test-coverage v2.19.0), `go-lint.yml` (golangci-lint 2.13.2), `pr-title.yml` (commit types), the reusable-workflow list and `templates/`. Governance `ci.yml` job `workflow-sync` runs it.
- **Repo settings:** `GITHUB_TOKEN` cannot read them. Run by hand: `GH_TOKEN= scripts/check-repo-settings.sh`.
- **Limits:**
  - No branch protection on the free plan: red blocks only automerge, not a human merge.
  - `SKIP=` / `--no-verify` bypass locally.
  - A caller pinning an old rev runs the old policy.
- **Motivating example (unpinned-tool class):** Makefiles installed go-test-coverage `@latest` while CI ran v2.19.0, so local and CI could measure coverage differently with no diff. Now caught by `make-coverage-pin` + `unpinned-tool`.
- **Rollout: done 2026-10-04.** `hooks/v1.2.0` and `go-lint/v1.2.0` cut, `go-lint/v1` moved (the only change to `go-lint.yml` since the old alias was the additive input, a no-op when off). Every repo's `.pre-commit-config.yaml` carries `rev: hooks/v1.2.0` with `workflow-sync` and `go-vuln`, and its `lint` job sets `with: workflow-sync: true`.
- **Still open for D:**
  - Add `workflow-sync` as a required check on the two rulesets once it has a few green runs.
  - Run all hygiene hooks in CI (`pre-commit run --all-files`): not wired; expect existing files to fail `trailing-whitespace` / `end-of-file-fixer` first.
  - `make vuln` fails while `.pre-commit-config.yaml` has unstaged edits (pre-commit refuses to run). Adding `--all-files` fixes it but the policy pins the recipe (`VulnRecipe`), so it needs a policy edit plus a new hook release (`hooks/v1.3.0`).
  - The `automerge` gate has never merged a real Dependabot PR; see §2 step 5.

### E — `promy-market-catalog`

**Zero CI**, but the blocker is more fundamental. Surveyed 2026-09-13: 74 tracked `.py` files, Python 3.12, `.venv` correctly gitignored, **zero first-party tests** (every `test_*.py` on disk is inside `.venv/site-packages`), and **no dependency manifest of any kind** — no `pyproject.toml`, `requirements.txt`, `setup.py`, `poetry.lock` or `uv.lock`. `src/` holds `leaflet`, `store`, `template`, `yolo`; `models/leaflet_product_detector`. An ML/CV pipeline, not a service. release-please is already configured.

CI cannot be added until a manifest exists — there is nothing to install from. Order:
1. Generate a manifest from the working `.venv`, then curate to genuine direct dependencies rather than committing the transitive closure.
2. Choose the toolchain. `uv` fits the fleet's pinned-tooling pattern (`.golangci-version`, `.actionlint-version`, `.govulncheck-version`, `.zizmor-version`).
3. Add lint (`ruff`) and format CI.
4. Add tests, then a coverage floor. A floor over zero tests is 0% and meaningless.

**Treat E as the largest of the three**, not the smallest. Scoping it as a quick win produces a green badge over an ungated repo — the false assurance this session spent its time removing.

---

## 7. Open issues and un-actioned findings

**Issues**
- **promy-identifier#57** — `POST /api/identify/v1/` has no inbound authentication. OPEN, security.
- **promy-github-workflows#6** — audit SHA pins, due 2027-03-12. Backstop for Dependabot PRs piling up unreviewed.
- **promy-user#68** — coverage differs by one statement between local (1416/1653) and CI (1415/1653). Gates nothing today, but `.testcoverage.yml` cites 85.7%, which CI does not reproduce — a ratchet to 86% would be flaky, not merely tight.
- **promy-crm#36** — quiet hours: an untestable clock **and** `IsInQuietHours` ignoring the `Timezone` field, so production applies a Paris user's window in UTC. Fixing the clock alone closes the issue with the production bug intact.
- **promy-github-workflows#13** — CLOSED. Squash-merge arbitrated fleet-wide; historical duplicates removed from `promy-event-bus`.
- Open governance issues:
  - **#18** — release-please: centralise + GitHub App token
  - **#3** — decide which per-repo workflows to centralise
  - **#1** — go-lint: centralise

**Dependabot**
- 51 open PRs on 2026-10-03 (template-go 9, crm 10, product 9, user 11, event-bus 3, identifier 9); none merged ever. Recount before triage.
- Alerts and security updates are on in all six. The grouped `templates/dependabot.yml` is now in five repos (crm pending).
- Pre-grouping rule only: merge Go module bumps one at a time per repo — each rewrites `go.sum`, conflicting the rest until rebase.
- 6 × `gitleaks-action` v2→v3 and ~2 × `release-please-action` v4→v5 deferred — majors needing caller validation, not a green check. `gitleaks-action` v2 required `GITLEAKS_LICENSE` for organisations; if v3 changed that, a green run on a personal repo proves nothing.

**Findings (2026-10-03)**
- **`promy-event-bus` `registry.yaml` supply-chain gap: RESOLVED** (event-bus #53; see §2).
- **`promy-template-go` `release_please/`** holds a second `release-please-config.json` and `.release-please-manifest.json` that differ from the root copies:
  - `package-name: TEMPLATE_PACKAGE_NAME` vs `promy-template-go`.
  - No `include-component-in-tag`.
  - Manifest `0.2.0` vs root `0.1.0`.
  - Nothing in the repo references the directory.
  - Unexplained. Decide: keep (scaffold placeholder) or delete.
- **The two public repos can be protected server-side: DONE** (`protect-main` rulesets, §2).
- **Hooks without explicit `stages:` run twice per commit** (pre-commit and commit-msg). `gitleaks` genuinely runs twice. Fix is `default_stages: [pre-commit]`.

- **`promy-identifier` Makefile `rename` target** substitutes `github.com/ankorstore/yokai-http-template`, which can never match this module: a no-op.
- **`promy-template-go` Makefile `git-check` checklist** still asks "One domain/layer per commit?", contradicting the one-PR-per-business-purpose rule.
- **`promy-event-bus` HOWTO** contradicts itself: the FAQ says a Tier 1 handler "should route to events:dlq" while the DLQ section says the subscriber auto-routes.
- **README Go badge** is hardcoded in template-go (a dynamic badge cannot read private repos).
- Local debris to clean: two stashes in the template-go checkout (both superseded), stale `chore/align-precommit-hooks` branches in the crm/product/user checkouts, hand-written `.git/hooks/pre-commit` scripts (moved to `pre-commit.legacy` by `make setup`; delete after setup), and worktrees registered by past sessions (`git worktree list`, `git worktree prune`).

**Other un-actioned findings**
- **`main` cannot be protected server-side.** `gh api .../branches/main/protection` returns `403 Upgrade to GitHub Pro` on these private free-plan repos. The local branch guard is the only control, and `git commit --no-verify` bypasses it. It is the strongest control available on this plan, not a strong one.
- `FromAsCasing` buildkit warning (`as` vs `AS`) in `promy-product` and `promy-identifier` Dockerfiles. Non-fatal, pre-existing.
- `-v` still in several Makefile `test` targets now that CI dropped it.
- **Release-please PRs: leave them open to accumulate.** Decided 2026-09-13. Merging one cuts a release with no behavioural change; for `promy-event-bus` that would invite three pointless consumer bumps. Do not merge them reflexively as part of a sweep.

---

## 8. Git conventions in force

- Branch off `main`; never commit on `main`.
- `git commit -S` always. Never `--no-gpg-sign`, never `--no-verify`.
- Never `git add .` or `git add -u` — explicit paths only.
- **Squash-merge only, fleet-wide** (arbitrated 2026-09-13, #13). The PR title becomes the commit on `main` and is the only thing release-please parses, so it must be a clean Conventional Commits string. The unit of change is the PR: one business purpose per PR (a whole feature, fix or chore, however many commits); never a PR per commit or per layer, never unrelated purposes bundled.
- End commit bodies with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`; end PR bodies with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
- Merging workflow-file PRs needs `workflow` scope, which the `GH_TOKEN` env var lacks. Prefix `gh` commands with `GH_TOKEN=` to fall back to the keyring token.
- Verify subagent claims independently before merging. Their reports have been accurate on substance but have twice overstated what was actually exercised.
