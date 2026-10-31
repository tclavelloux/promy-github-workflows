# Dependency and vulnerability automation

- No PAT, by choice. Everything runs on `GITHUB_TOKEN`, except vuln-fix and release PRs: the fleet GitHub App (`promy-fleet-bot`) opens them so CI runs.
- Ruleset `protect-main` on every repo: PR required, squash only, required checks (including `lint / lint`, which carries `workflow-sync`), Admin-only bypass.

## Pieces

| Piece | Files | Tag | Fixes |
|---|---|---|---|
| Grouped Dependabot config | `templates/dependabot.yml` | none (copied per repo) | Serial `go.sum`-conflicting PRs become one weekly PR; indirect deps covered |
| Automerge | `.github/workflows/dependabot-automerge.yml`, `templates/ci-automerge-job.yml` | `dependabot-automerge/v1` | Green patch/minor PRs sit unmerged |
| Go vuln fix | `.github/workflows/go-vuln-fix.yml`, `templates/go-vuln-fix-caller.yml` | `go-vuln-fix/v1` | Called advisories wait for a human to notice |
| Python vuln fix | `.github/workflows/python-vuln-fix.yml`, `templates/python/vuln-fix.yml` | `python-vuln-fix/v1` | OSV-only advisories (PYSEC, no GHSA) that Dependabot's `uv-security` group never fixes |
| Pre-push hooks | `hooks/go-vuln.sh`, `hooks/py-vuln.sh`, `.pre-commit-hooks.yaml` | `hooks/vX.Y.Z` | Advisories surface only after push |
| Local twins and CI guard | `scripts/go-vuln-fix.sh`, `scripts/python_vuln_fix.py`, `.github/workflows/ci.yml`, `internal/syncheck/python_workflows_test.go` | none | Version and fix-logic drift |

## Automerge gate

Logic: `scripts/automerge-gate.sh`, inlined in the workflow and kept byte-identical by `internal/syncheck/automerge_test.go`, which also runs the accept/refuse table through bash.

Merges only when all hold:

- Ecosystem is in `ecosystems` (`[a-z_-]` only; anything else refuses every PR).
- Dependabot: update type is in `merge-update-types` (`dependabot/fetch-metadata`, SHA-pinned).
- No dependency name matches `exclude-dependencies`: `*` `?` globs, case-insensitive, `.` `_` `-` equivalent, whole-name (`numpy` does not match `numpy-financial`). One match refuses the whole PR, grouped or not. Non-empty with no `dependency-names` fails closed.
- Every commit on the PR is authored by the PR's own bot, not only the first one.
- Each bot is tied to its branches: `dependabot/*` for Dependabot; `fix/vuln-*` (`go_modules`) and `fix/py-vuln-*` (`uv`) for `promy-fleet-bot[bot]`. A `GITHUB_TOKEN` PR (`github-actions[bot]`) never matches.
- Fleet-bot PRs: the decision reads the diff (merge base vs head), not the title or body; fetch-metadata is skipped and `merge-update-types` does not apply. Every changed file is a modified `go.mod`, `go.sum` or `uv.lock` (not added, not renamed); same set of requirements; at least one bump; every version moves within the same major.minor, never down (`go` directive included); no pre-release or pseudo-version; no other changed line (`replace`, `exclude`, `toolchain`).
- Every other check run on the head SHA is `completed` with `success`, `neutral` or `skipped`; no commit status is `failure`, `error` or `pending`. Polls `wait-attempts` x `wait-seconds` for runs outside this workflow (e.g. `pr-title.yml`).
- Head SHA unchanged at merge (`gh pr merge --squash --match-head-commit`).

Never merges:

- Major bumps, even if listed in `merge-update-types`.
- `github-actions` bumps: `GITHUB_TOKEN` cannot merge workflow-file changes. A human merges that group.
- release-please PRs.
- Anything a human touched (extra commit, other author).
- A `python-vuln-fix` PR whose resolver also moved another package by a minor (listed under "Also moved by the resolver"): refused, waits for a human.

Refusal: exit 0, `::notice` and step summary; a red check would duplicate the real failure.

The bot allowlist lives in the caller's `if:` (`AutomergeIf` in `policy.go`, rule `ci-automerge`), mirrored in the reusable job's `if:`. A repo without an automerge job gets nothing; add it from `templates/ci-automerge-job.yml`.

Trigger: a job inside the caller's `ci.yml`, so "all jobs succeeded" is native and human PRs skip it for free. Rejected: `pull_request_target` and `workflow_run` (zizmor `dangerous-triggers`; fetch-metadata reads only `pull_request` payloads), `check_suite` (never fires for suites created by Actions), a standalone poller (burns minutes).

