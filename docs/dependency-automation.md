# Dependency and vulnerability automation

## Why

- 2026-10-03 outage: two `// indirect` modules (otel sdk/otlptrace GO-2026-6505, grpc GO-2026-6348) failed every `vuln` job. `dependency-type: direct` never bumps them.
- Zero Dependabot PRs have ever merged; ~9-10 are open per repo (re-counted 2026-10-03). Each rewrites `go.sum`, so ungrouped they merge only one at a time.
- No branch protection: free private plan, `gh api .../branches/main/protection` returns 403.
- No PAT, by choice. Everything runs on `GITHUB_TOKEN`.

## Pieces

All tags are cut: `dependabot-automerge/v1(.0.0)`, `go-vuln-fix/v1(.0.0)`, `hooks/v1.1.0`.

| Piece | File | Tag | Fixes |
|---|---|---|---|
| Grouped Dependabot config | `templates/dependabot.yml` | none (copied per repo) | ~30 serial go.sum-conflicting PRs become one weekly PR; indirect deps covered |
| Automerge | `.github/workflows/dependabot-automerge.yml`, `templates/ci-automerge-job.yml` | `dependabot-automerge/v1` | Green patch/minor gomod PRs sit unmerged |
| Vuln fix | `.github/workflows/go-vuln-fix.yml`, `templates/go-vuln-fix-caller.yml` | `go-vuln-fix/v1` | Called advisories wait for a human to notice |
| Pre-push hook | `hooks/go-vuln.sh`, `.pre-commit-hooks.yaml` | `hooks/v1.1.0` | Advisories surface only after push |
| Local twin and CI guard | `scripts/go-vuln-fix.sh`, `.github/workflows/ci.yml` | none | Version and fix-logic drift |

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
- Fix PR commit uses GraphQL `createCommitOnBranch`: GitHub signs it and no credentials touch disk.
- Hook warns unless the pushed range touches `go.mod`/`go.sum`: an advisory published today must not block unrelated pushes, but a dependency edit is where the fix belongs. Manual runs always block.
- Hook sets `GOTOOLCHAIN` from go.mod: CI resolves the toolchain from go.mod, so local scans on a newer Go understated CVEs (template-go: 8 local vs 30 CI).

## Limitations

- `GITHUB_TOKEN`-created PRs and merges start no workflows.
  - Fix PRs have no CI until someone closes and reopens them.
  - The automerge squash does not fire release-please; its PR refreshes on the next human merge.
- No-PAT alternatives evaluated:
  - `workflow_dispatch` exception: rejected. With no PR payload, the draft check `github.event.pull_request.draft == false` evaluates false and skips every job, and coverage's PR comment breaks.
  - Dependabot security updates: PRs do trigger CI. Vulnerability alerts and automated security fixes are enabled in all six repos (verified via API 2026-10-03; `scripts/check-repo-settings.sh` re-checks). The `gomod-security` group is in the template.
- Nothing enforces required checks server-side. Automerge's own check-run verification is the only gate.
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
   - Make each gate fail once; a green check is not verification. Example: a Dependabot PR with a failing check must leave `automerge` skipped. Repeat for a major bump, a human-pushed extra commit and a `github-actions` PR.
