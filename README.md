# promy-github-workflows

Reusable GitHub Actions workflows shared across `promy-*` services. Single-purpose repo — `main` only ever changes for CI-infra reasons, so a caller's gate never breaks for reasons unrelated to CI.

## `go-coverage.yml`

Gates a pre-computed coverage profile on `vladopajic/go-test-coverage` and posts a per-package + low-coverage breakdown as a PR comment. It does **not** run your tests — the caller runs `go test -coverprofile=...` itself (with whatever service containers it needs — MySQL, Redis, ...) and uploads the raw profile as an artifact. This keeps the shared workflow decoupled from any repo's service dependencies, and avoids running the test suite twice per CI run.

The workflow checks out the **caller's** repo (a plain `actions/checkout@v4` inside a reusable workflow resolves to the calling repository and ref, not to `promy-github-workflows`) and reads the caller's own `.testcoverage.yml` and `go.mod` directly:

- Thresholds and exclusions are gated by `go-test-coverage --config=.testcoverage.yml` — the same file your pre-push hook reads. There is exactly one place regex exclusions are evaluated, and it evaluates them against file paths (so `main\.go$` works as expected).
- `module-prefix` is derived from the caller's `go.mod` (`go list -m`) instead of being passed in.

```yaml
test:
  runs-on: ubuntu-latest
  # add a `services:` block here if your tests need Redis/MySQL/etc.
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version-file: go.mod
    - name: Run tests
      run: go test -race -failfast -coverpkg=./... -covermode=atomic -coverprofile=coverage.raw.out -count=1 ./...
    - uses: actions/upload-artifact@v4
      with:
        name: coverage-profile
        path: coverage.raw.out

coverage:
  needs: test
  uses: tclavelloux/promy-github-workflows/.github/workflows/go-coverage.yml@go-coverage/v2
  secrets: inherit
```

