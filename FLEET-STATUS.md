# promy fleet CI/security — status and resume plan

Paused **2026-09-13** on GitHub Actions budget exhaustion, mid-way through Phase C3 stage 3.

This document is self-contained. A fresh session should be able to resume from it without prior context. Its companion is `PHASE-F-PROMPT.md` in this repo.

**Scope:** six Go repos — `promy-template-go` (scaffold), `promy-crm`, `promy-event-bus` (library), `promy-identifier`, `promy-product`, `promy-user` — plus this governance repo, which holds every reusable workflow and shared hook. `promy-frontend` (Flutter) and `promy-market-catalog` (Python) are out of scope except where noted.

The work began as a `ci.yml` audit. It found a live coverage-gate hole, then reachable CVEs in all six services, which took priority over the remaining CI work.

---

## 1. STOP — read before interpreting any red check

**GitHub Actions had no remaining budget when this paused.** Jobs fail at startup with:

> The job was not started because recent account payments have failed or your spending limit needs to be increased.

This presents as a **2-second failed job with no runner assigned and zero steps** — easily mistaken for a broken change. Confirm the budget has reset before reading any failure as real.

While constrained, push with `[skip ci]` on its own line in the **commit body**. Never in the PR title: under squash-merge the title becomes the commit on `main` and is the only thing release-please parses. `ci.yml` no longer triggers on `push: main`, so merging costs nothing — only PR runs consume minutes.

---

## 2. Exact pause state

Phase C3 stage 3 aligns pre-commit configuration across the five service repos. `promy-template-go` (stage 2) is already done and merged.

| Repo | Local branch | `main` migrated? | State |
|---|---|---|---|
| `promy-crm` | `chore/align-precommit-hooks` | no | **PR #54 open**, CI ran green before the budget ran out |
| `promy-product` | `chore/align-precommit-hooks` | no | **PR #91 open**, red check is the **billing failure**, not a real one |
| `promy-user` | `chore/align-precommit-hooks` | no | **UNCOMMITTED** — `.pre-commit-config.yaml` (+13/−6) and `Makefile` (+1/−2) edited, nothing committed or pushed |
| `promy-event-bus` | `main` | no | not started |
| `promy-identifier` | `main` | no | not started |

**Also uncommitted and NOT ours — leave alone:** `promy-template-go/README.md` carries the maintainer's squash-merge policy notes and an ADR row.

### 2a. Per-repo specifics for the remaining work

Measured 2026-09-13. `test-hook` counts are from each repo's current working tree:

| Repo | duplicate `test` hook | `check-coverage` has `always_run` |
|---|---|---|
| `promy-crm` | already removed on its branch | yes |
| `promy-product` | already removed on its branch | yes |
| `promy-user` | already removed in worktree (uncommitted) | yes |
| `promy-event-bus` | never had one — correct already | **no — must add** |
| `promy-identifier` | never had one — correct already | **no — must add** |

---

## 3. Completing C3 stage 3 — full specification

Reference implementation: `promy-template-go` at commit `74a5fec`. Read its `.pre-commit-config.yaml` and `Makefile` before starting.

### The change, per repo

**1. `.pre-commit-config.yaml` — add at the very top, above `repos:`:**

```yaml
# `pre-commit install` only writes the hook types listed here. Without this line it writes
# .git/hooks/pre-commit alone, and the commit-msg and pre-push stages below never fire.
default_install_hook_types: [pre-commit, commit-msg, pre-push]
```

This single line is the root-cause fix. `make setup` historically ran only `pre-commit install` and `pre-commit install --hook-type commit-msg`, never `--hook-type pre-push` — which is why four of six repos had no pre-push gate and three had no `commit-msg` gate either.

**2. Add the shared branch guard as the first entry under `repos:`:**

```yaml
  - repo: https://github.com/tclavelloux/promy-github-workflows
    rev: hooks/v1.0.0
    hooks:
      - id: no-direct-commit-to-main
```

**`rev:` must be the immutable `hooks/v1.0.0`, never the moving `hooks/v1`.** See §5 — this is the single most likely mistake to repeat.