The squash commit is pushed by `GITHUB_TOKEN`, so it starts no workflows: release-please's PR refreshes on the next human merge. Nothing is lost; release-please scans every commit since the last release and `build(deps)` is non-releasing.

## Vuln fix

Shared by `go-vuln-fix` and `python-vuln-fix`:

- Dedupe: branch `fix/vuln-<hash>` (Go) or `fix/py-vuln-<hash>` (Python), 12 hex of sha256 over the sorted advisory IDs (Python: project/ID pairs). An open PR on that branch skips the run. A closed-unmerged PR on it means rejected: never retried. A new advisory changes the hash and opens a fresh PR.
- A new PR closes older open bot PRs of the same prefix (by `github-actions[bot]` or the App) as superseded; the scan starts from current `main`, so it is a superset.
- The commit goes through GraphQL `createCommitOnBranch`: GitHub-signed, no credentials on disk.
- App token: with `APP_CLIENT_ID` and `APP_PRIVATE_KEY`, `actions/create-github-app-token` mints a token scoped to the calling repo (`contents` + `pull-requests` write); the App authors the PR and commit, so CI runs. A failed mint fails the run. Without the secrets, `GITHUB_TOKEN` opens the PR, CI does not run, and the PR body says so: close and reopen it before merging, or merge with `gh pr merge --admin`.
- Rolling issue and its comments always use `GITHUB_TOKEN`.
- Never merges itself; the caller's automerge job merges an App-opened PR under the [gate](#automerge-gate).
- Runs weekly (Thursday). Dependabot sweeps Monday, and the hooks plus each PR's `vuln` job already surface advisories.
- Every run, clean ones included, emits `::notice title=vuln-record::<date> <repo> <IDs|none> <action>` and a step-summary row. Measure frequency:

  ```bash
  GH_TOKEN= gh run list --workflow "Vuln fix" --limit 50 --json databaseId -q '.[].databaseId' \
    | xargs -I{} sh -c 'gh run view {} --log 2>/dev/null | grep vuln-record'
  ```

### `go-vuln-fix`

- Module findings: group by module, take the highest `fixed_version`, `go get`, `go mod tidy`, `go build ./...`, rescan. A fix that would change the `go` or `toolchain` directive goes to the issue.
- Go patch bump (`go-patch-bump`, default on): a called stdlib/toolchain advisory fixed in a newer patch of the `go` directive's minor (`go 1.26.8` -> `1.26.9`) bumps the directive in the same PR. Tidy, build and rescan run with `GOTOOLCHAIN=go<target>`; any claimed ID still called aborts and restores `go.mod`/`go.sum`. Minor bumps stay human.
- Patch bump refused (advisory goes to the issue): other minors, no fixed version, a `toolchain` directive, `go.work`, `.go-version`, a Go line in `.tool-versions`, a Dockerfile pinning `golang:X.Y.Z` or `@sha256:` (official images set `GOTOOLCHAIN=local`, so they cannot build a newer patch). Minor-tag Dockerfiles (`golang:1.26`) pass `go-docker.yml` and pick up the patch on rebuild. Dockerfiles are never edited.
- Uncovered stdlib/toolchain findings and module findings with no fix: one rolling issue per repo, marker `<!-- go-vuln-fix:stdlib -->`, with the refusal reason.
- The govulncheck version lives in `.govulncheck-version`, `go-vuln.yml` and `go-vuln-fix.yml`, and the fix body in the workflow and `scripts/go-vuln-fix.sh`: a reusable workflow cannot check out its own files at its pinned ref. `ci.yml` asserts both stay in sync.

### `python-vuln-fix`