Nothing else is required as long as the caller has a `.testcoverage.yml` at its repo root (see `promy-crm`'s for the canonical shape: `profile`, `threshold.total`/`.package`/`.file`, `exclude.paths`).

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `post-pr-comment` | `true` | Post/refresh the coverage breakdown PR comment |
| `coverage-artifact` | `coverage-profile` | Name of the artifact uploaded by the caller's `test` job |
| `profile-filename` | `coverage.raw.out` | Filename of the coverage profile inside that artifact |

No threshold or exclusion inputs exist. `go-coverage/v1` had `total-threshold`, `package-threshold`, `exclude-patterns` and `module-prefix`; every one of them duplicated a value the caller already declares in `.testcoverage.yml` or `go.mod`, and `go-test-coverage` treats each `threshold-*` flag as an override of the config file. `promy-crm` drifted 33 points between the two for months. An input that *can* contradict the source of truth eventually will, so `go-coverage/v2` removes the ability.

`.testcoverage.yml` at the caller's repo root is mandatory. The workflow fails with an explicit error if it is absent, rather than letting `go-test-coverage` fall back to a zero-threshold config and pass everything.

The PR comment is one sticky comment, updated in place on each push rather than deleted and reposted:

- Per-package breakdown, rolled up from `go-test-coverage`'s own `--breakdown-file-name` output. Statement-weighted, so a 3-statement file does not count as much as a 300-statement one.
- On failure only, the tool's own report: every file below threshold with its uncovered line ranges.

The gate step runs with `continue-on-error` so the comment posts before the check goes red; a final step restores the failure.

There is no function-level section. It required re-reading the raw, unexclusion-filtered profile with `go tool cover -func` and allowlisting it back down — the one place where the workflow still had to reconcile two different path formats. The tool's uncovered-line ranges are more actionable and cost no bespoke parsing.

## `go-lint.yml`

Installs a fixed `golangci-lint` version via `go install` and runs it. This is the single place the fleet's linter version is decided — bumping the `golangci-lint-version` default here is one PR, not six. Deliberately mirrors `go-coverage.yml`'s `workflow_call` shape (same input-description style, same step naming) but installs from source rather than a prebuilt binary, matching `promy-template-go/scripts/golangci-lint.sh` — so "local matches CI" is literally true, not just intended.

```yaml
jobs:
  lint:
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-lint.yml@go-lint/v1
```

A reusable workflow is called at the **job** level. It cannot be a `steps:` entry — `uses:` inside `steps:` resolves actions, not workflows.

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `golangci-lint-version` | `v2.13.2` | Version to install (with or without a leading `v`) |
| `timeout` | `5m` | `golangci-lint --timeout` value. A Go duration string, not a number. |
| `go-version-file` | `go.mod` | Path used to resolve the Go toolchain version |
| `args` | `""` | Extra one-off flags appended to `golangci-lint run`. Repo-wide linter behavior (enabled linters, exclusions, settings) belongs in the caller's own `.golangci.yml` — this input is for one-off flags only. Flags are word-split on whitespace but not glob-expanded, so `--skip-dirs vendor/*` reaches `golangci-lint` literally instead of expanding against the runner's filesystem. |
| `workflow-sync` | `false` | Run the fleet `workflow-sync` pre-commit hook at the caller's pinned `rev:` over the whole repo, as the last step (runs even if an earlier step failed). Requires `- id: workflow-sync` in the caller's `.pre-commit-config.yaml`. See [docs/workflow-sync.md](docs/workflow-sync.md). |

Requires only `contents: read` — unlike `go-coverage.yml`, it posts nothing and needs no `pull-requests: write`.

Beyond `golangci-lint run`, the job also blocks on three gates that previously only ran as local pre-commit hooks (so pushing without hooks installed bypassed them):

- `golangci-lint fmt --diff` — fails if the caller isn't formatted per its own `.golangci.yml`.
- `golangci-lint config verify` — fails if the caller's `.golangci.yml` doesn't validate against the resolved `golangci-lint` version's schema.
- `go mod tidy -diff` — fails if `go.mod`/`go.sum` don't match what `go mod tidy` would produce. Requires Go 1.23+.

All three print a diff and exit non-zero on drift, exit 0 clean — verified directly against `golangci-lint v2.13.2` and `go1.26`, not assumed.

`go-lint.yml` accepts `${{ inputs.* }}` into `run:` steps via an `env:` mapping rather than interpolating them directly into the shell string, specifically to close the shell-injection surface that direct interpolation opens (a caller-controlled `args` or `golangci-lint-version` string could otherwise break out of the intended command). `go-coverage.yml` now follows the same convention throughout.

If the caller has a `.golangci-version` file (see `promy-template-go`), the workflow emits an `::warning` — never a failure — when it drifts from the resolved `golangci-lint-version`. A warning, not a hard failure, so a version bump here doesn't turn all six repos red until each lands its own PR: that would convert free propagation into six mandatory PRs.

### pre-commit consumers pin immutably — the opposite of workflow consumers

Workflows and pre-commit hooks resolve refs differently, so they take opposite conventions:

| | resolution | correct ref |
|---|---|---|
| `uses:` in a workflow | re-resolved **every run** | moving major alias (`go-lint/v1`) |
| `rev:` in `.pre-commit-config.yaml` | resolved **once**, then cached in `~/.cache/pre-commit` and never re-resolved | immutable tag (`hooks/v1.0.0`) |

Point a caller at the moving `hooks/v1` and pre-commit warns on every invocation:

```
[WARNING] The 'rev' field ... appears to be a mutable reference (moving tag / branch).
Mutable references are never updated after first install and are not supported.
```

It means what it says. Once a developer's cache has resolved `hooks/v1`, moving the tag ships them nothing — they stay on the old script permanently, with no signal. The moving-alias convention that makes workflow bumps free is actively harmful here.

So: cut `hooks/vX.Y.Z` for every hook change, and let callers pick it up with `pre-commit autoupdate`. The `hooks/v1` alias exists for symmetry with the workflow tags but should not appear in a `rev:`.

### Versioning

Tag releases rather than pinning consumers to `@main` — a breaking change to a workflow should require an explicit opt-in bump in each caller, not a silent flip across every `promy-*` service on the next push.

Each workflow is versioned in its **own tag namespace**: `<workflow>/vX` (moving major alias) and `<workflow>/vX.Y.Z` (immutable) — e.g. `go-lint/v1`, `go-lint/v1.1.0`, `go-coverage/v2`, `go-coverage/v2.0.1`, `go-vuln/v1`, `go-vuln/v1.0.0`. A caller tracks the moving alias for its workflow:

```yaml
uses: tclavelloux/promy-github-workflows/.github/workflows/go-lint.yml@go-lint/v1
```

**Why namespaced, not a bare `v1`/`v2`:** a git tag is repo-wide, not per-file. A bare `v1` is one ref shared by every workflow in this repo, so freezing it for one workflow's breaking change freezes all of them — there is no way to keep `go-lint.yml` moving while `go-coverage.yml` is pinned to a frozen `v1`. That is exactly what happened: freezing bare `v1` to cut `go-coverage@v2` silently froze `go-lint` too, and an additive `go-lint.yml` change (the Phase A gates) shipped to `main` without ever reaching a single caller, because no caller's `@v1` moved. Per-workflow tag namespaces make each workflow's major independently movable, so this can't recur.

A moving major alias gains additive, backward-compatible changes (new optional inputs, new default versions) without a new tag. Immutable `vX.Y.Z` tags exist alongside it for any caller that needs to freeze at an exact revision. This is deliberate: the entire value proposition of this repo — "a version bump is one PR here, not six" — requires callers to track a moving ref. If a major were frozen at first release, the next tool-version bump would cost seven PRs (one here, plus one per caller to re-pin), not six.

The corollary is that a semantic change must not ride a moving alias. `go-coverage.yml@go-coverage/v2` moves exclusions from a workflow input to the caller's `.testcoverage.yml`; shipping that on `go-coverage/v1` would have flipped the exclusion source under all six services with no opt-in. `go-coverage/v1` is therefore frozen at its final revision and `go-coverage/v2` is the migration target.

The bare `v1`, `v1.1.0`, `v2`, `v2.0.0` tags are **deprecated** — retained only so nothing on an old ref breaks mid-migration. No caller should reference them; use the namespaced tags above.

### Current majors

| Workflow | Major | Notes |
|---|---|---|
| `go-coverage.yml` | `go-coverage/v2` | `go-coverage/v1` frozen. Migration: delete `total-threshold`, `package-threshold`, `exclude-patterns` and `module-prefix` from the caller and bump the ref. Confirm the caller's `.testcoverage.yml` already carries the intended values first — they are now the only ones that apply. |
| `go-lint.yml` | `go-lint/v1` | Own tag namespace; unaffected by the `go-coverage.yml` major bump. |
| `go-vuln.yml` | `go-vuln/v1` | New; own tag namespace, same as the other two. |
| `go-docker.yml` | `go-docker/v1` | Own tag namespace, same as the others. Immutable `go-docker/v1.0.0` alongside. |
| `pr-title.yml` | `pr-title/v1` | Own tag namespace, same as the others. Tag cut when this workflow lands on `main`. |
| `dependabot-automerge.yml` | `dependabot-automerge/v1` | Own tag namespace, same as the others. Immutable `v1.0.0`, `v1.1.0` (`ecosystems`, `exclude-dependencies`), `v1.2.0` (`promy-fleet-bot` vuln-fix PRs) alongside. `workflow-sync` requires the two-bot caller `if:` from `hooks/v1.6.0`. |
| `go-vuln-fix.yml` | `go-vuln-fix/v1` | Own tag namespace, same as the others. Immutable `go-vuln-fix/v1.1.0` alongside (adds the optional App secrets and `go-patch-bump`). |
| `python-lint.yml` | `python-lint/v1` | New; own tag namespace. `RuffVersion` in `policy.go` ties to its `ruff-version` default. |
| `python-typecheck.yml` | `python-typecheck/v1` | New; own tag namespace. |
| `python-vuln.yml` | `python-vuln/v1` | New; own tag namespace. `OSVScannerVersion` in `policy.go` ties to its default. |
| `python-test.yml` | `python-test/v1` | New; own tag namespace. |
| `python-docker.yml` | `python-docker/v1` | New; own tag namespace. |
| `python-vuln-fix.yml` | `python-vuln-fix/v1` | New; own tag namespace. Writes (App token, PR, issue), so it is not one of the five read-only Python workflows. `OSVScannerVersion` in `policy.go` and `python-vuln.yml` tie to its `osv-scanner-*` defaults. `workflow-sync` checks its caller from `hooks/v1.5.0`. |
| `release-please.yml` | `release-please/v1` | New; own tag namespace. `workflow-sync` checks its caller from `hooks/v1.6.0`. |
| `.pre-commit-hooks.yaml` | `hooks/v1.6.0` (latest: `workflow-sync` requires the reusable release-please caller and the two-bot automerge `if:`; `hooks/v1.5.x` checks the python-vuln-fix caller; `hooks/v1.4.0` adds `py-vuln` and the Python profile of `workflow-sync`; `hooks/v1.3.0` accepts the go-vuln-fix App secrets; `hooks/v1.2.0` added `workflow-sync`; `hooks/v1.1.0` added `go-vuln`) | Not a workflow — the pre-commit hook manifest at the repo root. One namespace for every hook it declares, since pre-commit resolves the whole repo at one `rev`. **Callers pin the immutable tag, not the moving `hooks/v1` alias** — see below. |

## `go-vuln.yml`

Runs `govulncheck` over the caller's module and fails on any **called** vulnerability (one actually reachable from the caller's code, not merely present in `go.sum`). Deliberately **opt-in**: unlike `go-lint.yml`'s new formatting/tidiness gates, this is not wired into any other reusable workflow, so adopting it is a one-line job addition in each caller's own `ci.yml` rather than an instant fleet-wide gate on the next push.

```yaml
jobs:
  vuln:
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-vuln.yml@go-vuln/v1
```

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `govulncheck-version` | `v1.8.0` | Version to install (with or without a leading `v`) |
| `go-version-file` | `go.mod` | Path used to resolve the Go toolchain version |
| `args` | `./...` | Package pattern(s)/flags passed to `govulncheck` |

Requires only `contents: read` — it posts nothing and needs no `pull-requests: write`.


## `go-docker.yml`

Catches Dockerfile/`go.mod` drift, then proves the image still builds. Opt-in, one job per caller.

