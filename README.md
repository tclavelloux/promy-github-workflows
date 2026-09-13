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

Requires only `contents: read` — unlike `go-coverage.yml`, it posts nothing and needs no `pull-requests: write`.

Beyond `golangci-lint run`, the job also blocks on three gates that previously only ran as local pre-commit hooks (so pushing without hooks installed bypassed them):

- `golangci-lint fmt --diff` — fails if the caller isn't formatted per its own `.golangci.yml`.
- `golangci-lint config verify` — fails if the caller's `.golangci.yml` doesn't validate against the resolved `golangci-lint` version's schema.
- `go mod tidy -diff` — fails if `go.mod`/`go.sum` don't match what `go mod tidy` would produce. Requires Go 1.23+.

All three print a diff and exit non-zero on drift, exit 0 clean — verified directly against `golangci-lint v2.13.2` and `go1.26`, not assumed.

`go-lint.yml` accepts `${{ inputs.* }}` into `run:` steps via an `env:` mapping rather than interpolating them directly into the shell string, specifically to close the shell-injection surface that direct interpolation opens (a caller-controlled `args` or `golangci-lint-version` string could otherwise break out of the intended command). `go-coverage.yml` now follows the same convention throughout.

If the caller has a `.golangci-version` file (see `promy-template-go`), the workflow emits an `::warning` — never a failure — when it drifts from the resolved `golangci-lint-version`. A warning, not a hard failure, so a version bump here doesn't turn all six repos red until each lands its own PR: that would convert free propagation into six mandatory PRs.

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

## CI

`actionlint` and `zizmor` run on every PR (`.github/workflows/ci.yml`), both blocking. A bug here is fleet-wide, so these are the only gates before merge.

- `actionlint` parses every workflow file and shellchecks each `run:` block. Version pinned in `.actionlint-version`.
- `zizmor` audits the same workflows for security anti-patterns actionlint doesn't check — unpinned action refs, credential persistence, template injection. Version pinned in `.zizmor-version`, mirroring `.actionlint-version`. Installed from zizmor's released binary rather than `uvx zizmor`, so the job needs no Python/uv toolchain — same reasoning as the actionlint install step.

### Action-pinning policy

Every `uses:` in this repo's own workflows is pinned to a full commit SHA with a trailing version comment (`actions/checkout@<sha> # v4.4.0`), not to a mutable tag. `vladopajic/go-test-coverage@v2` matters most: it is the only third-party action, it runs in a job holding `pull-requests: write`, and a mutable major tag is one its maintainer can repoint underneath every caller with no review on this side.

Dependabot (`.github/dependabot.yml`, `github-actions` ecosystem, weekly) is what keeps these pins from going stale — pinning without Dependabot just freezes the fleet on old actions. Each Dependabot PR bumps one SHA and its version comment; it does not touch the per-workflow moving major tags (`go-lint/v1`, `go-coverage/v2`, `go-vuln/v1`, `go-docker/v1`) that callers track — those are managed by hand, per the Versioning section above.
