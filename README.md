# promy-github-workflows

Reusable GitHub Actions workflows shared across `promy-*` services. Single-purpose repo — `main` only ever changes for CI-infra reasons, so a caller's gate never breaks for reasons unrelated to CI.

## `go-coverage.yml`

Gates a pre-computed coverage profile on `vladopajic/go-test-coverage` and posts a per-package + low-coverage breakdown as a PR comment. It does **not** run your tests — the caller runs `go test -coverprofile=...` itself (with whatever service containers it needs — MySQL, Redis, ...) and uploads the raw profile as an artifact. This keeps the shared workflow decoupled from any repo's service dependencies, and avoids running the test suite twice per CI run.

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
  uses: tclavelloux/promy-github-workflows/.github/workflows/go-coverage.yml@v1
  with:
    total-threshold: 80
    module-prefix: github.com/tclavelloux/promy-crm/
  secrets: inherit
```

### Inputs

| Input | Default | Purpose |
|---|---|---|
| `total-threshold` | *(required)* | Fail below this overall coverage % |
| `package-threshold` | `0` | Fail any package below this % (0 = off) |
| `exclude-patterns` | `main.go`, `internal/bootstrap.go`, `cmd/`, `test/mock.go`, `testdata` | Newline-separated regexes dropped from the coverage profile before gating — applies to both the threshold check and the PR comment, so generated/untestable code never pads either. Add repo-specific paths (e.g. `examples/`) as needed. |
| `post-pr-comment` | `true` | Post/refresh the coverage breakdown PR comment |
| `module-prefix` | `""` | Go module prefix stripped from paths in the PR comment, e.g. `github.com/org/repo/` |
| `coverage-artifact` | `coverage-profile` | Name of the artifact uploaded by the caller's `test` job |
| `profile-filename` | `coverage.raw.out` | Filename of the coverage profile inside that artifact |

### Versioning

Tag releases (`v1`, `v2`, ...) rather than pinning consumers to `@main` — a breaking change to this workflow should require an explicit opt-in bump in each caller, not a silent flip across every `promy-*` service on the next push.