`promy-crm` bumped its `go` directive to 1.26.8 while its Dockerfile still said `golang:1.24`. CI was green — no job in any repo built the image — and the Railway deployment failed. `promy-template-go` shipped a 1.25 module on a `golang:1.23` image for months for the same reason.

```yaml
jobs:
  docker:
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-docker.yml@go-docker/v1
```

`promy-event-bus` is a library with no Dockerfile and does not adopt this.

### What it checks

**1. Consistency gate.** Compares each Dockerfile's `golang:` base-image minor against the `go` directive in `go.mod`. Static, no network, about a second. Patch levels are ignored — `golang:1.26` against `go 1.26.8` passes, because the `golang` image publishes X.Y tags.

Per case:

| Case | Result | Why |
|---|---|---|
| Base minor ≠ `go` directive minor | **fail** | The drift this workflow exists to catch |
| No `golang:` line in the file | **pass**, `::notice` | Pulls no Go toolchain, so nothing can drift. Multistage final stages (`gcr.io/distroless/base-debian10`) are exactly this |
| Several `golang:` lines in one file | **every one is checked** | A multistage build can carry more than one Go stage; checking only the first lets a later stage drift undetected |
| Listed file does not exist | **fail** | Skipping it would let a renamed or deleted Dockerfile turn the gate into a silent no-op. Set `dockerfiles` to the files the repo actually ships |
| `golang:latest` or any tag with no X.Y | **fail** | Unparseable, therefore uncheckable — pin a concrete minor |

**2. `docker build`, no push.** Builds each Dockerfile to prove it compiles. Secondary to the gate, and skippable via `build: false`.

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `dockerfiles` | `Dockerfile dev.Dockerfile` | Space-separated paths to check and build. Every listed file must exist |
| `build` | `true` | Run `docker build` after the gate. `false` keeps the gate alone |
| `go-version-file` | `go.mod` | Path whose `go` directive the base images are compared against |

Requires only `contents: read` — it pushes nothing and posts nothing.

### Railway parity is unverified

A GitHub-hosted runner can succeed where Railway fails. If Railway restricts egress to `proxy.golang.org`, it cannot auto-download a newer toolchain, so a `golang:1.24` image building a `go 1.26.8` module breaks there and not here. That asymmetry is why the static gate is the primary check and the build is secondary — the gate fails on the mismatch regardless of whether any builder tolerates it.

## `pr-title.yml`

Fails the PR when its title is not a Conventional Commits string. Wraps `amannn/action-semantic-pull-request`.

The fleet squash-merges, so the PR title *is* the commit message on `main` — and the only string release-please parses to derive the next version and write the CHANGELOG. The `commit-msg` hook never sees a PR title, so branch-level conventional commits are not a backstop. A malformed title ships a malformed release.

```yaml
name: PR Title

on:
  pull_request:
    types: [opened, edited, reopened, synchronize]

permissions:
  contents: read

jobs:
  pr-title:
    permissions:
      contents: read
      pull-requests: read
    uses: tclavelloux/promy-github-workflows/.github/workflows/pr-title.yml@pr-title/v1
```

Copy the block verbatim. Three parts of it are load-bearing:

- **`permissions:` on the calling job is mandatory.** The `promy-*` repos set their default workflow token to *read repository contents and packages* — which is `pull-requests: none`. A called workflow can only narrow the caller's token, never widen it, so a caller that omits the job-level grant does not fail the job: the run never starts. It reports `startup_failure` with no jobs and no log, which reads like an infrastructure blip rather than a config error. Verified against `promy-template-go`: identical caller, `startup_failure` without the block, green with it. `go-coverage.yml`'s caller carries the same block for the same reason.

- **`edited` is mandatory.** GitHub's default `pull_request` types are `opened`, `synchronize`, `reopened` — a title change fires none of them. Without `edited`, the check goes red on a bad title and stays red forever, because fixing the title dispatches no run. Verified: an edit-only title fix with `edited` present dispatched a new run and flipped the check green.
- **`pull_request`, not `pull_request_target`.** `promy-frontend` uses `pull_request_target`; that is only required to hand a fork PR a token with write scope or repo secrets. This workflow holds `pull-requests: read`, posts nothing, and checks nothing out. The `promy-*` repos are private and single-maintainer, so fork PRs do not occur — and `pull_request_target` runs the base-branch workflow with an elevated token against untrusted head content, which is the pattern zizmor exists to flag. `pull_request` is strictly the safer ref for the same result.

A reusable workflow is callable from either trigger — `workflow_call` constrains the job, not the caller's event.

`synchronize` is not required for correctness (a push does not change the title) but keeps the check present on every head SHA, so a branch-protection rule requiring it never blocks on a missing run.

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `types` | `feat fix docs style refactor perf test build ci chore revert`, newline-separated | Allowed types. Each entry is a regex the action auto-wraps in `^ $`. |
| `require-scope` | `false` | Require `feat(api): ...` over `feat: ...` |
| `subject-pattern` | `^.+$` | Regex the subject must match. The default only rejects an empty subject; tighten per-caller (`^(?![A-Z]).+$` bans a leading capital). |
| `subject-pattern-error` | see workflow | Message shown when `subject-pattern` fails. `{subject}` and `{title}` are substituted by the action. |

Every default matches what `promy-frontend`'s standalone `enforce-conventional-pr-title.yml` already enforces, so adopting this needs no `with:` block.

Requires only `pull-requests: read` — it reads the title off the event payload and reports a check run. It posts no comment and performs no checkout.

## `dependabot-automerge.yml`

Squash-merges a Dependabot or `promy-fleet-bot` vuln-fix PR (Go modules by default; `uv` and others opt-in) once every check on its head commit is green. Tag `dependabot-automerge/v1`. Rollout and limits: [docs/dependency-automation.md](docs/dependency-automation.md).

Call it as the **last job of the caller's own `ci.yml`** (`on: pull_request`), with `needs:` listing every other job:

```yaml
  # List EVERY other job in `needs:`. The in-workflow check-run verification is
  # the backstop, not the primary gate.
  automerge:
    needs: [lint, vuln]
    if: github.event.pull_request.user.login == 'dependabot[bot]' || github.event.pull_request.user.login == 'promy-fleet-bot[bot]'
    permissions:
      contents: write
      pull-requests: write
      checks: read
      actions: read
      statuses: read
    uses: tclavelloux/promy-github-workflows/.github/workflows/dependabot-automerge.yml@dependabot-automerge/v1
```

Inputs (all optional; the defaults are the Go behaviour, so Go callers pass none):

| Input | Default | Meaning |
|---|---|---|
| `merge-update-types` | `version-update:semver-patch version-update:semver-minor` | Space-separated allowlist of fetch-metadata `update-type`. A major is rejected even if listed. |
| `ecosystems` | `go_modules` | Comma-separated allowlist of Dependabot `package-ecosystem` values (`go_modules`, `uv`, `pip`, `docker`, ...). `[a-z_-]` only; anything else refuses every PR. `github_actions` is refused even if listed. |
| `exclude-dependencies` | empty | Newline- or comma-separated globs (`torch*`, `numpy`; `*` `?` only). One matching name in `dependency-names` refuses the whole PR, grouped or not. Case-insensitive; `.` `_` `-` are equivalent; an exact name does not match longer names (`numpy` skips `numpy-financial`). Non-empty with no `dependency-names` fails closed. |
| `wait-attempts` / `wait-seconds` | `10` / `15` | Poll budget for check runs outside this workflow run. |

