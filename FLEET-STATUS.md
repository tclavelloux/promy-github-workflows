# promy fleet CI/security — status

Paused 2026-09-13 on **GitHub Actions budget exhaustion**, mid-way through Phase C3 stage 3.

Scope: six Go repos (`promy-template-go`, `promy-crm`, `promy-event-bus`, `promy-identifier`, `promy-product`, `promy-user`) plus this governance repo. `promy-frontend` (Flutter) and `promy-market-catalog` (Python) are out of scope except where noted.

Began as a `ci.yml` audit. Found a live coverage-gate hole, then reachable CVEs in all six services, which took priority over the remaining CI work.

---

## STOP — read before resuming

**GitHub Actions has no remaining budget.** Jobs fail at startup with:

> The job was not started because recent account payments have failed or your spending limit needs to be increased.

This presents as a **2-second failed job with no runner and zero steps** — easy to mistake for a broken change. It is not. Confirm the budget has reset before interpreting any red check.

Until it resets, push with `[skip ci]` on its own line in the **commit body** (never the PR title — the title is the squash commit on `main` and is what release-please parses). Note `ci.yml` no longer triggers on `push: main`, so merging costs nothing; only PR runs consume minutes.

---

## Immediate state — exactly where it stopped

Phase C3 stage 3 aligns pre-commit config across the five service repos. Two PRs are open, one repo has **uncommitted work**, two are untouched.

| Repo | State | Action on resume |
|---|---|---|
| `promy-crm` | **PR #54 open**, CI ran green (pre-budget) | verify, merge |
| `promy-product` | **PR #91 open**, CI hit the billing failure — *not* a real failure | verify, merge |
| `promy-user` | **UNCOMMITTED** on branch `chore/align-precommit-hooks`: `.pre-commit-config.yaml` (+13/-6), `Makefile` (+1/-2). Guard present in worktree, absent on `main`. | finish, commit, push with `[skip ci]` |
| `promy-event-bus` | not started | do |
| `promy-identifier` | not started | do |

Also uncommitted, and **not ours** — leave alone: `promy-template-go/README.md` carries the maintainer's squash-merge policy notes and an ADR row.

### The stage 3 change, per repo

1. `.pre-commit-config.yaml` — add at the very top:
   ```yaml
   default_install_hook_types: [pre-commit, commit-msg, pre-push]
   ```
2. Add the shared branch guard as the first `repos:` entry:
   ```yaml
     - repo: https://github.com/tclavelloux/promy-github-workflows
       rev: hooks/v1.0.0
       hooks:
         - id: no-direct-commit-to-main
   ```
   **`rev:` must be the immutable `hooks/v1.0.0`, never `hooks/v1`** — see "Landmines" below.
3. `Makefile` — `setup:` becomes `pre-commit install --install-hooks`.
4. Remove the duplicate `test` pre-push hook in **crm, product, user only**. `make check-coverage` already runs the full race suite, so those three run it twice per push. `event-bus` and `identifier` already have the correct single-hook config — verify, do not "fix".
5. Ensure `check-coverage` carries `always_run: true`, or a push containing no `.go` files silently skips the coverage gate.

Reference implementation: `promy-template-go` at `74a5fec`.

---

## Shipped

### Plan 1 — `go-coverage.yml` v2

Thresholds and exclusions now come from each caller's own `.testcoverage.yml`; the inputs that duplicated them are gone. Inputs cut 7 → 3. `.testcoverage.yml` is mandatory — the job errors out rather than falling back to a zero-threshold config. Exclusions applied once, by the tool, against file paths. Sticky PR comment (PATCH in place), `❌` header with uncovered line ranges on failure. Gate runs `continue-on-error` so the comment posts first; a final step re-reads `steps.coverage.outcome` and re-raises.

Root bug: `promy-crm` gated CI at 55% while its pre-push hook gated at 88%, on a different denominator, since PR #34.

### Plan 2 Phase A — governance hardening

`go-lint.yml` gained `golangci-lint fmt --diff`, `config verify`, `go mod tidy -diff` — all three existed as local hooks and were absent from CI. All action refs SHA-pinned with version comments, `persist-credentials: false` on every checkout, `dependabot.yml`, and a blocking `zizmor` job (15 findings → 0).

### Tag scheme correction

Phase A shipped **inert**. Git tags are repo-wide: freezing bare `v1` for go-coverage's breaking change also froze `go-lint.yml@v1`, which all six callers referenced. Per-workflow namespaces now exist, all signed:

```
go-lint/v1  ·  go-vuln/v1  ·  go-coverage/v2  ·  go-docker/v1  ·  pr-title/v1  ·  hooks/v1.0.0
```

### Phase S — CVE remediation, 6/6

| Repo | Called CVEs (CI) | After |
|---|---|---|
| promy-template-go | 30 | 0 |
| promy-identifier | 17 | 0 |
| promy-user | 15 | 0 |
| promy-crm | 14 | 0 |
| promy-product | 9 | 0 |
| promy-event-bus | 8 | 0 |

All on `go 1.26.8`, Dockerfiles aligned to `golang:1.26`, `vuln:` job live. Six of event-bus's eight CVEs were **stdlib**, fixable only by the toolchain bump — 1.24/1.25 are outside Go's support window and never receive backports.

### Phase B — Docker validation + `ci.yml` hygiene, 6/6

`go-docker.yml` (static `Dockerfile`-vs-`go.mod` gate + `docker build`), duplicate `push: main` trigger dropped, `concurrency` + `cancel-in-progress`, measured `timeout-minutes`, draft-PR skip, `pull-requests` scoped per job, artifact retention, `-v` dropped.

### Phase C — supply chain + PR titles

C1 (caller actions SHA-pinned + Dependabot) and C2 (`pr-title.yml`) are **6/6**. `release-please.yml` pinned 6/6 to `@5c625bf # v4.4.1`. C3 stages 1 and 2 done — shared hook published and tagged, `promy-template-go` migrated off `.githooks/`.

---

## Landmines — each cost real time to find

**`rev: hooks/v1` is wrong for pre-commit.** Workflows and pre-commit resolve refs oppositely: `uses:` re-resolves every run (moving alias correct); `rev:` resolves **once**, caches in `~/.cache/pre-commit`, and never re-resolves (immutable tag correct). A moving tag warns on every invocation and freezes each developer at first install. Always `hooks/vX.Y.Z`.

**`startup_failure` = zero jobs, no log.** Every repo's default workflow token is `contents: read`. A called workflow can only *narrow* the caller's token, so a caller job requesting `pull-requests: read` without its own `permissions:` block is an escalation and the run is refused before any job starts. Discriminator: job count.

**`gitleaks-action@v2` needs `pull-requests: read`.** It calls `GET /pulls/{n}/commits` to scope its scan. Removing the grant 403s the job in ~8s.

**`setup-go@v7` changed `GOTOOLCHAIN` from `auto` to `local`**, breaking `go install` of any tool needing a newer Go. Fixed by setting it explicitly. `actionlint` and `zizmor` both passed on the broken version.

**`gh run rerun` does NOT re-resolve reusable-workflow refs.** It replays the SHA pinned at dispatch. Re-running to pick up a `@main` change silently re-tests the old code and looks like a failed fix. Push a commit instead.

**Local CVE counts understate CI.** `setup-go` resolves the toolchain from `go.mod`, so CI ran older stdlibs than local scans. `promy-template-go`: 8 local vs **30** in CI.

**`gh pr checks --json state` exits 8 while pending** and kills naive poll loops. Poll `gh run view --json status` instead.

**Checking out `main` in a scratch repo also checks out `main`'s old config**, producing a false pass when testing the branch guard. Ensure the test branch contains the change.

**Don't normalise the `timeout-minutes` values.** They are 15/25/20/10/8/6, each measured from that repo's own runtimes. Two agents have proposed unifying them. The inconsistency is the correctness.

---

## Remaining phases — execution order F, D, E

### F — documentation alignment

**Depends on C3.** Several of the worst items *are* hook documentation. A turnkey prompt is in `PHASE-F-PROMPT.md` in this repo.

Actively wrong under C3: `promy-template-go/TEMPLATE_INSTRUCTION.md` L56-58 instructs **"Do NOT run `pre-commit install`"**, and L84's checklist verifies `core.hooksPath` is set. L38-47, L49-53, L61, L77, L88, L93 also stale. `HOWTO.md` L27, L29, L58, L83, L85-89, L98. `README.md` L62, L69. `scripts/setup-new-repo.sh` L58-64, L69-71.

Factually false today, independent of C3: **every `.testcoverage.yml` header** claims the file is "read by both the pre-push hook and CI" — false in four repos. `promy-product`'s cites `.githooks/pre-push`, a path that does not exist in that repo at all.