**3. `Makefile` — replace the `setup:` target:**

```make
setup:
	pre-commit install --install-hooks
```

`--install-hooks` pre-fetches hook environments so the first commit is not a surprise stall.

**4. Remove the duplicate `test` pre-push hook** — only where one exists (see §2a). `make check-coverage` already runs the full race suite before invoking `go-test-coverage`, so a separate `test` hook runs the suite **twice per push**.

**5. Ensure `check-coverage` carries `always_run: true`:**

```yaml
      - id: check-coverage
        name: check-coverage
        entry: make check-coverage
        language: system
        pass_filenames: false
        always_run: true
        stages: [pre-push]
```

Without `always_run`, pre-commit scopes pre-push hooks to files in the pushed range — so a push containing no `.go` files silently skips the coverage gate entirely. `promy-event-bus` and `promy-identifier` both need this added.

### Validation — required per repo

These repos shipped dead hooks for months. Do not ship a config you have not executed. In a throwaway `cp -R` copy under `/tmp` (never the real repo):

- `make setup`, then confirm `.git/hooks/` contains **all three**: `pre-commit`, `commit-msg`, `pre-push`.
- Commit on a feature branch → **allowed**.
- Commit on `main` → **blocked**.
- A non-conventional commit message → **rejected**.
- `pre-commit run --hook-stage pre-push --all-files` → `check-coverage` actually runs.
- Confirm **no** `[WARNING] ... mutable reference` appears. Its absence proves the `rev:` is right.

**Trap:** checking out `main` in a scratch repo also checks out `main`'s *old* config, which has no guard — producing a false pass. Ensure the branch you test on actually contains the change.

### Do not

- Do not touch `.github/`, `go.mod`, `go.sum`, Dockerfiles, `.testcoverage.yml`, `.golangci.yml`, or any Go source.
- Do not touch documentation — that is Phase F. **Report** which files and lines go stale.
- Do not add `default_stages:`. Hooks without explicit `stages:` currently run at both `pre-commit` and `commit-msg`; pre-existing, out of scope, noted in §7.

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
go-lint/v1  ·  go-vuln/v1  ·  go-coverage/v2  ·  go-docker/v1  ·  pr-title/v1  ·  hooks/v1.0.0
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

---

## 6. Remaining phases — execution order F, D, E

### F — documentation alignment

**Depends on C3 being finished.** Several of the worst items *are* hook documentation. Turnkey prompt: `PHASE-F-PROMPT.md`.

*Actively wrong:* `promy-template-go/TEMPLATE_INSTRUCTION.md` L56-59 instructs **"Do NOT run `pre-commit install`"** and L84 verifies `core.hooksPath` is set — both become instructions to recreate the configuration C3 removes. Also L38-47, L49-53, L61, L77, L88, L93. `scripts/setup-new-repo.sh` L58-64, L69-71 prints a `core.hooksPath` confirmation and the same warning.

*Factually false today:* **every `.testcoverage.yml` header** claims it is "read by both the pre-push hook and CI" — false in four repos. `promy-product`'s cites `.githooks/pre-push`, **a path that does not exist in that repo at all**.

*Stale:* `HOWTO.md` L27, L29, L58, L83, L85-89, L98; `README.md` L62, L69. Per-repo CI descriptions — `promy-template-go` lost its `build` job, all six gained `vuln` and `pr-title`, five gained `docker`.

**Documentation-only, so it qualifies for `[skip ci]`** — the phase to run first if budget is still tight.

### D — `workflow-sync-check`

The governance mechanism. Every drift this session began with was a documented guarantee with nothing enforcing it. The governance repo renders a canonical `ci.yml` per repo; each repo's CI diffs its own against it and fails on drift. Pull-based first — no cross-repo secrets, and a repo can only drift through a red PR. A push-based bot needs a PAT with write access to six repos; later.

