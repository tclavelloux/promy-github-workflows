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
  uses: tclavelloux/promy-github-workflows/.github/workflows/go-coverage.yml@v2
  secrets: inherit
```

Nothing else is required as long as the caller has a `.testcoverage.yml` at its repo root (see `promy-crm`'s for the canonical shape: `profile`, `threshold.total`/`.package`/`.file`, `exclude.paths`).

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `post-pr-comment` | `true` | Post/refresh the coverage breakdown PR comment |
| `coverage-artifact` | `coverage-profile` | Name of the artifact uploaded by the caller's `test` job |
| `profile-filename` | `coverage.raw.out` | Filename of the coverage profile inside that artifact |

No threshold or exclusion inputs exist. `v1` had `total-threshold`, `package-threshold`, `exclude-patterns` and `module-prefix`; every one of them duplicated a value the caller already declares in `.testcoverage.yml` or `go.mod`, and `go-test-coverage` treats each `threshold-*` flag as an override of the config file. `promy-crm` drifted 33 points between the two for months. An input that *can* contradict the source of truth eventually will, so `v2` removes the ability.

`.testcoverage.yml` at the caller's repo root is mandatory. The workflow fails with an explicit error if it is absent, rather than letting `go-test-coverage` fall back to a zero-threshold config and pass everything.

The PR comment's per-package breakdown and low-coverage listing are built from `go-test-coverage`'s own `--breakdown-file-name` output (already exclusion-filtered by the tool) plus `go tool cover -func`, restricted to files present in that same breakdown — a plain allowlist membership check, not a second regex-exclusion engine. The low-coverage section is function-level, same as before.

## `go-lint.yml`

Installs a fixed `golangci-lint` version via `go install` and runs it. This is the single place the fleet's linter version is decided — bumping the `golangci-lint-version` default here is one PR, not six. Deliberately mirrors `go-coverage.yml`'s `workflow_call` shape (same input-description style, same step naming) but installs from source rather than a prebuilt binary, matching `promy-template-go/scripts/golangci-lint.sh` — so "local matches CI" is literally true, not just intended.

```yaml
jobs:
  lint:
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-lint.yml@v1
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

`go-lint.yml` accepts `${{ inputs.* }}` into `run:` steps via an `env:` mapping rather than interpolating them directly into the shell string, specifically to close the shell-injection surface that direct interpolation opens (a caller-controlled `args` or `golangci-lint-version` string could otherwise break out of the intended command). `go-coverage.yml` now follows the same convention throughout.

If the caller has a `.golangci-version` file (see `promy-template-go`), the workflow emits an `::warning` — never a failure — when it drifts from the resolved `golangci-lint-version`. A warning, not a hard failure, so a version bump here doesn't turn all six repos red until each lands its own PR: that would convert free propagation into six mandatory PRs.

### Versioning

Tag releases (`v1`, `v2`, ...) rather than pinning consumers to `@main` — a breaking change to this workflow should require an explicit opt-in bump in each caller, not a silent flip across every `promy-*` service on the next push.

A major alias is a **moving ref**, not a frozen one: it gains additive, backward-compatible changes (new optional inputs, new default versions) without a new tag. Immutable `vX.Y.Z` tags exist alongside it for any caller that needs to freeze at an exact revision. This is deliberate: the entire value proposition of this repo — "a version bump is one PR here, not six" — requires callers to track a moving ref. If `v1` were frozen at first release, the next `golangci-lint`/coverage-tool bump would cost seven PRs (one here, plus one per caller to re-pin), not six.

The corollary is that a semantic change must not ride a moving alias. `go-coverage.yml@v2` moves exclusions from a workflow input to the caller's `.testcoverage.yml`; shipping that on `v1` would have flipped the exclusion source under all six services with no opt-in, which is exactly what the first paragraph forbids. `v1` is therefore frozen at its final revision and `v2` is the migration target.

### Current majors

| Workflow | Major | Notes |
|---|---|---|
| `go-coverage.yml` | `v2` | `v1` frozen. Migration: delete `total-threshold`, `package-threshold`, `exclude-patterns` and `module-prefix` from the caller and bump the ref. Confirm the caller's `.testcoverage.yml` already carries the intended values first — they are now the only ones that apply. |
| `go-lint.yml` | `v1` | Unaffected by the `go-coverage.yml` major bump; the two are versioned independently per file path. |

## CI

`actionlint` runs on every PR (`.github/workflows/ci.yml`): it parses every workflow file and shellchecks each `run:` block. A bug here is fleet-wide, so it is the only gate before merge.

Its version is pinned in `.actionlint-version`, mirroring how `.golangci-version` pins the fleet's linter.
