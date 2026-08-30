# promy-github-workflows

Reusable GitHub Actions workflows shared across `promy-*` services. Single-purpose repo — `main` only ever changes for CI-infra reasons, so a caller's gate never breaks for reasons unrelated to CI.

## `go-coverage.yml`

Runs the full race+coverage test suite, excludes generated/untestable paths, gates on `vladopajic/go-test-coverage`, and posts a per-package + low-coverage breakdown as a PR comment.

```yaml
coverage:
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
| `exclude-patterns` | `main.go`, `internal/bootstrap.go`, `cmd/`, `test/mock.go`, `testdata` | Newline-separated regexes dropped from the coverage profile before gating — applies to both the threshold check and the PR comment, so generated/untestable code never pads either |
| `post-pr-comment` | `true` | Post/refresh the coverage breakdown PR comment |
| `module-prefix` | `""` | Go module prefix stripped from paths in the PR comment, e.g. `github.com/org/repo/` |

### Versioning

Tag releases (`v1`, `v2`, ...) rather than pinning consumers to `@main` — a breaking change to this workflow should require an explicit opt-in bump in each caller, not a silent flip across every `promy-*` service on the next push.