**Not mechanical.** A naive check would flag the variation deliberately established. It must separate:
- *Governed invariants* — job names and structure, reusable-workflow refs and tags, `concurrency`, `permissions` scoping (`pull-requests: write` on coverage, `read` on gitleaks), draft-skip `if:` and trigger `types:`, artifact settings, absence of a `push: main` trigger, SHA pinning.
- *Measured locals* — `timeout-minutes`, presence of `docker:`, `services:` blocks, coverage floors.

Add a `# sync-exempt: <reason>` marker so justified deviations are recorded rather than silently tolerated.

Related: a `pre-commit-drift.yml` asserting `default_install_hook_types` is present and correct, required hook ids exist, no duplicate `test` hook, `Makefile setup:` contains no `core.hooksPath`, and no tracked `.githooks/` exists. Plus `pre-commit run --all-files --hook-stage pre-commit` in CI — the strongest available check, and the only thing recovering the file-hygiene gates CI otherwise lacks.

**Needs CI budget** — the check exists to run.

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
- **promy-github-workflows#6** — audit SHA pins, due 2027-03-12. Backstop for Dependabot PRs piling up unreviewed.
- **promy-user#68** — coverage differs by one statement between local (1416/1653) and CI (1415/1653). Gates nothing today, but `.testcoverage.yml` cites 85.7%, which CI does not reproduce — a ratchet to 86% would be flaky, not merely tight.
- **promy-crm#36** — quiet hours: an untestable clock **and** `IsInQuietHours` ignoring the `Timezone` field, so production applies a Paris user's window in UTC. Fixing the clock alone closes the issue with the production bug intact.
- **promy-github-workflows#13** — CLOSED. Squash-merge arbitrated fleet-wide; historical duplicates removed from `promy-event-bus`.

**Dependabot** — first run opened ~40 PRs; 38 remain, all correctly rebased to `SHA → SHA` with version comments maintained.
- ~30 Go module bumps. **Merge one at a time per repo** — each rewrites `go.sum`, conflicting the rest until rebase.
- **Config fix worth making first:** `dependabot.yml` has no `groups:`. Adding a `gomod` group collapses ~30 serialised merge-and-wait cycles into one PR per repo.
- 6 × `gitleaks-action` v2→v3 and ~2 × `release-please-action` v4→v5 deferred — majors needing caller validation, not a green check. `gitleaks-action` v2 required `GITLEAKS_LICENSE` for organisations; if v3 changed that, a green run on a personal repo proves nothing.

**Un-actioned findings**
- **`main` cannot be protected server-side.** `gh api .../branches/main/protection` returns `403 Upgrade to GitHub Pro` on these private free-plan repos. The local branch guard is the only control, and `git commit --no-verify` bypasses it. It is the strongest control available on this plan, not a strong one.
- **Hooks without explicit `stages:` run twice per commit** (pre-commit and commit-msg). `gitleaks` genuinely runs twice. Fix is `default_stages: [pre-commit]`.
- `FromAsCasing` buildkit warning (`as` vs `AS`) in `promy-product` and `promy-identifier` Dockerfiles. Non-fatal, pre-existing.
- `-v` still in several Makefile `test` targets now that CI dropped it.
- **Release-please PRs: leave them open to accumulate.** Decided 2026-09-13. Merging one cuts a release with no behavioural change; for `promy-event-bus` that would invite three pointless consumer bumps. Do not merge them reflexively as part of a sweep.

---

## 8. Git conventions in force

- Branch off `main`; never commit on `main`.
- `git commit -S` always. Never `--no-gpg-sign`, never `--no-verify`.
- Never `git add .` or `git add -u` — explicit paths only.
- **Squash-merge only, fleet-wide** (arbitrated 2026-09-13, #13). The PR title becomes the commit on `main` and is the only thing release-please parses, so it must be a clean Conventional Commits string. Atomicity lives at the PR boundary — splitting commits within a branch is cosmetic.
- End commit bodies with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`; end PR bodies with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
- Merging workflow-file PRs needs `workflow` scope, which the `GH_TOKEN` env var lacks. Prefix `gh` commands with `GH_TOKEN=` to fall back to the keyring token.
- Verify subagent claims independently before merging. Their reports have been accurate on substance but have twice overstated what was actually exercised.
