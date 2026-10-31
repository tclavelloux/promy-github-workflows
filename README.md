# promy-github-workflows

Fleet governance for the `promy-*` repos: reusable GitHub Actions workflows, shared pre-commit hooks, the `workflow-sync` policy checker and pin management. `main` changes for CI-infra reasons only.

- Rules `workflow-sync` enforces, exemptions, policy changes: [docs/workflow-sync.md](docs/workflow-sync.md)
- Dependabot, automerge and vuln-fix automation, onboarding a repo: [docs/dependency-automation.md](docs/dependency-automation.md)
- Pinned tool versions and bump procedures: [docs/pin-bumps.md](docs/pin-bumps.md)
- Inputs, defaults, secrets and outputs of each workflow: the `on.workflow_call` block of its YAML under `.github/workflows/`

## Versioning and pinning

| Consumer | Ref | Why |
|---|---|---|
| `uses:` in a workflow | moving major `<workflow>/v<N>` (`go-lint/v1`) | Re-resolved every run, so additive changes reach every caller with no PR |
| `rev:` in `.pre-commit-config.yaml` | immutable `hooks/vX.Y.Z` | pre-commit resolves a rev once and caches it; a moving tag is never re-fetched |

- Each workflow has its own tag namespace: moving `<workflow>/vN` plus immutable `<workflow>/vX.Y.Z`. A git tag is repo-wide, so a shared bare `v1` would freeze every workflow at once.
- Current majors: `go-coverage/v2`, every other workflow `/v1`. `workflow-sync` rule `governance-ref` rejects anything else: bare `v1`/`v2`, `main`, SHAs, immutable `x/v1.2.3`, frozen majors.
- Additive change (new optional input, new default version): move the major. Semantic change: cut a new major and freeze the old one.
- Never put `hooks/v1` in a `rev:`; pre-commit warns `appears to be a mutable reference` and the cache never updates. Cut a new `hooks/vX.Y.Z` for every hook or policy change; never move an existing one. Consumers update with `pre-commit autoupdate --repo https://github.com/tclavelloux/promy-github-workflows`.
- Release first, then bump consumers. Tag history is in the annotated tags (`git tag -n -l '<workflow>/*'`).

## Workflows

All are called at job level and grant `contents: read` unless stated. Never path-filter a job that is a required check: one that does not run blocks the PR.