Python repo (uv), as the last job of `ci.yml`:

```yaml
    uses: tclavelloux/promy-github-workflows/.github/workflows/dependabot-automerge.yml@dependabot-automerge/v1
    with:
      ecosystems: uv
      exclude-dependencies: |
        torch*
        ultralytics*
        opencv*
        numpy
        openai
```

`ecosystems` and `exclude-dependencies` need `dependabot-automerge/v1.1.0` (`/v1` moves to it). The gate logic lives in `scripts/automerge-gate.sh`, inlined in the workflow and kept byte-identical by `internal/syncheck/automerge_test.go`, which also runs the accept/refuse table through bash.

Merges only when all of these hold:

- Ecosystem is in `ecosystems`; update type is patch or minor (`dependabot/fetch-metadata`, SHA-pinned).
- No dependency name matches `exclude-dependencies`.
- Every commit on the PR is authored by the PR's own bot (`dependabot[bot]` or `promy-fleet-bot[bot]`), not only the first one fetch-metadata checks.
- `promy-fleet-bot[bot]` PRs (opened by `go-vuln-fix` / `python-vuln-fix` with the App token) are accepted only when the head branch is `fix/vuln-*` (Go) or `fix/py-vuln-*` (uv), every changed file is `go.mod`, `go.sum` or `uv.lock` (modified, not added or renamed), and the diff, read from the merge base and the head rather than the title or body, holds only patch-level bumps within the same minor (`go` directive included). An added or removed requirement, a changed `replace`/`exclude`/`toolchain` line, a pre-release or pseudo-version, or a downgrade refuses. `ecosystems` (`go_modules` for `fix/vuln-*`, `uv` for `fix/py-vuln-*`) and `exclude-dependencies` apply as for Dependabot; `merge-update-types` does not. A resolver side effect that moves another package by a minor (listed by `python-vuln-fix` under "Also moved") therefore goes to a human.
- Every other check run on the head SHA is `completed` with `success`, `neutral` or `skipped`; no legacy commit status is `failure`, `error` or `pending`.
- The head SHA is unchanged at merge time (`gh pr merge --squash --match-head-commit`).

Never merges:

- Major bumps. Rejected in the script even if listed in `merge-update-types`.
- Ecosystems outside `ecosystems`, and PRs containing an excluded dependency.
- `github-actions` bumps. `GITHUB_TOKEN` lacks the `workflows` permission and cannot merge workflow-file changes.
- release-please PRs.
- Anything a human touched (extra commit, non-Dependabot author).

A non-merge decision exits 0 with a `::notice` and a step summary. A red check would duplicate the real failure.

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `merge-update-types` | `version-update:semver-patch version-update:semver-minor` | Allowlist of fetch-metadata `update-type` values |
| `wait-attempts` | `10` | Polls for check runs outside this run (e.g. `pr-title.yml`) before giving up without merging |
| `wait-seconds` | `15` | Seconds between polls |

Needs `contents: write`, `pull-requests: write`, `checks: read`, `actions: read`, `statuses: read`. The caller job must grant exactly these; a called workflow can only narrow them.

The squash commit is pushed by `GITHUB_TOKEN`, so it starts no workflows. release-please's `push: main` run does not fire until the next human merge, which refreshes its PR. Nothing is lost: release-please scans every commit since the last release, and `build(deps)` commits are non-releasing.

## `release-please.yml`

Opens and updates the release PR from the Conventional Commits on `main`, and cuts the tag and GitHub release when it merges. Tag `release-please/v1`.

```yaml
name: Release Please

on:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  release-please:
    permissions:
      contents: write
      pull-requests: write
    uses: tclavelloux/promy-github-workflows/.github/workflows/release-please.yml@release-please/v1
    secrets:
      APP_CLIENT_ID: ${{ secrets.APP_CLIENT_ID }}
      APP_PRIVATE_KEY: ${{ secrets.APP_PRIVATE_KEY }}
```

Canonical caller: `templates/release-please.yml`. `workflow-sync` requires this shape. Set `config-file` / `manifest-file` under `with:` only if the repo keeps them elsewhere.

Behaviour:

- With `APP_CLIENT_ID` and `APP_PRIVATE_KEY`, the release PR is opened with a GitHub App installation token (`actions/create-github-app-token`, scoped to the calling repo, `contents` + `pull-requests` write), so **CI runs on it**. A failed token mint fails the run.
- Without the secrets, the PR is opened with `GITHUB_TOKEN`: **CI does not run on it**, and the run emits a `::warning title=release-please::` annotation. Close and reopen the PR (fires `reopened`) before merging.
- The release PR branch is `release-please--branches--main--components--<package-name>` whenever `package-name` is set. `include-component-in-tag` changes the tag and PR title format, not the branch.
- Release PRs are never auto-merged. Cutting a release stays a human decision.
- Tags and releases created with the App token trigger `push: tags` and `release` workflows in the caller, unlike `GITHUB_TOKEN`.
- Runs are serialized in the `release-please` concurrency group, never cancelled mid-run.
- No checkout: release-please works through the API.

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `config-file` | `release-please-config.json` | Path to the release-please config in the caller repo |
| `manifest-file` | `.release-please-manifest.json` | Path to the release-please manifest in the caller repo |

### Secrets

| Secret | Required | Purpose |
|---|---|---|
| `APP_CLIENT_ID` | no | Client ID of the fleet GitHub App (installed on the repo, `contents` + `pull-requests` write) |
| `APP_PRIVATE_KEY` | no | Private key of that App |

Map them explicitly as in the snippet; never `secrets: inherit` (`workflow-sync` rejects it).

### Outputs

| Output | Purpose |
|---|---|
| `releases_created` | `true` when at least one release was created |
| `release_created` | `true` when a release was created for the root path |
| `tag_name` | Tag of the root-path release |
| `version` | Version of the root-path release |
| `sha` | Commit SHA the root-path release was cut from |
| `prs_created` | `true` when at least one release PR was created or updated |
| `pr` | JSON of the release PR (head branch, number, title, body) |
| `paths_released` | JSON array of the paths released |

Needs `contents: write` and `pull-requests: write`; the caller job must grant exactly these.

## `go-vuln-fix.yml`

Weekly, patches the advisories govulncheck reports as **called** and opens a `fix(deps)` PR. Tag `go-vuln-fix/v1`.

```yaml
name: Vuln fix

on:
  schedule:
    - cron: '17 6 * * 4'
  workflow_dispatch:

permissions:
  contents: read

concurrency:
  group: go-vuln-fix
  cancel-in-progress: false

jobs:
  fix:
    permissions:
      contents: write
      pull-requests: write
      issues: write
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-vuln-fix.yml@go-vuln-fix/v1
    secrets:
      APP_CLIENT_ID: ${{ secrets.APP_CLIENT_ID }}
      APP_PRIVATE_KEY: ${{ secrets.APP_PRIVATE_KEY }}
```

Behaviour:

- Module path: group findings by module, take the highest `fixed_version`, run `go get`, `go mod tidy`, `go build ./...`, rescan. Abort to the issue path if a fix would change the `go` or `toolchain` directive.
- Go patch bump: a called stdlib/toolchain advisory fixed in a newer patch of the `go` directive's minor (`go 1.26.8` -> `1.26.9`) bumps the directive in the same PR. Tidy, build and the rescan run with `GOTOOLCHAIN=go<target>`; the run aborts if any claimed ID is still called.
  - Refused, advisory left for the issue: other minors, no fixed version, a `toolchain` directive, `go.work`, a Dockerfile pinning `golang:X.Y.Z` or `@sha256:` (official images set `GOTOOLCHAIN=local`, so they cannot build a newer patch), `.go-version`, a `golang`/`go` line in `.tool-versions`.
  - Minor-tag Dockerfiles (`golang:1.26`) pass `go-docker.yml`, which compares minors only, and pick up the patch on rebuild.
  - `go-patch-bump: false` disables it.
- Stdlib/toolchain findings the bump does not cover, and module findings with no fix: one rolling issue per repo, marker `<!-- go-vuln-fix:stdlib -->`, with the refusal reason. Dockerfiles are never edited.
- Dedupe: branch `fix/vuln-<12 hex of sha256 over the sorted IDs>`. An open PR on that branch skips the run. A **closed-unmerged** PR on that branch means rejected and is never retried.
- A new PR closes older open `fix/vuln-*` PRs by `github-actions[bot]` or the App as superseded. The new scan starts from current `main`, so it is a superset.
- The commit goes through GraphQL `createCommitOnBranch` (GitHub-signed, no credentials on disk).
- With `APP_CLIENT_ID` and `APP_PRIVATE_KEY`, the PR and commit are authored by the App (`actions/create-github-app-token`, scoped to the calling repo, `contents` + `pull-requests` write), so **CI runs on the PR**. A failed token mint fails the run.
- Without the secrets, the PR is opened with `GITHUB_TOKEN`: **CI does not run on it** and the PR body says so. Close and reopen the PR (fires `reopened`) before merging.
- The rolling issue and its comments always use `GITHUB_TOKEN`.

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `govulncheck-version` | `v1.8.0` | Must equal `go-vuln.yml`'s default; this repo's CI enforces it |
| `go-version-file` | `go.mod` | Path used to resolve the Go toolchain |
| `base-branch` | `''` (repo default branch) | Branch to scan and open the PR against |
| `go-patch-bump` | `true` | Bump the `go` directive to a same-minor patch release when a called stdlib advisory is fixed there |

### Secrets

| Secret | Required | Purpose |
|---|---|---|
| `APP_CLIENT_ID` | no | Client ID of the fleet GitHub App (installed on the repo, `contents` + `pull-requests` write) |
| `APP_PRIVATE_KEY` | no | Private key of that App |

Map them explicitly as in the snippet; never `secrets: inherit` (`workflow-sync` rejects it). Both unset: the workflow behaves as before.

Needs `contents: write`, `pull-requests: write`, `issues: write`; the caller job must grant exactly these.

Every run, clean ones included, emits `::notice title=vuln-record::<date> <repo> <IDs|none> <action>` and a step-summary row. Measure frequency:

```bash
GH_TOKEN= gh run list --workflow "Vuln fix" --limit 50 --json databaseId -q '.[].databaseId' \
  | xargs -I{} sh -c 'gh run view {} --log 2>/dev/null | grep vuln-record'
```

## `python-vuln-fix.yml`

Weekly, patches the advisories osv-scanner reports in each project's `uv.lock` and opens one `fix(deps)` PR. Python twin of `go-vuln-fix`. Tag `python-vuln-fix/v1`.

```yaml
name: Vuln fix

on:
  schedule:
    - cron: '17 6 * * 4'
  workflow_dispatch:

permissions:
  contents: read

concurrency:
  group: python-vuln-fix
  cancel-in-progress: false

jobs:
  fix:
    permissions:
      contents: write
      pull-requests: write
      issues: write
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-vuln-fix.yml@python-vuln-fix/v1
    with:
      projects: '["src/app","src/worker"]'
      exclude-dependencies: |
        torch*
        numpy
    secrets:
      APP_CLIENT_ID: ${{ secrets.APP_CLIENT_ID }}
      APP_PRIVATE_KEY: ${{ secrets.APP_PRIVATE_KEY }}
```

Behaviour:

- Per project, scan `uv.lock` (config lookup as in `python-vuln.yml`). Per vulnerable package, the target is the highest of the lowest fixed versions across its advisory groups, taken from OSV `ECOSYSTEM` ranges that contain the installed version (`GIT` ranges are ignored).
- Each package: `uv lock --upgrade-package <pkg>==<target>` in the project directory, one package at a time, lock restored on any refusal. Only `uv.lock` changes: never `pyproject.toml`, a Dockerfile or a requirement.
- Refused without calling uv:
  - `exclude-dependencies` match (PEP 503-normalised globs, whole-name: `numpy` does not match `numpy-financial`).
  - A major bump (first release segment rises) unless `allow-major: true`. Calendar versions (first segment >= 1000 on both sides, e.g. certifi `2024.2.2` -> `2026.10.19`) are never a major.
  - An advisory with no fixed version.