Stale CI descriptions: `promy-template-go` lost its `build` job; all six gained `vuln` and `pr-title`; five gained `docker`.

**Documentation-only, so it qualifies for `[skip ci]`** — the phase to do first while the budget is constrained.

### D — `workflow-sync-check`

The governance mechanism. Every drift this session began with was a documented guarantee with nothing enforcing it. The governance repo renders a canonical `ci.yml` per repo; each repo's CI diffs its own against it and fails on drift. Pull-based first — no cross-repo secrets, and a repo can only drift through a red PR.

**Not mechanical.** A naive check would flag the variation we deliberately established. It must separate **governed invariants** (job names and structure, workflow refs and tags, `concurrency`, `permissions` scoping, draft-skip `if:` and trigger `types:`, artifact settings, absence of a `push: main` trigger, SHA pinning) from **measured locals** (`timeout-minutes`, presence of `docker:`, `services:` blocks, coverage floors). Add a `# sync-exempt: <reason>` marker so justified deviations are recorded rather than tolerated.

Related: a `pre-commit-drift.yml` asserting `default_install_hook_types` is correct, required hook ids exist, no duplicate `test` hook, and `Makefile setup:` contains no `core.hooksPath`.

**Needs CI budget** — the check exists to run.

### E — `promy-market-catalog`

**Zero CI**, but the blocker is more fundamental. Surveyed: 74 tracked `.py` files, Python 3.12, **zero first-party tests**, and **no dependency manifest of any kind** — no `pyproject.toml`, `requirements.txt`, or lockfile. `src/` holds `leaflet`, `store`, `template`, `yolo`; an ML/CV pipeline, not a service.

CI cannot be added until a manifest exists — there is nothing to install from. Order: generate and curate a manifest → choose the toolchain (`uv` fits the fleet's pinned-tooling pattern) → add lint/format CI → add tests → only then a coverage floor. A floor over zero tests is 0% and meaningless.

**Treat E as the largest of the three**, not the smallest. Scoping it as a quick win produces a green badge over an ungated repo.

---

## Open issues

- **promy-github-workflows#6** — audit SHA pins, due 2027-03-12. Backstop for Dependabot PRs piling up unreviewed.
- **promy-user#68** — coverage differs by one statement between local (1416/1653) and CI (1415/1653). Gates nothing today, but `.testcoverage.yml` cites 85.7%, which CI does not reproduce — a ratchet to 86% would be flaky, not merely tight.
- **promy-crm#36** — quiet hours: untestable clock **and** `IsInQuietHours` ignoring the `Timezone` field, so production applies a Paris user's window in UTC. Fixing the clock alone closes the issue with the production bug intact.
- **promy-github-workflows#13** — CLOSED. Squash-merge arbitrated fleet-wide; historical duplicates removed.

## Dependabot

First run opened ~40 PRs; 38 remain. All rebased correctly to `SHA → SHA` with version comments maintained.

- ~30 Go module bumps. **Merge one at a time per repo** — each rewrites `go.sum`, conflicting the rest until rebase.
- **Config fix worth making first:** `dependabot.yml` has no `groups:`. Adding a `gomod` group collapses ~30 serialised merge-and-wait cycles into one PR per repo.
- 6 × `gitleaks-action` v2→v3 and ~2 × `release-please-action` v4→v5 deferred — majors needing caller validation, not a green check.

## Findings not yet actioned

- **Pre-push hooks installed in only 2 of 6 repos** before C3; `commit-msg` dead in three. Root cause was `make setup` never running `pre-commit install --hook-type pre-push`. C3 fixes it.
- **`main` cannot be protected server-side** — `gh api .../branches/main/protection` returns `403 Upgrade to GitHub Pro` on these private free-plan repos. The local branch guard is the only control. `git commit --no-verify` bypasses it.
- **Hooks without explicit `stages:` run twice per commit** (pre-commit and commit-msg). `gitleaks` genuinely runs twice. Fix is `default_stages: [pre-commit]`.
- `FromAsCasing` buildkit warning in `promy-product` and `promy-identifier` Dockerfiles. Non-fatal.
- `-v` still in several Makefile `test` targets now that CI dropped it.
- **Release-please PRs: leave them open to accumulate.** Decided 2026-09-13. Merging one cuts a release with no behavioural change; for `promy-event-bus` that would invite three pointless consumer bumps. Do not merge them reflexively as part of a sweep.