| Workflow | Purpose | Profile |
|---|---|---|
| [`go-lint.yml`](#go-lintyml) | golangci-lint, fmt, config verify, `go mod tidy -diff`, `workflow-sync` | Go |
| [`go-coverage.yml`](#go-coverageyml) | Gate a coverage profile, sticky PR comment | Go |
| [`go-vuln.yml`](#go-vulnyml) | govulncheck, called vulns only | Go |
| [`go-docker.yml`](#go-dockeryml) | Dockerfile `golang:` minor vs `go` directive, then `docker build` | Go |
| [`python-*.yml`](#python-workflows) | lint, typecheck, vuln, test, docker for uv projects | Python |
| [`docs-lint.yml`](#docs-lintyml) | `workflow-sync` for a repo with no code | Docs |
| [`pr-title.yml`](#pr-titleyml) | Conventional Commits PR title | All |
| [`release-please.yml`](#release-pleaseyml) | Release PR, tag, GitHub release | All |
| [`dependabot-automerge.yml`](#dependabot-automergeyml) | Squash-merge green bot PRs | All |
| [`go-vuln-fix.yml`, `python-vuln-fix.yml`](#vuln-fix) | Weekly `fix(deps)` PR for advisories | Go, Python |
| [`pin-drift.yml`](#pin-driftyml) | Monthly issue for pins behind upstream | This repo only |

### `go-lint.yml`

```yaml
jobs:
  lint:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-lint.yml@go-lint/v1
    with:
      workflow-sync: true
```

Fails on:

- `golangci-lint run` findings, with the caller's `.golangci.yml`.
- `golangci-lint fmt --diff`: unformatted code.
- `golangci-lint config verify`: `.golangci.yml` invalid for the pinned version.
- `go mod tidy -diff`: `go.mod`/`go.sum` not tidy (Go 1.23+).
- `workflow-sync` findings, when `workflow-sync: true` (required by rule `ci-lint-sync`). Runs as the last step, even after an earlier failure. Needs `- id: workflow-sync` in the caller's `.pre-commit-config.yaml`.

A `.golangci-version` that differs from the `golangci-lint-version` default emits a `::warning`, never a failure.

### `go-coverage.yml`

Gates a profile the caller produced; it runs no tests. Thresholds and exclusions come only from the caller's `.testcoverage.yml` (also read by the pre-push hook); `module-prefix` comes from `go.mod`.

```yaml
test:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version-file: go.mod
    - run: go test -race -failfast -coverpkg=./... -covermode=atomic -coverprofile=coverage.raw.out -count=1 ./...
    - uses: actions/upload-artifact@v4
      with:
        name: coverage-profile
        path: coverage.raw.out

coverage:
  needs: test
  permissions:
    contents: read
    pull-requests: write
  uses: tclavelloux/promy-github-workflows/.github/workflows/go-coverage.yml@go-coverage/v2
  secrets: inherit
```

- A missing `.testcoverage.yml` fails the job (no zero-threshold fallback). Canonical shape: `promy-crm`'s.
- One sticky PR comment: statement-weighted per-package breakdown; on failure, every file below threshold with its uncovered line ranges. The comment posts before the check goes red.
- Exact `test` and `coverage` job shapes: rules `ci-test-artifact` and `ci-job-permissions`.

### `go-vuln.yml`

Fails on any **called** vulnerability (reachable from the caller's code, not merely in `go.sum`).

```yaml
  vuln:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-vuln.yml@go-vuln/v1
```

### `go-docker.yml`

```yaml
  docker:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-docker.yml@go-docker/v1
```

1. Static gate: each `golang:` base image in each listed Dockerfile must match the `go` directive's minor (`golang:1.26` vs `go 1.26.8` passes).
   - Fails: minor mismatch; a listed file missing (set `dockerfiles` to the files the repo ships); `golang:latest` or any tag without X.Y.
   - Passes with a `::notice`: a file with no `golang:` line. Every `golang:` stage of a multistage file is checked.
2. `docker build`, no push. `build: false` skips it.

Required iff the repo has a root `Dockerfile` (rule `ci-jobs`).

### Python workflows

`python-{lint,typecheck,vuln,test,docker}.yml`, one caller job each:

```yaml
jobs:
  lint:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-lint.yml@python-lint/v1
    with:
      projects: '["src/leaflet","src/store"]'
      workflow-sync: true
  typecheck:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-typecheck.yml@python-typecheck/v1
    with:
      projects: '["src/leaflet","src/store"]'
  # vuln, test, docker: same shape with python-vuln, python-test, python-docker
```

Start from `templates/python/` (`ci.yml`, `dependabot.yml`, `pre-commit-config.yaml`, `Makefile`, `vuln-fix.yml`).

Shared contract:

- `projects`: JSON array string of uv project directories (a directory with a tracked `uv.lock`). `workflow-sync` rule `ci-projects` requires exactly the repo's uv projects (`docker`: those with a `Dockerfile`).
- Check names are fixed per project: `lint / lint (src/store)`. Use those names in the ruleset's required checks.
- A listed project without its `uv.lock` (or `Dockerfile` for `python-docker`) fails the job.
- uv version: the caller's `.uv-version`. Missing or malformed fails; no `latest` fallback.
- `uv sync --frozen` everywhere. pytest, pytest-cov, deptry and basedpyright come from the project's own `dev` group.
- No `permissions:` and no secrets on these caller jobs (rule `ci-job-permissions`).

#### `python-lint.yml`

| Check | Fails when |
|---|---|
| `ruff check`, `ruff format --check` | Findings, with the caller's `ruff.toml`, at the `ruff-version` input |
| projects | A listed project lacks `pyproject.toml` or `uv.lock`, or a tracked `uv.lock` is not listed |
| uv pin drift | `.uv-version`, the `astral-sh/uv-pre-commit` `rev:`, a `COPY --from=ghcr.io/astral-sh/uv:X` or a `[tool.uv] required-version` floor disagree |
| ruff pin drift | `.ruff-version`, the `astral-sh/ruff-pre-commit` `rev: vX` or a `required-version` floor in `ruff.toml` / `[tool.ruff]` disagree |
| `.python-version` | Missing in a project, disagrees with `FROM python:X.Y`, or violates `requires-python` |
| `uv lock --check` | `uv.lock` stale against `pyproject.toml` |
| deptry | Findings in the frozen env |
| `workflow-sync` | Findings, when `workflow-sync: true`. Needs `- id: workflow-sync` at `hooks/v1.4.0` or later (the Python profile) |

- Drift checks read tracked files only and discover every uv project, template directories included. One annotation per mismatching file.
- A missing pin in a uv project is an error: no `.uv-version`, no `required-version`, a Dockerfile with no uv `COPY`.
- `required-version` is a floor; `.uv-version` must satisfy it. An exact `==X` passes with a warning (it breaks Dependabot's uv updater).
- `.ruff-version` differing from the `ruff-version` input warns only.
- Logic: `scripts/python_pins.py`, inlined into the workflow; tests in `scripts/tests/`.

#### `python-typecheck.yml`

`basedpyright --baselinefile .basedpyright/baseline.json` per project. New errors and warnings fail; baselined ones pass.

- A missing baseline fails with the command to create it. Set `require-baseline: false` only for a project with zero findings: basedpyright writes no file then.
- Regenerate a baseline only to shrink it.

#### `python-vuln.yml`

`osv-scanner scan source --lockfile <project>/uv.lock`. No reachability analysis: any advisory in the lock fails, minus ignores.

- Config: `<project>/osv-scanner.toml`, else root `osv-scanner.toml`. Each ignore needs `id`, `reason`, `ignoreUntil` within 90 days (rule `osv-ignore-expiry`).
- The binary is rejected unless its sha256 equals the `osv-scanner-sha256` input.

#### `python-test.yml`

`pytest -m "<markers>" --cov --cov-fail-under=<fail_under>` per project.

- Floor: `[tool.coverage.report] fail_under` in the project's `pyproject.toml`, also used by `make check-coverage`. Missing, zero, negative, non-numeric or above 100 fails.
- Warns when `[tool.coverage.run]` has no `source`.
- Logic: `scripts/python_coverage_floor.py`, inlined.

#### `python-docker.yml`

Builds each project's Dockerfile (`linux/amd64`, no push), then `docker run --rm <image> <smoke-command>`, which must exit 0. Timeout 45 minutes.

### `docs-lint.yml`

Runs `workflow-sync` (docs profile) in a repo with no `go.mod` or `uv.lock`. Single job, check name `lint / lint`. No inputs, no secrets.

```yaml
jobs:
  lint:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/docs-lint.yml@docs-lint/v1
```

- Root `.workflow-sync-profile` holding `docs`. A root `go.mod` or a tracked `uv.lock`/`pyproject.toml` beside it fails with `profile`.
- `.pre-commit-config.yaml`: `- id: workflow-sync` at `hooks/v1.8.0` or later; earlier tags report `profile`.
- Start from `templates/docs/`.

### `pr-title.yml`

Fails when the PR title is not a Conventional Commits string. The squash commit on `main` is the PR title, and release-please parses it.

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

Copy it verbatim (rule `pr-title`):

- Job-level `permissions:` is mandatory. The repo default token has `pull-requests: none`; without the grant the run reports `startup_failure` with no jobs and no log.
- `edited` is mandatory: without it, fixing a title never re-runs the check.
- `pull_request`, never `pull_request_target` (rule `dangerous-trigger`).

Defaults match the fleet type list (rule `precommit-commit-msg`), so callers pass no `with:`.

### `release-please.yml`

Opens and updates the release PR from Conventional Commits on `main`; cuts the tag and GitHub release on merge.

Copy `templates/release-please.yml` verbatim to `.github/workflows/release-please.yml` (rule `release-please`): `on: push: [main]`, job `release-please` with exactly `contents: write` + `pull-requests: write`, `uses: ...release-please.yml@release-please/v1`.

- Map the two App secrets explicitly; `secrets: inherit` fails `workflow-sync`.
- With the App secrets, the release PR is opened by the App, so CI runs on it, and its tags trigger the caller's `push: tags` / `release` workflows. A failed token mint fails the run.
- Without them, `GITHUB_TOKEN` opens the PR, CI does not run, and the run emits a `::warning`. Close and reopen the PR before merging.
- Release PRs are never auto-merged.
- Release PR branch: `release-please--branches--main--components--<package-name>` when `package-name` is set.
- `promy-frontend` has no `workflow-sync` profile: copy `templates/release-please.yml` by hand, after adding the App secrets.

### `dependabot-automerge.yml`

Squash-merges a Dependabot or `promy-fleet-bot` vuln-fix PR once every check on its head commit is green. Gate rules and refusal reasons: [docs/dependency-automation.md](docs/dependency-automation.md#automerge-gate).

Last job of the caller's `ci.yml` (rule `ci-automerge`; template `templates/ci-automerge-job.yml`):

```yaml
  automerge:
    needs: [lint, vuln, test, coverage, gitleaks]   # every other job
    if: github.event.pull_request.user.login == 'dependabot[bot]' || github.event.pull_request.user.login == 'promy-fleet-bot[bot]'
    permissions:
      contents: write
      pull-requests: write
      checks: read
      actions: read
      statuses: read
    uses: tclavelloux/promy-github-workflows/.github/workflows/dependabot-automerge.yml@dependabot-automerge/v1
```

Defaults are the Go behaviour. A uv repo adds:

```yaml
    with:
      ecosystems: uv
      exclude-dependencies: |
        torch*
        numpy
```

A refusal exits 0 with a `::notice` and a step summary.

### Vuln fix

`go-vuln-fix.yml` (govulncheck, called advisories) and `python-vuln-fix.yml` (osv-scanner over each `uv.lock`) run weekly and open one `fix(deps)` PR, or update a rolling issue for what needs a human. Behaviour, refusals and dedupe: [docs/dependency-automation.md](docs/dependency-automation.md#vuln-fix).

Copy the caller to `.github/workflows/vuln-fix.yml` from `templates/go-vuln-fix-caller.yml` or `templates/python/vuln-fix.yml` (rule `vuln-fix`): `schedule` + `workflow_dispatch`, non-cancelling concurrency group, job `fix` with exactly `contents: write`, `pull-requests: write`, `issues: write`. Python also sets `projects` to every uv project.

- Map the App secrets explicitly; never `secrets: inherit`. Without them, `GITHUB_TOKEN` opens the PR and CI does not run on it: close and reopen it before merging.
- Closing a fix PR without merging rejects that advisory set permanently.
- Every run emits `::notice title=vuln-record::<date> <repo> <IDs|none> <action>`.

### `pin-drift.yml`

Not reusable. Monthly and on `workflow_dispatch`, compares every pin in `pin-manifest.json` with upstream and keeps one rolling issue (`<!-- pin-drift -->`). Detect-only. Manifest and bump procedures: [docs/pin-bumps.md](docs/pin-bumps.md).

## Pre-commit hooks

`.pre-commit-hooks.yaml` declares the hooks; scripts live in `hooks/`. The repo is public: pre-commit clones it over anonymous HTTPS.

```yaml
repos:
  - repo: https://github.com/tclavelloux/promy-github-workflows
    rev: hooks/vX.Y.Z   # latest immutable tag; never hooks/v1
    hooks:
      - id: no-direct-commit-to-main
      - id: go-vuln        # Go repos; Python repos use py-vuln
      - id: workflow-sync
```

Rule `precommit-governance-repo` requires this entry with an immutable rev and both `no-direct-commit-to-main` and `workflow-sync`. Rule `vuln-target` requires `go-vuln`/`py-vuln` iff the Makefile has a `vuln:` target.

### `no-direct-commit-to-main`

Fails a commit on a protected branch (`main` by default) or on a detached `HEAD`. Other branches: `args: [main, release]`. Early feedback only; the server-side `protect-main` ruleset is the control.

### `go-vuln`

Fleet-pinned `govulncheck`, same Go toolchain as CI. Stages `pre-push`, `manual`.

- Version: `.govulncheck-version` at the pinned `rev:`.
- `GOTOOLCHAIN` set from `go.mod` (`toolchain`, else `go`) when it is a full `x.y.z`.
- Binary cached under `${XDG_CACHE_HOME:-~/.cache}/promy-go-vuln/<version>/`.

| Context | Mode |
|---|---|
| `pre-push`, range touches `go.mod`/`go.sum` | block |
| `pre-push`, otherwise | warn (exit 0) |
| `manual`, `make vuln`, `--all-files` | block |

- `GO_VULN_MODE=block|warn` overrides. Block mode fails closed on scanner errors.
- `make vuln` target: `templates/Makefile.vuln.mk` (`pre-commit run go-vuln --hook-stage manual`).
- Bypass once: `SKIP=go-vuln git push`.

### `py-vuln`

osv-scanner over every tracked `uv.lock`, with `python-vuln.yml`'s config lookup. Stages `pre-push`, `manual`.

- Version: `.osv-scanner-version` at the pinned `rev:` (`OSV_SCANNER_VERSION` overrides). Installed with `go install`, so Go must be on `PATH`.
- Blocks a push that changes a `uv.lock`, `pyproject.toml` or `osv-scanner.toml`; warns otherwise; `manual`, `make vuln`, `--all-files` block. `PY_VULN_MODE=block|warn` overrides.
- `make vuln` target: `templates/python/Makefile`. Bypass once: `SKIP=py-vuln git push`.

### `workflow-sync`

Checks the repo's CI wiring (`ci.yml`, `pr-title.yml`, `release-please.yml`, `vuln-fix.yml`, `dependabot.yml`, action pins, `.pre-commit-config.yaml`, `Makefile`, pinned tool files) against the policy at the caller's pinned `rev:`. Profile Go, Python or docs. `language: golang`: needs `go` on `PATH`. Stages `pre-commit`, `manual`.

- CI runs it through `go-lint`/`python-lint` with `workflow-sync: true`, or `docs-lint`.
- Repo settings CI cannot read: `GH_TOKEN= scripts/check-repo-settings.sh`.
- Rules, exemptions, limits: [docs/workflow-sync.md](docs/workflow-sync.md).
- No `.githooks/` and no `core.hooksPath`: `pre-commit install` refuses while it is set (rules `no-githooks`, `make-hookspath`).

## This repo's CI

`.github/workflows/ci.yml`, all blocking on every PR:

- `actionlint` (`.actionlint-version`): parses every workflow, shellchecks each `run:`.
- `zizmor` (`.zizmor-version`): security audit of the workflows.
- `workflow-sync`: `gofmt -l`, `go vet`, `go test ./...`. `governance_test.go` fails when `policy.go` drifts from the workflow defaults, `.pre-commit-hooks.yaml`, `.osv-scanner-version` or `templates/`; `python_workflows_test.go` fails when an inlined script differs from `scripts/` or a workflow breaks a hard rule (SHA pins, `persist-credentials: false`, read-only token, inputs through `env:`, timeout).
- `python-scripts`: `python3 -m unittest discover -s scripts/tests`.

Every `uses:` here is pinned to a full commit SHA with a `# vX.Y.Z` comment. Dependabot (`github-actions`, weekly) bumps the SHAs; it never moves the `<workflow>/vN` tags, which are cut by hand.

<!-- readme-updated-at: 59f5fd1 -->