- Refused after calling uv, lock restored: uv says `No solution found` or `unsatisfiable` (the fix is outside the project's own constraints); the resolver picked another version; the change drags an excluded or major dependency along.
- Any other uv failure (network, index, broken lock) fails the run and restores every lock. Nothing is closed or rejected on a failed run.
- Other side effects of a bump (patch or minor moves of other packages) are kept and listed under the PR's "Also moved by the resolver".
- Verification: osv-scanner rescans each patched lock. A claimed ID still reported restores that whole project's lock and goes to the issue.
- Refusals and aborts: one rolling issue per repo, marker `<!-- python-vuln-fix:unfixed -->`, title `security: Python advisories need a human`, table `project | package installed | ids | max severity | fixed | reason`. Never closed automatically.
- Dedupe: branch `fix/py-vuln-<12 hex of sha256 over the sorted project/ID pairs>`. An open PR on that branch skips the run. A **closed-unmerged** PR on that branch means rejected and is never retried.
- A new PR closes older open `fix/py-vuln-*` PRs by `github-actions[bot]` or the App as superseded. A run that finds nothing to fix (Dependabot or a human landed it first) closes open `fix/py-vuln-*` PRs opened by a bot as stale, only when the scan completed and nothing aborted.
- The commit goes through GraphQL `createCommitOnBranch` (GitHub-signed, no credentials on disk), one commit with every changed lock.
- Never merges, never enables auto-merge itself. A caller's automerge job (`dependabot-automerge/v1.2.0`+) merges the App-opened fix PR once the diff is verified as patch-level and CI is green; a PR opened with `GITHUB_TOKEN` runs no CI and never reaches it.
- uv version: read from the caller's `.uv-version` (same validation as `python-lint.yml`). A lock last written by a newer uv gets that uv's lock style when a resolution runs: keep the fleet pin at the version Dependabot uses.
- With `APP_CLIENT_ID` and `APP_PRIVATE_KEY`, the PR and commit are authored by the App, so **CI runs on the PR**. Without them the PR is opened with `GITHUB_TOKEN`, **CI does not run on it** and the PR body says so. Close and reopen the PR before merging. A failed token mint fails the run. The issue always uses `GITHUB_TOKEN`.
- The fix logic is inlined in the workflow; `scripts/python_vuln_fix.py` is its locally testable twin, and the Go tests keep the two blocks byte-identical. Exit codes: 0 completed, 2 usage or scanner error, 4 uv error that is not a refusal.

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `projects` | required | JSON array of uv project directories, each with a tracked `uv.lock` (same input as `python-vuln.yml`; `workflow-sync` requires every uv project) |
| `osv-scanner-version` | `2.6.0` | Must equal `python-vuln.yml`'s default and `.osv-scanner-version`; Go tests enforce it |
| `osv-scanner-sha256` | `ca69b3d3...b108` | sha256 of the `osv-scanner_linux_amd64` asset; must equal `python-vuln.yml`'s default |
| `exclude-dependencies` | `''` | Newline- or comma-separated globs this workflow never moves; same syntax as `dependabot-automerge`'s input |
| `allow-major` | `false` | Let a fix cross a major version |
| `base-branch` | `''` (repo default branch) | Branch to scan and open the PR against |

### Secrets

| Secret | Required | Purpose |
|---|---|---|
| `APP_CLIENT_ID` | no | Client ID of the fleet GitHub App (installed on the repo, `contents` + `pull-requests` write) |
| `APP_PRIVATE_KEY` | no | Private key of that App |

Map them explicitly as in the snippet; never `secrets: inherit` (`workflow-sync` rejects it). The caller job must grant `contents: write`, `pull-requests: write`, `issues: write` exactly.

Every run emits `::notice title=vuln-record::<date> <repo> <IDs|none> <action>` and a step-summary row.

## Python workflows

Five reusable workflows for uv-managed Python repos (first consumer: `promy-market-catalog`). Opt-in, one caller job each, tags `python-{lint,typecheck,vuln,test,docker}/v1`.

```yaml
jobs:
  lint:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-lint.yml@python-lint/v1
    with:
      projects: '["src/leaflet","src/store"]'
  typecheck:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-typecheck.yml@python-typecheck/v1
    with:
      projects: '["src/leaflet","src/store"]'
  vuln:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-vuln.yml@python-vuln/v1
    with:
      projects: '["src/leaflet","src/store"]'
  test:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-test.yml@python-test/v1
    with:
      projects: '["src/store"]'
  docker:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-docker.yml@python-docker/v1
    with:
      projects: '["src/leaflet","src/store"]'
```

Shared contract:

- **`projects`** is a JSON array string of project directories. Each workflow matrixes over it inside one job whose id equals the caller's job name, so check names are fixed: `lint / lint (src/store)`, `test / test (src/store)`. Required checks in a ruleset use those names.
- **Never path-filter** these jobs: a required check that does not run blocks the PR.
- A listed project without its `uv.lock` (or `Dockerfile` for `python-docker`) fails the job. Nothing is skipped.
- `contents: read` only; no secrets; every `inputs.*` reaches the shell through `env:`. A caller with `secrets: inherit` is unnecessary.
- Callers track the moving `@python-<name>/v1` tag; additive changes (new optional inputs, new default versions) ride it.
- **uv version** comes from the caller's `.uv-version` (`astral-sh/setup-uv` installs exactly it). A missing or malformed file fails the job; there is no `latest` fallback.
- `uv sync --frozen` everywhere. The uv cache (`setup-uv` `enable-cache`) is keyed on each project's `uv.lock`, which matters for torch.
- Pytest, pytest-cov, deptry and basedpyright are the project's own `dev` dependencies: the lock decides their versions, and `uv run` uses the same ones locally.

### workflow-sync Python profile

`workflow-sync` (from `hooks/v1.4.0`) governs a uv repo with the same tool, picked by profile: a root `go.mod` is Go, else any tracked `uv.lock` or `pyproject.toml` is Python, else the run fails with `profile`. A uv project is a directory with a tracked `uv.lock`.

- `ci.yml`: jobs `lint`, `typecheck`, `vuln`, `test`, `gitleaks`, plus `docker` iff a project has a `Dockerfile`, each calling its `python-*/v1` workflow with no `permissions:`; every job's `projects` lists exactly the uv projects (`ci-projects`).
- `dependabot.yml`: `github-actions`, `uv` for every project, `docker` for every project with a `Dockerfile` (ignoring `ghcr.io/astral-sh/uv`).
- Pins: `.uv-version` = `0.12.18`, `.ruff-version` and the `ruff-pre-commit` rev = `0.17.0`; `.python-version` per project; no `poetry.lock`.
- Every osv-scanner ignore has a reason and expires within 90 days; every tested project has `fail_under` in (0, 100].
- Shared with Go: trigger, concurrency, permissions, draft skip, SHA pins, `pr-title.yml`, `release-please.yml`, the pre-commit and Makefile rules (`check-coverage` hook running `make check-coverage`).

Start from `templates/python/` (`ci.yml`, `dependabot.yml`, `pre-commit-config.yaml`, `Makefile`), derived from promy-market-catalog; a Go test proves it passes. Rule catalogue: [docs/workflow-sync.md](docs/workflow-sync.md).

### `python-lint.yml`

Per project: `uv lock --check`, then `uv sync --frozen` and `deptry`. Once per run (matrix index 0): everything else.

| Check | Fails when |
|---|---|
| `ruff check`, `ruff format --check` | Repo-wide, with the caller's `ruff.toml`, at the `ruff-version` input |
| projects | A listed project lacks `pyproject.toml` or `uv.lock`, or a tracked `uv.lock` is not listed |
| uv pin drift | `.uv-version`, the `astral-sh/uv-pre-commit` `rev:`, each `COPY --from=ghcr.io/astral-sh/uv:X` and each `[tool.uv] required-version` floor disagree |
| ruff pin drift | `.ruff-version`, the `astral-sh/ruff-pre-commit` `rev: vX` and each `required-version` floor in `ruff.toml` / `[tool.ruff]` disagree |
| `.python-version` | A project's `.python-version` is missing, disagrees with a `FROM python:X.Y` in its Dockerfile, or violates `requires-python` |
| `uv lock --check` | `uv.lock` is stale against `pyproject.toml` |
| deptry | Runs in the frozen env (a bare `uvx deptry` reports false DEP001) |

Drift rules:

- The check reads tracked files only (`git ls-files`), discovers every uv project (a `pyproject.toml` with a sibling `uv.lock` or a `[tool.uv]` table, template directories included), and never hardcodes a path.
- One annotation per mismatching file, with the expected and found values (and the line for hooks and Dockerfiles).
- A missing pin in a uv project is an error, not a skip: no `.uv-version`, no `required-version`, a Dockerfile with no uv `COPY`.
- `required-version` is a floor: `.uv-version` must satisfy it, not equal it. An exact `==X` passes with a warning (it breaks Dependabot's uv updater).
- A `.ruff-version` that differs from the `ruff-version` input is a warning, never a failure, like `.golangci-version` in `go-lint`.
- Logic: `scripts/python_pins.py`, inlined into the workflow; unit tests with a failing fixture per mismatch class in `scripts/tests/`.

Bump uv in one PR that edits every place: `.uv-version`, the `uv-pre-commit` `rev:`, every `COPY --from=ghcr.io/astral-sh/uv:X`, the floors in every `pyproject.toml` that must rise, then `uv lock` per project. The caller's `dependabot.yml` ignores the `ghcr.io/astral-sh/uv` image, so uv bumps stay deliberate. Bump ruff the same way: `.ruff-version`, the `ruff-pre-commit` `rev:`, `ruff.toml` floor.

| Input | Default | Purpose |
|---|---|---|
| `projects` | required | JSON array of uv project directories |
| `ruff-version` | `0.17.0` | ruff to run (with or without a leading `v`). The fleet's single ruff decision; `RuffVersion` in `policy.go` is tied to it |
| `deptry-args` | `src/job --known-first-party helpers` | Arguments to `deptry` from each project directory. Word-split, not globbed |
| `workflow-sync` | `false` | Run the `workflow-sync` pre-commit hook at the caller's pinned `rev:`; adds `setup-go` for that one step. Needs `- id: workflow-sync` in the caller's `.pre-commit-config.yaml` at `rev: hooks/v1.4.0` or later (the Python profile) |

### `python-typecheck.yml`

Per project: `uv sync --frozen`, then `basedpyright --baselinefile <baseline-file>` (the project's own, from its `dev` group). New errors fail; baselined ones are grandfathered.

- A missing baseline fails with the exact command to create it. `require-baseline: false` is for a project with zero errors only: basedpyright writes no baseline file when there is nothing to record (verified), so a clean project cannot satisfy the default.
- Regenerate a baseline only to shrink it.
- basedpyright exits 1 on warnings too, so the baseline must cover warnings: write it after the tests and any other code that the check sees.

| Input | Default | Purpose |
|---|---|---|
| `projects` | required | JSON array of uv project directories |
| `baseline-file` | `.basedpyright/baseline.json` | Baseline path relative to each project directory |
| `require-baseline` | `true` | Fail when the baseline file is missing |
| `args` | `""` | Extra flags for `basedpyright`. Word-split, not globbed |

### `python-vuln.yml`

Per project: `osv-scanner scan source --lockfile <project>/uv.lock`. Python has no reachability analysis, so any known advisory in the lock fails, minus ignores.

- Config: `<project>/osv-scanner.toml`, else `osv-scanner.toml` at the repo root, else none. Each `[[IgnoredVulns]]` entry should carry `id`, `reason` and `ignoreUntil`; osv-scanner re-reports an advisory once its `ignoreUntil` has passed.
- Install: the `osv-scanner_linux_amd64` release asset, rejected unless its sha256 equals the `osv-scanner-sha256` input. The digest is pinned in the workflow, not fetched from the same release.
- Bump osv-scanner: change both defaults (take the digest from the release's `osv-scanner_SHA256SUMS`, `linux_amd64` line) and `OSVScannerVersion` in `internal/syncheck/policy.go`; governance CI ties the version, and the workflow fails closed on a wrong digest.
- Needs network access to the OSV API; a scanner failure fails the job.

| Input | Default | Purpose |
|---|---|---|
| `projects` | required | JSON array of uv project directories |
| `osv-scanner-version` | `2.6.0` | Version to install (with or without a leading `v`) |
| `osv-scanner-sha256` | digest of 2.6.0 | sha256 of `osv-scanner_linux_amd64` for that version |
| `args` | `""` | Extra flags for `osv-scanner scan source`. Word-split, not globbed |

### `python-test.yml`

Per project: `uv sync --frozen`, then `pytest -m "<markers>" --cov --cov-fail-under=<fail_under>`.

- The floor is `[tool.coverage.report] fail_under` in the project's `pyproject.toml`; the same value drives the local `make check-coverage`.
- A missing, zero, negative, non-numeric or above-100 `fail_under` is an error. Never zero (the `go-coverage` v2 lesson).
- A warning when `[tool.coverage.run]` has no `source`: unexecuted files would not count.
- Pytest config (`pythonpath`, `--disable-socket`, markers, `source`) lives in the project's `pyproject.toml`.
- No PR comment and no ratchet check against the base branch.
- Logic: `scripts/python_coverage_floor.py`, inlined into the workflow, with unit tests.

| Input | Default | Purpose |
|---|---|---|
| `projects` | required | JSON array of uv project directories |
| `markers` | `not network and not model and not gcp` | pytest `-m` expression |
| `args` | `""` | Extra flags for `pytest`. Word-split, not globbed |

### `python-docker.yml`

Per project: `docker/build-push-action` (context = the project directory, `linux/amd64`, `push: false`, `load: true`, GitHub Actions layer cache scoped per project), then `docker run --rm <image> <smoke-command>`.

- A missing Dockerfile fails the job.
- The smoke command only has to exit 0, so a broken import or entrypoint fails it. It does not assert which commands the help lists.
- The static `.python-version` vs `FROM` check lives in `python-lint`; this workflow proves the build.
- Timeout 45 minutes; the leaflet image (chromium, Playwright, CPU torch) is the long pole.

| Input | Default | Purpose |
|---|---|---|
| `projects` | required | JSON array of project directories (build contexts) |
| `dockerfile` | `Dockerfile` | Dockerfile path relative to each project directory |
| `smoke-command` | `python main.py --help` | Command run in the image; must exit 0. Word-split, not globbed |

## Shared pre-commit hooks

`.pre-commit-hooks.yaml` at the repo root makes this repo a pre-commit **remote hook repository**, alongside its reusable workflows. Callers add it to their own `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/tclavelloux/promy-github-workflows
    rev: hooks/v1.4.0   # hooks/v1.0.0 has no go-vuln; hooks/v1.1.0 has no workflow-sync; py-vuln needs v1.4.0
    hooks:
      - id: no-direct-commit-to-main
      - id: go-vuln        # Go repos; Python repos use py-vuln
      - id: workflow-sync
```

The repo is public, so pre-commit clones it over anonymous HTTPS — no token, no SSH key, no credential helper. Hooks resolve on a fresh machine and in CI.

Hooks live under `hooks/`. All of them share the `hooks/vX` tag namespace: pre-commit resolves one `rev` for the whole repository, so a per-hook namespace would be unusable.

### `no-direct-commit-to-main`

Fails the commit when `HEAD` is on a protected branch — `main` by default.

```
Direct commits to 'main' are blocked. Create a feature branch instead.
```

| Field | Value | Why |
|---|---|---|
| `language` | `script` | The entry is a file shipped in this repo and executed directly through its `#!/bin/sh` shebang. `language: system` would require the interpreter to be resolvable on the caller's `PATH` and the script to already exist there — `script` ships the implementation with the hook |
| `stages` | `[pre-commit]` | The only stage where blocking is meaningful. Without it, pre-commit registers the hook at every stage the caller installs, so a `pre-push` or `commit-msg` install would re-run the same branch check and fail a push that has nothing to do with committing |
| `always_run` | `true` | Triggers on branch state, not on file contents. Without it the hook is skipped whenever no staged file matches |
| `pass_filenames` | `false` | Reads `git symbolic-ref`, not the staged paths. Without it every staged path is appended to the command line, where the script would read them as protected-branch names |

Detached `HEAD` **fails** rather than passing. `git rev-parse --abbrev-ref HEAD` returns the literal string `HEAD` when detached, so a naive `[ "$branch" = "main" ]` silently allows the commit. The script uses `git symbolic-ref --quiet --short HEAD`, which exits non-zero instead.

The protected branch is configurable through `args:`, taking one or more branch names:

```yaml
      - id: no-direct-commit-to-main
        args: [main, release]
```

Every `promy-*` repo uses `main` today, so the default covers all six. The argument exists because a branch name baked into a shared hook is a value six callers cannot override without forking the hook — the exact coupling this repo removes elsewhere.

### `go-vuln`

Runs the fleet-pinned `govulncheck` locally, with the same Go toolchain CI uses. Stages: `pre-push`, `manual`.

- Version: `.govulncheck-version` in the hook clone, so the caller's immutable `rev:` pins it. CI ties that file to `go-vuln.yml` and `go-vuln-fix.yml`.
- Toolchain: `GOTOOLCHAIN` is set from go.mod (`toolchain`, else `go`) when it is a full `x.y.z`. This fixes local CVE counts understating CI.
- Binary cached per version under `${XDG_CACHE_HOME:-~/.cache}/promy-go-vuln/<version>/`; `~/go/bin` is untouched.

| Context | Mode | Why |
|---|---|---|
| `pre-push`, range touches `go.mod`/`go.sum` | **block** | A dependency change is where the fix belongs |
| `pre-push`, range does not touch them | **warn** (exit 0) | An advisory published today must not block unrelated work |
| `manual` stage, `make vuln`, `--all-files` | **block** | Explicit request |

`GO_VULN_MODE=block|warn` overrides. Block mode fails closed on scanner errors; warn mode reports and exits 0.

Add the `make vuln` target (`templates/Makefile.vuln.mk`):

```make
vuln:
	pre-commit run go-vuln --hook-stage manual
```

Bypass once: `SKIP=go-vuln git push`.

`rev:` must be an immutable `hooks/vX.Y.Z` tag, never `hooks/v1`; see the pinning note above.

### `py-vuln`

Python twin of `go-vuln`: runs the fleet-pinned osv-scanner over every tracked `uv.lock`, with `python-vuln.yml`'s config lookup (`<project>/osv-scanner.toml`, else `./osv-scanner.toml`). Stages: `pre-push`, `manual`.

- Version: `.osv-scanner-version` in the hook clone (`OSV_SCANNER_VERSION` overrides); Go tests tie it to `python-vuln.yml` and `policy.go`.
- Install: `go install github.com/google/osv-scanner/v2/cmd/osv-scanner@v<version>` (checksums verified by the Go module mirror), cached per version under `${XDG_CACHE_HOME:-~/.cache}/promy-py-vuln/`. Needs Go on `PATH`, like `workflow-sync`. CI uses the sha256-pinned release binary of the same tag.
- Mode: blocks a push that changes a `uv.lock`, a `pyproject.toml` or an `osv-scanner.toml`; warns (exit 0) otherwise; `manual`, `make vuln` and `--all-files` block. `PY_VULN_MODE=block|warn` overrides. Block mode fails closed on scanner or install errors.

Add the `make vuln` target (`templates/python/Makefile`):

```make
vuln:
	pre-commit run py-vuln --hook-stage manual
```

Bypass once: `SKIP=py-vuln git push`.

### `workflow-sync`

Checks the repo's CI wiring against the fleet policy at the caller's pinned `rev:`: `ci.yml` shape, `pr-title.yml`, `release-please.yml`, `dependabot.yml`, SHA-pinned actions, `.pre-commit-config.yaml`, `Makefile` and the pinned tool files, with the Go or the Python profile (see [Python workflows](#workflow-sync-python-profile)). `language: golang`, so the system `go` must be on `PATH`. Stages: `pre-commit`, `manual`.

```yaml
      - id: workflow-sync
```

CI runs the same hook when the `lint` job sets `with: {workflow-sync: true}` (the go-lint and python-lint input). Rule catalogue, exemptions, limits and rollout: [docs/workflow-sync.md](docs/workflow-sync.md). Repo settings that CI cannot read: `GH_TOKEN= scripts/check-repo-settings.sh`.

### Why it is centralised

- Every repo has a server-side ruleset `protect-main` (PR required, squash only, required checks, Admin-only bypass). It needs GitHub Pro for the five private repos; the account has been on Pro since 2026-10-04. This hook stops a commit on `main` before it exists locally; the ruleset stops the push.
- A `local` hook duplicated across six `.pre-commit-config.yaml` files is one policy in six places, free to drift. Same failure mode as the per-caller workflow inputs `go-coverage/v2` removed.
- It cannot live in `.githooks/`. `core.hooksPath` does not survive a clone (it is written to `.git/config`), and `pre-commit install` refuses outright while it is set: `Cowardly refusing to install hooks with core.hooksPath set`. The two mechanisms are mutually exclusive; the fleet standardises on pre-commit.

A hook is not a substitute for a server-side rule — it runs only where it is installed, and `--no-verify` bypasses it. The ruleset is the control; the hook is early feedback.

## CI

`actionlint`, `zizmor` and `workflow-sync` run on every PR (`.github/workflows/ci.yml`), all blocking. A bug here is fleet-wide, so these are the only gates before merge.

- `actionlint` parses every workflow file and shellchecks each `run:` block. Version pinned in `.actionlint-version`.
- `zizmor` audits the same workflows for security anti-patterns actionlint doesn't check — unpinned action refs, credential persistence, template injection. Version pinned in `.zizmor-version`, mirroring `.actionlint-version`. Installed from zizmor's released binary rather than `uvx zizmor`, so the job needs no Python/uv toolchain — same reasoning as the actionlint install step.

- `workflow-sync` runs `gofmt -l`, `go vet` and `go test ./...`. The tests include `internal/syncheck/governance_test.go`, which fails when the policy constants drift from `go-coverage.yml`, `go-lint.yml`, `python-lint.yml`, `python-vuln.yml`, `pr-title.yml`, `.osv-scanner-version`, `.pre-commit-hooks.yaml` or `templates/` (including `templates/python/`, which must pass the Python profile), and `python_workflows_test.go`, which fails when an inlined script block differs from `scripts/` or a Python workflow breaks a hard rule (SHA pins, `persist-credentials: false`, read-only token, inputs through `env:`, timeout, one job per workflow).
- `python-scripts` runs the unit tests of `scripts/python_pins.py` and `scripts/python_coverage_floor.py` (`python3 -m unittest discover -s scripts/tests`).

### Action-pinning policy

Every `uses:` in this repo's own workflows is pinned to a full commit SHA with a trailing version comment (`actions/checkout@<sha> # v4.4.0`), not to a mutable tag. `vladopajic/go-test-coverage@v2` matters most: it is the only third-party action, it runs in a job holding `pull-requests: write`, and a mutable major tag is one its maintainer can repoint underneath every caller with no review on this side.

Dependabot (`.github/dependabot.yml`, `github-actions` ecosystem, weekly) is what keeps these pins from going stale — pinning without Dependabot just freezes the fleet on old actions. Each Dependabot PR bumps one SHA and its version comment; it does not touch the per-workflow moving major tags (`go-lint/v1`, `go-coverage/v2`, `go-vuln/v1`, `go-docker/v1`) that callers track — those are managed by hand, per the Versioning section above.

<!-- readme-updated-at: 59f5fd1 -->