- Per project, scan `uv.lock` (config lookup as in `python-vuln.yml`). Target per package: the highest of the lowest fixed versions across its advisory groups, from OSV `ECOSYSTEM` ranges containing the installed version (`GIT` ranges ignored).
- Per package: `uv lock --upgrade-package <pkg>==<target>` in the project directory, one at a time, lock restored on any refusal. Only `uv.lock` changes.
- Refused without calling uv: `exclude-dependencies` match (PEP 503-normalised, whole-name); a major bump (first release segment rises) unless `allow-major: true`; no fixed version. CalVer (first segment >= 1000 on both sides, e.g. certifi `2024.2.2` -> `2026.10.19`) is never a major.
- Refused after calling uv, lock restored: `No solution found` / `unsatisfiable`; the resolver picked another version; the change drags an excluded or major-bumped package along.
- Other uv failures (network, index, broken lock) fail the run and restore every lock; nothing is closed on a failed run.
- Other patch/minor moves of the resolver are kept and listed under "Also moved by the resolver".
- osv-scanner rescans each patched lock; a claimed ID still reported restores that project's lock and goes to the issue.
- Refusals and aborts: one rolling issue per repo, marker `<!-- python-vuln-fix:unfixed -->`, title `security: Python advisories need a human`. Never closed automatically.
- A run that finds nothing to fix closes open bot-opened `fix/py-vuln-*` PRs as stale, only when the scan completed and nothing aborted.
- uv version: the caller's `.uv-version`. `scripts/python_vuln_fix.py` (the locally testable twin, kept byte-identical by Go tests) exits 2 when `--uv` differs from it. Exit codes: 0 completed, 2 usage or scanner error, 4 uv error that is not a refusal.
- Keep Dependabot's `uv-security` groups: they fix GHSA-backed advisories faster; this workflow fills the OSV-only gap. Mirror the caller's automerge `exclude-dependencies` here.

### uv version and lock `revision`

- `.uv-version` is the single authority: the vuln-fix bot, CI, the uv-pre-commit hook, Dockerfiles and `make lock` read it.
- Its value equals the uv Dependabot bundles (dependabot-core `uv/helpers/requirements.txt`, not configurable): Dependabot is the one lock writer that cannot be pinned.
- uv >= 0.13.0 writes `revision = 5` in `uv.lock`; <= 0.12.x writes `revision = 3`. Every lock write by a disagreeing uv flips the line; a no-op `uv lock` keeps the file.
- `[tool.uv] required-version = "==X"` breaks Dependabot's updater. Keep `>=` floors.
- `pin-drift` reports when the pin and Dependabot's bundled uv differ, in either direction.

## Dependabot config

- `gomod` and `github-actions` groups take minor+patch; majors stay individual PRs for human review.
- `dependency-type: all` covers indirect modules (`direct` never bumps them).
- `cooldown: 3` delays version updates, not security updates. `go-vuln-fix` ignores cooldown.
- Dependabot security updates and vulnerability alerts are on in every repo (`scripts/check-repo-settings.sh` checks); the `gomod-security` group is in the template.

## Limitations

- `GITHUB_TOKEN`-created PRs and merges start no workflows.
- Release-please, `[skip ci]` and vuln-fix PRs opened without the App report no checks and need `gh pr merge --admin`.
- `automerge` is not a required check; `protect-main` enforces the required checks in addition to the gate's own verification.
- A `workflow_dispatch` trigger is not a no-PAT workaround: with no PR payload, `github.event.pull_request.draft == false` skips every job, and coverage's PR comment breaks.

## Onboard a repo

One PR, titled `ci: automate dependency bumps and vuln fixes`:

1. Replace `.github/dependabot.yml` with the profile's template (`templates/dependabot.yml`, `templates/python/dependabot.yml`).
2. Append `templates/ci-automerge-job.yml` to `ci.yml`, with every other job in `needs:`.
3. Add `.github/workflows/vuln-fix.yml` from `templates/go-vuln-fix-caller.yml` or `templates/python/vuln-fix.yml`.
4. Install the fleet App on the repo and add `APP_CLIENT_ID` and `APP_PRIVATE_KEY` as repo secrets. Skip it and fix PRs run no CI.
5. `.pre-commit-config.yaml`: latest immutable `hooks/vX.Y.Z`, `- id: go-vuln` or `- id: py-vuln`, plus the `vuln:` target (`templates/Makefile.vuln.mk` or `templates/python/Makefile`).
6. Merge with the keyring token: `GH_TOKEN= gh pr merge --squash`.

After merge:

- Insights → Dependency graph → Dependabot → "Check for updates" per ecosystem.
- When the grouped PR opens, close superseded individual minor/patch PRs with `@dependabot close`. Leave majors and release-please PRs open.
- Close or merge any human PR already fixing the current advisories before the first vuln-fix run.

## Verify

- Watch the first grouped PR's `automerge` job summary: expect a merge or a precise reason.
- Run `gh workflow run "Vuln fix"` once per repo; confirm the `vuln-record` line in the log.
- Test a caller change before merge: push it on a branch and run `gh workflow run vuln-fix.yml --ref <branch>`. The ref selects only the caller file; the scan and PR target `base-branch` (default: the repo default branch).
- Make each gate fail once; a green check is not verification. A Dependabot PR with a failing check must leave `automerge` skipped. Repeat for a major bump, a human-pushed extra commit and a `github-actions` PR.
