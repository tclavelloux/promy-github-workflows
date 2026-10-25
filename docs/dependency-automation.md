# Dependency and vulnerability automation

## Why

- 2026-10-03 outage: two `// indirect` modules (otel sdk/otlptrace GO-2026-6505, grpc GO-2026-6348) failed every `vuln` job. `dependency-type: direct` never bumps them.
- Zero Dependabot PRs have ever merged; ~9-10 are open per repo (re-counted 2026-10-03). Each rewrites `go.sum`, so ungrouped they merge only one at a time.
- Branch protection: ruleset `protect-main` on all seven repos since 2026-10-04 (issue #27). Required checks include `lint / lint`, which carries `workflow-sync`. Bypass is the Admin role only.
- No PAT, by choice. Everything runs on `GITHUB_TOKEN`, except vuln-fix PRs: a fleet GitHub App opens them so CI runs.

## Pieces

Tags: `dependabot-automerge/v1(.0.0, .1.0)`, `go-vuln-fix/v1(.0.0)`, `hooks/v1.4.0` are cut (`dependabot-automerge/v1.1.0` adds the `ecosystems` and `exclude-dependencies` inputs; `dependabot-automerge/v1.2.0`, not yet cut, adds `promy-fleet-bot[bot]` vuln-fix PRs). `go-vuln-fix/v1.1.0` (App token, go patch bump) is cut with the move of `go-vuln-fix/v1`.

| Piece | File | Tag | Fixes |
|---|---|---|---|
| Grouped Dependabot config | `templates/dependabot.yml` | none (copied per repo) | ~30 serial go.sum-conflicting PRs become one weekly PR; indirect deps covered |
| Automerge | `.github/workflows/dependabot-automerge.yml`, `templates/ci-automerge-job.yml` | `dependabot-automerge/v1` | Green patch/minor PRs sit unmerged (gomod by default; `ecosystems: uv` and `exclude-dependencies` from `v1.1.0`) |
| Vuln fix | `.github/workflows/go-vuln-fix.yml`, `templates/go-vuln-fix-caller.yml` | `go-vuln-fix/v1` | Called advisories wait for a human to notice |
| Python vuln fix | `.github/workflows/python-vuln-fix.yml`, `templates/python/vuln-fix.yml` | `python-vuln-fix/v1` | OSV-only advisories (PYSEC, no GHSA) that Dependabot's `uv-security` group never fixes wait for a human |
| Pre-push hook | `hooks/go-vuln.sh`, `.pre-commit-hooks.yaml` | `hooks/v1.1.0` | Advisories surface only after push |
| Local twin and CI guard | `scripts/go-vuln-fix.sh`, `scripts/python_vuln_fix.py`, `.github/workflows/ci.yml`, `internal/syncheck/python_workflows_test.go` | none | Version and fix-logic drift |

## Decisions

- Grouping: `gomod` and `github-actions` groups take minor+patch; majors stay individual PRs for human validation. `dependency-type: all` catches the indirect class; grouping holds the cost at one PR per week.
- Automerge trigger: last job of the caller's `ci.yml` with `needs:` on every other job.
  - `pull_request_target`: write token next to PR content; zizmor `dangerous-triggers`.
  - `workflow_run`: `dangerous-triggers`; fetch-metadata reads only `pull_request` payloads and reports "not from Dependabot".
  - `check_suite`: never fires for suites created by GitHub Actions.
  - Standalone `pull_request` poller: burns minutes while waiting.
  - In-CI job: native "all jobs succeeded" gate, skipped for free on human PRs. The workflow re-checks check runs, so a job missing from `needs:` cannot slip through.
- `github-actions` bumps are not auto-merged: `GITHUB_TOKEN` cannot merge workflow-file changes. A human merges that group.
- Vuln fix runs weekly on Thursday, not daily: advisories land every few weeks, Dependabot sweeps Monday, and the hook plus each PR's `vuln` job already surface them. Daily costs 7x for the same signal.
- The govulncheck version lives in three places (`.govulncheck-version`, `go-vuln.yml`, `go-vuln-fix.yml`) and the fix body in two, because a reusable workflow has no documented context exposing its own ref, so it cannot check out its own files at the pinned version. `ci.yml` asserts both stay in sync.
- Vuln-fix App token (#28):
  - Minted per run by `actions/create-github-app-token`, scoped to the calling repo with `contents` + `pull-requests` write; the issue step keeps `GITHUB_TOKEN`.
  - Optional secrets `APP_CLIENT_ID` + `APP_PRIVATE_KEY`; without them the run falls back to `GITHUB_TOKEN` and the PR body carries the "CI has not run" notice. A failed mint fails the run so a broken key is visible.
  - `client-id` replaces the deprecated `app-id` input.
  - Callers map the secrets explicitly; `workflow-sync` rejects `secrets: inherit`.
  - Automerge merges these PRs when authored by `promy-fleet-bot[bot]` (`dependabot-automerge/v1.2.0`): see "Fleet-bot automerge" below.
- Fleet-bot automerge (`dependabot-automerge/v1.2.0`):
  - Why: green vuln-fix PRs sat unmerged for days (promy-user #109 `go` 1.26.9 -> 1.26.10, same advisory open in six repos).
  - One allowlist: `dependabot[bot]`, `promy-fleet-bot[bot]`. It lives in the caller's `if:` (`AutomergeIf` in `policy.go`, enforced by `ci-automerge`), mirrored in the reusable job's `if:`. The commit-author check compares every commit with the PR opener, so a human commit on either bot's branch refuses.
  - Each bot is tied to its branches: `dependabot/*`; `fix/vuln-*` and `fix/py-vuln-*` for the fleet bot. A `GITHUB_TOKEN` PR (`github-actions[bot]`) never matches.
  - The fleet-bot decision reads the diff (merge base vs head), not the title or body, with fetch-metadata skipped: only modified `go.mod` / `go.sum` / `uv.lock`; same set of requirements/packages; versions move within the same major.minor and never down; no pre-release or pseudo-version; no other changed line (`replace`, `exclude`, `toolchain`); at least one bump. Branch and ecosystem must agree (`fix/vuln-*` = `go_modules`, `fix/py-vuln-*` = `uv`), then `ecosystems` and `exclude-dependencies` apply.
  - Consequence: a `python-vuln-fix` PR whose resolver also moved another package by a minor is refused (notice, job success) and waits for a human.
  - Check runs, commit statuses and `--match-head-commit` are unchanged (shared "Verify checks" / "Merge" steps).
  - Caller change: the job `if:` must admit `promy-fleet-bot[bot]`; `workflow-sync` rule `ci-automerge` enforces it fleet-wide. Repos without an automerge job get nothing; add it from `templates/ci-automerge-job.yml`.
- Go patch bump:
  - A called stdlib advisory fixed in a newer patch of the `go` directive's minor bumps the directive in the fix PR. Minor bumps stay human.
  - Fleet Dockerfiles use minor tags and `go-docker.yml` compares minors only, so no Dockerfile edit is needed.
  - Official golang images set `GOTOOLCHAIN=local`: a patch- or digest-pinned image cannot build a newer-patch module, so those repos are refused, as are repos with a `toolchain` directive, `go.work`, `.go-version` or a `.tool-versions` Go pin.
  - Tidy, build and the rescan run under `GOTOOLCHAIN=go<target>`; any claimed ID still called aborts the run and restores go.mod/go.sum.
  - `go-patch-bump: false` opts out.
- Python vuln fix (`python-vuln-fix`):
  - uv version: read from the caller's `.uv-version`, the pin `python-lint` enforces. Keep the pin equal to the uv version Dependabot writes locks with: measured 2026-10-22 on promy-market-catalog locks written by Dependabot's uv 0.12.18, a fix run with uv 0.11.13 rewrote 226 lines on `store` and 528 on `leaflet` (adds `marker = ...` to dependency entries), against 0 with uv 0.12.18. The locks stay valid and Dependabot rewrites them on its next PR, but the diff buries the real change.
  - Dependabot overlap: keep the `uv-security` groups. They fix GHSA-backed advisories faster; this workflow fills the OSV-only gap. Branch namespaces differ (`dependabot/uv/...` vs `fix/py-vuln-*`), and a run that finds nothing to fix closes a bot-opened `fix/py-vuln-*` PR, so a bump Dependabot landed first leaves no stale PR.
  - CalVer: the first release segment rising is a major, except when both sides are >= 1000 (certifi `2024.2.2` -> `2026.10.19`, tzdata). Without it every certifi advisory would need a human.
  - Cascades: uv may move other packages when it resolves a bump. A cascade into an excluded or major-bumped package restores the lock and goes to the issue; other moves are kept and listed in the PR.
  - The workflow has no merge step; a caller's automerge job merges the App-opened PR under the rules in "Fleet-bot automerge" (patch-level lock diff only). `exclude-dependencies` should mirror the caller's automerge exclusions.
  - Branches `fix/py-vuln-<hash>` are separate from Go's `fix/vuln-<hash>`: same dedupe, supersede and rejected-is-permanent rules.
- Fix PR commit uses GraphQL `createCommitOnBranch`: GitHub signs it and no credentials touch disk.
- Hook warns unless the pushed range touches `go.mod`/`go.sum`: an advisory published today must not block unrelated pushes, but a dependency edit is where the fix belongs. Manual runs always block.
- Hook sets `GOTOOLCHAIN` from go.mod: CI resolves the toolchain from go.mod, so local scans on a newer Go understated CVEs (template-go: 8 local vs 30 CI).

## Limitations

- `GITHUB_TOKEN`-created PRs and merges start no workflows.
  - Fix PRs have CI when the App secrets are set; without them they have none until someone closes and reopens them.
  - The automerge squash does not fire release-please; its PR refreshes on the next human merge.
- No-PAT alternatives evaluated:
  - `workflow_dispatch` exception: rejected. With no PR payload, the draft check `github.event.pull_request.draft == false` evaluates false and skips every job, and coverage's PR comment breaks.
  - Dependabot security updates: PRs do trigger CI. Vulnerability alerts and automated security fixes are enabled in all six repos (verified via API 2026-10-03; `scripts/check-repo-settings.sh` re-checks). The `gomod-security` group is in the template.
- Required checks are enforced server-side by `protect-main`, in addition to automerge's own check-run verification. `automerge` itself is not a required check. Release-please and `[skip ci]` PRs report no checks and need `gh pr merge --admin`; so do vuln-fix PRs opened without the App.
- promy-event-bus and the governance repo are both public (verified). Visibility is not the constraint: reusable workflows are referenced, not checked out, and a reusable workflow cannot see its own pinned ref to fetch sibling files.
- Closing a vuln-fix PR without merging is permanent for that advisory set: the same `fix/vuln-<hash>` branch is never reopened. A new advisory changes the hash and opens a fresh PR.
- `cooldown: 3` delays version updates, not security updates; a fix released < 3 days ago reaches the weekly group PR one week later. go-vuln-fix does not honour cooldown.
- Automerge, go-vuln-fix and the hook's pinned-`rev` resolution have never run on GitHub. Only their shell/jq logic was exercised locally (fixtures, crm/event-bus copies).

## Rollout

0. Governance repo:
   - Merge the `feat/bump-automation` PR.
   - Cut signed tags `dependabot-automerge/v1.0.0` + `dependabot-automerge/v1`, `go-vuln-fix/v1.0.0` + `go-vuln-fix/v1`, `hooks/v1.1.0`.
   - Leave the existing `hooks/v1` alias alone and never move `hooks/v1.0.0`. No caller may reference `hooks/v1`.
1. Per repo, in order: promy-event-bus (library; consumers depend on it), promy-template-go, promy-identifier, promy-crm, promy-product, promy-user. One PR each, titled `ci: automate dependency bumps and vuln fixes`:
   - Replace `.github/dependabot.yml` with `templates/dependabot.yml`.
   - Append `templates/ci-automerge-job.yml` to `ci.yml`, with every existing job in `needs:`.
   - Add `.github/workflows/vuln-fix.yml` from `templates/go-vuln-fix-caller.yml`.
   - Fleet App (once): create it with `contents` + `pull-requests` write, install it on the repos, add `APP_CLIENT_ID` and `APP_PRIVATE_KEY` as repo secrets. Skip it and vuln-fix PRs stay without CI.
   - Set `.pre-commit-config.yaml` rev to `hooks/v1.1.0` and add `- id: go-vuln`. When combined with the `workflow-sync` rollout, use `hooks/v1.2.0` and also add `- id: workflow-sync` (see [workflow-sync.md](workflow-sync.md)).
   - Add the `vuln:` target from `templates/Makefile.vuln.mk`.
   - Merge with the keyring token: `GH_TOKEN= gh pr merge --squash`.

   | Repo | `needs:` |
   |---|---|
   | promy-crm, promy-product, promy-user, promy-identifier, promy-template-go | `lint, vuln, docker, test, coverage, gitleaks` |
   | promy-event-bus | `lint, vuln, test, coverage, gitleaks` (no `docker`) |

   promy-crm has open human PR #57 `fix/bump-otel-grpc-advisories` covering the current advisories. Merge or close it before the first vuln-fix run to avoid a duplicate.
2. After merge, per repo:
   - Insights → Dependency graph → Dependabot → "Check for updates" for each ecosystem.
   - When the grouped PR opens, close superseded individual minor/patch PRs with a `@dependabot close` comment.
   - Leave major bumps and release-please PRs open.
3. First-run verification:
   - Watch the first grouped gomod PR's `automerge` job summary. Expect a merge or a precise reason.
   - Run `gh workflow run "Vuln fix"` once per repo (`workflow_dispatch` works with a user token). Confirm the `vuln-record` line in the run log.
   - To test a caller change (e.g. the `secrets:` block) before it merges, push it on a branch and dispatch from there: `gh workflow run vuln-fix.yml --ref <branch>`.
     - The ref selects only the caller file. `go-vuln-fix` scans and opens the PR against `base-branch` (default: the repository default branch), so the fix PR carries only `go.mod` and `go.sum`.
   - Make each gate fail once; a green check is not verification. Example: a Dependabot PR with a failing check must leave `automerge` skipped. Repeat for a major bump, a human-pushed extra commit and a `github-actions` PR.
