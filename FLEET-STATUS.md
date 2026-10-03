# promy fleet CI/security — status and resume plan

Updated **2026-10-03**. C3 and Phase F are done. Phase D is built, not rolled out.

This document is self-contained. A fresh session should be able to resume from it without prior context. `PHASE-F-PROMPT.md` in this repo is now historical (Phase F is done).

**Scope:** six Go repos — `promy-template-go` (scaffold), `promy-crm`, `promy-event-bus` (library), `promy-identifier`, `promy-product`, `promy-user` — plus this governance repo, which holds every reusable workflow and shared hook. `promy-frontend` (Flutter) and `promy-market-catalog` (Python) are out of scope except where noted.

The work began as a `ci.yml` audit. It found a live coverage-gate hole, then reachable CVEs in all six services, which took priority over the remaining CI work.

---

## 1. STOP — read before interpreting any red check

**GitHub Actions had no remaining budget when this paused.** Jobs fail at startup with:

> The job was not started because recent account payments have failed or your spending limit needs to be increased.

This presents as a **2-second failed job with no runner assigned and zero steps** — easily mistaken for a broken change. Confirm the budget has reset before reading any failure as real.

While constrained, push with `[skip ci]` on its own line in the **commit body**. Never in the PR title: under squash-merge the title becomes the commit on `main` and is the only thing release-please parses. `ci.yml` no longer triggers on `push: main`, so merging costs nothing — only PR runs consume minutes.

---

## 2. Current state

Verified 2026-10-03.

- **DONE — C3, all six repos:**
  - `default_install_hook_types: [pre-commit, commit-msg, pre-push]`
  - shared `no-direct-commit-to-main` at `rev: hooks/v1.0.0`
  - `check-coverage` with `always_run: true`, `stages: [pre-push]`
  - no duplicate `test` pre-push hook
  - Makefile `setup:` = `pre-commit install --install-hooks`
  - no `.githooks/`; `core.hooksPath` unset
- **DONE — Phase F docs alignment.**
- **DONE — fleet dependency bump** for GO-2026-6505 (otel sdk/otlptrace) and GO-2026-6348 (grpc).
- **DONE — go-test-coverage pinned** in all six Makefiles to v2.19.0 (matches `go-coverage.yml`'s SHA-pinned `vladopajic/go-test-coverage` v2.19.0); previously `@latest`.
- **DONE — release guide deleted.**
- **DONE — repo settings in all six** (verified via `scripts/check-repo-settings.sh`): Actions default token `read`, Actions may create PRs, squash-merge only, vulnerability alerts on, automated security fixes on.
- **BUILT, TAGGED, NOT ROLLED OUT:** `dependabot-automerge/v1(.0.0)`, `go-vuln-fix/v1(.0.0)`, `hooks/v1.1.0` (go-vuln hook), `templates/dependabot.yml` (grouped). Per-repo steps: `docs/dependency-automation.md` §Rollout.
- **BUILT, NOT MERGED/TAGGED:** Phase D `workflow-sync` (branch `feat/workflow-sync-check`). Tags to cut: `hooks/v1.2.0`, `go-lint/v1.2.0` + move `go-lint/v1`.
- **Remaining, in order:**
  1. Merge D.
  2. One PR per repo combining dependency automation + workflow-sync at `rev: hooks/v1.2.0` (order: event-bus, template-go, identifier, crm, product, user).
  3. Dependabot triage.
  4. Phase E.

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
- Phase D built (see §6).

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

---

## 6. Remaining phases — D rollout, then E

### F — done (2026-10)

### D — `workflow-sync` (built, not merged)

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
- **Result against `origin/main` of all six (before rollout):**
  - Every repo fails only the two rollout-wiring rules: `ci-lint-sync` and `precommit-governance-repo` ("no hook workflow-sync").
  - `promy-event-bus` additionally fails 4 rules on `.github/workflows/registry.yaml` (see §7).
- **Rollout:** `docs/workflow-sync.md` §Rollout.
  - Tag `hooks/v1.2.0`; never move `hooks/v1.0.0` or `hooks/v1.1.0`.
  - Tag `go-lint/v1.2.0` and move `go-lint/v1`.
  - Deliberate failure to run: event-bus wiring pushed before the `registry.yaml` fix must turn `lint` red.
- **Follow-up, NOT done:** run all hygiene hooks in CI (`pre-commit run --all-files`). Not wired. Expect existing files to fail `trailing-whitespace` / `end-of-file-fixer` first.

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
- 51 open PRs on 2026-10-03: template-go 9, crm 10, product 9, user 11, event-bus 3, identifier 9. None merged.
- Alerts and security updates are now on in all six.
- Automation rollout pending (`docs/dependency-automation.md`). After it, close the superseded individual PRs once the grouped PR opens.
- `groups:` exist in `templates/dependabot.yml`; not yet copied to any repo.
- Pre-rollout rule only: merge Go module bumps one at a time per repo — each rewrites `go.sum`, conflicting the rest until rebase.
- 6 × `gitleaks-action` v2→v3 and ~2 × `release-please-action` v4→v5 deferred — majors needing caller validation, not a green check. `gitleaks-action` v2 required `GITLEAKS_LICENSE` for organisations; if v3 changed that, a green run on a personal repo proves nothing.

**Findings (2026-10-03)**
- **`promy-event-bus` `.github/workflows/registry.yaml`** is a supply-chain gap:
  - `actions/checkout@v4` moving tag (every other fleet workflow is SHA-pinned).
  - No `persist-credentials: false`.
  - No top-level `permissions:`.
  - Downloads `yq` from `releases/latest` with no checksum.
  - workflow-sync flags all four: `sha-pin`, `persist-credentials`, `workflow-permissions`, `unpinned-tool`.
  - Fix in event-bus's rollout PR: SHA-pin checkout with version comment, `persist-credentials: false`, `permissions: {contents: read}`, pin yq to an exact release and verify sha256.
- **`promy-template-go` `release_please/`** holds a second `release-please-config.json` and `.release-please-manifest.json` that differ from the root copies:
  - `package-name: TEMPLATE_PACKAGE_NAME` vs `promy-template-go`.
  - No `include-component-in-tag`.
  - Manifest `0.2.0` vs root `0.1.0`.
  - Nothing in the repo references the directory.
  - Unexplained. Decide: keep (scaffold placeholder) or delete.
- **The two public repos can be protected server-side and are not.** `gh api repos/tclavelloux/{promy-event-bus,promy-github-workflows}/branches/main/protection` returns `404 Branch not protected`, not the private repos' `403 Upgrade to GitHub Pro`. A ruleset requiring the `lint` check (and so `workflow-sync`) on `main` is available for these two today. Not done.
- **Hooks without explicit `stages:` run twice per commit** (pre-commit and commit-msg). `gitleaks` genuinely runs twice. Fix is `default_stages: [pre-commit]`.

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
- **Squash-merge only, fleet-wide** (arbitrated 2026-09-13, #13). The PR title becomes the commit on `main` and is the only thing release-please parses, so it must be a clean Conventional Commits string. Atomicity lives at the PR boundary — splitting commits within a branch is cosmetic.
- End commit bodies with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`; end PR bodies with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
- Merging workflow-file PRs needs `workflow` scope, which the `GH_TOKEN` env var lacks. Prefix `gh` commands with `GH_TOKEN=` to fall back to the keyring token.
- Verify subagent claims independently before merging. Their reports have been accurate on substance but have twice overstated what was actually exercised.
