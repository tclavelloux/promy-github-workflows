# workflow-sync

## What runs where

| Where | What | Trigger |
|---|---|---|
| Local hook | `workflow-sync` from `.pre-commit-hooks.yaml`, built with the system `go` (`language: golang`) | `pre-commit` stage on every commit; `pre-commit run workflow-sync --all-files` by hand |
| CI | `Check fleet governance (workflow-sync)`, last step of `go-lint.yml`'s `lint` job | Caller sets `with: workflow-sync: true` on its `lint` job |
| By hand | `scripts/check-repo-settings.sh` | `GH_TOKEN= scripts/check-repo-settings.sh [owner/repo ...]` |
| Governance repo CI | `workflow-sync` job in `ci.yml`: gofmt, `go vet`, `go test` | Every PR here |

- Local hook and CI step run the same code at the same immutable `rev:` of the caller's `.pre-commit-config.yaml`. Policy and checker ship together.
- The hook needs Go on `PATH` locally. In CI, `setup-go` has already run in the `lint` job.
- The hook has `always_run: true`, `pass_filenames: false`, `verbose: true`. Verbose prints every exemption on success.
- `--root DIR` checks another directory; default `.`.
- Output: `<file>:<line>: [<rule>] <message>`. With `GITHUB_ACTIONS=true` it also emits `::error file=..,line=..,title=workflow-sync [<rule>]::<message>` annotations.
- Summary line: `workflow-sync: FAIL|OK — N violation(s), M exemption(s)`. Exit 1 on violations, 2 on usage or IO error.
- A sync failure shows as `lint` red. The annotation title names `workflow-sync`.
- The CI step uses `if: inputs.workflow-sync && !cancelled()`, so it reports even when golangci-lint failed.

## Rules

Source of truth: `internal/syncheck/policy.go` and the `register(...)` calls in `internal/syncheck/*.go`.

### All `.github/workflows/*.y{a,}ml`

| Rule | File | Invariant | Why |
|---|---|---|---|
| `sha-pin` | every workflow | Non-local step and job `uses:` is `@<40 lowercase hex>` with trailing `# vX[.Y[.Z]]`. `./` and `docker://` skipped; governance refs go to `governance-ref` | Mutable tags can be repointed without review; the comment lets Dependabot and reviewers see the version |
| `governance-ref` | every workflow | `tclavelloux/promy-github-workflows/.github/workflows/<name>.yml@<name>/v<major>`; majors: go-coverage 2, all others 1 | Callers track the moving per-workflow major. Bare `v1`, `main`, SHAs, immutable `x/v1.2.3` and frozen majors fail |
| `persist-credentials` | every workflow | Every `actions/checkout` step has `with.persist-credentials: false` | Keeps the token out of `.git/config` for later steps |
| `workflow-permissions` | every workflow | Top-level `permissions:` present | Default token is least privilege; jobs widen explicitly |
| `dangerous-trigger` | every workflow | No `pull_request_target`, no `workflow_run` | Base-repo token and secrets against untrusted input |
| `unpinned-tool` | every workflow, `Makefile` | `run:` / recipe contains neither `releases/latest` nor `@latest` | Floating tool versions change CI without a diff |

### `ci.yml`

| Rule | Invariant | Why |
|---|---|---|
| `ci-trigger` | `on` is exactly `pull_request` with `branches: [main]` and `types` = {opened, synchronize, ready_for_review, reopened}. No `push` | One run per PR head; `ready_for_review` re-runs jobs skipped while draft |
| `ci-concurrency` | group `${{ github.workflow }}-${{ github.ref }}`, `cancel-in-progress: true` | Superseded pushes stop burning minutes |
| `ci-permissions` | Top-level exactly `{contents: read}` | Called workflows can only narrow the caller's token |
| `ci-jobs` | Required: lint, vuln, test, coverage, gitleaks. `docker` required iff a root `Dockerfile` exists, forbidden otherwise. Optional: automerge. Anything else is an unknown job | Same gate set in every repo |
| `ci-job-ref` | lint, vuln, docker, coverage, automerge `uses:` the exact governance ref for go-lint, go-vuln, go-docker, go-coverage, dependabot-automerge | Policy majors move in one place |
| `ci-draft-skip` | Every job except automerge has `if: github.event.pull_request.draft == false` | Drafts do not spend minutes |
| `ci-timeout` | Inline (`runs-on`) jobs set `timeout-minutes` (value free) | A hung job cannot burn the budget |
| `ci-job-permissions` | lint, vuln, docker, test carry no `permissions:`. coverage: exactly `{contents: read, pull-requests: write}`, `needs: test`, `secrets: inherit`. gitleaks: exactly `{contents: read, pull-requests: read}`, uses `gitleaks/gitleaks-action`, checkout `fetch-depth: 0` | Read default is the repo setting; only coverage (PR comment) and gitleaks need more |
| `ci-test-artifact` | `test` job uploads `actions/upload-artifact` with name `coverage-profile`, path `coverage.raw.out`, `retention-days: 1`, `if-no-files-found: error` | Contract with `go-coverage.yml`; a missing profile must fail, not pass |
| `ci-lint-sync` | `lint` job has `with.workflow-sync: true` | Without it CI skips this check and only the local hook guards the repo |
| `ci-automerge` | If present: last job; `needs` = every other job; `if` = `github.event.pull_request.user.login == 'dependabot[bot]'`; permissions exactly `{contents: write, pull-requests: write, checks: read, actions: read, statuses: read}` | A job missing from `needs:` could merge past a red check |

### Other workflow files

| Rule | File | Invariant | Why |
|---|---|---|---|
| `pr-title` | `pr-title.yml` | Exists. `on.pull_request.types` = {opened, edited, reopened, synchronize}. Top-level `{contents: read}`. One job `pr-title`, permissions `{contents: read, pull-requests: read}`, uses `pr-title/v1` | PR title is the squash commit release-please parses; without `edited` a fixed title never re-checks |
| `release-please` | `release-please.yml` | Exists. `on` is exactly `push: {branches: [main]}`. A step uses `googleapis/release-please-action` | Releases cut from `main` only |
| `vuln-fix` | `vuln-fix.yml` (optional) | `on` = `schedule` plus optional `workflow_dispatch`. Top-level `{contents: read}`. Concurrency `{group: go-vuln-fix, cancel-in-progress: false}`. One job `fix`, permissions exactly `{contents: write, pull-requests: write, issues: write}`, uses `go-vuln-fix/v1` | A running fix is never cancelled mid-PR; no broader token |
| `dependabot` | `.github/dependabot.yml` | Exists with `gomod` and `github-actions` entries at directory `/` | Both ecosystems are the bump surface |

### `.pre-commit-config.yaml`

| Rule | Invariant | Why |
|---|---|---|
| `precommit-install-types` | `default_install_hook_types` = {pre-commit, commit-msg, pre-push} | One `pre-commit install` wires every stage |
| `precommit-governance-repo` | Exactly one entry for `https://github.com/tclavelloux/promy-github-workflows`; `rev` matches `^hooks/v\d+\.\d+\.\d+$`; hooks include `no-direct-commit-to-main` and `workflow-sync` | Immutable rev; the branch guard is the only main protection on the free plan |
| `precommit-rev` | No repo `rev` is `main`, `master` or `HEAD` | pre-commit never re-resolves a moving rev |
| `precommit-coverage-hook` | Local hook `check-coverage`: entry `make check-coverage`, `language: system`, `pass_filenames: false`, `always_run: true`, `stages: [pre-push]` | Local coverage gate matches CI |
| `precommit-no-test-hook` | No hook id `test`; no pre-push hook other than `check-coverage` whose entry contains `go test` or `make test` | CI owns the test run; `check-coverage` already runs the suite once |
| `precommit-commit-msg` | `conventional-pre-commit` with `stages: [commit-msg]` and `args` = the 11 policy types (feat fix docs style refactor perf test build ci chore revert) | Matches the `pr-title.yml` type list |
| `vuln-target` | `go-vuln` hook present iff the Makefile has a `vuln:` target; recipe exactly `pre-commit run go-vuln --hook-stage manual` | `make vuln` and the hook cannot diverge |

### `Makefile`

| Rule | Invariant | Why |
|---|---|---|
| `make-setup` | `setup:` recipe is exactly `pre-commit install --install-hooks` | One bootstrap command |
| `make-hookspath` | No `core.hooksPath` anywhere in the Makefile | pre-commit refuses to install while it is set |
| `make-coverage-pin` | `install-go-test-coverage:` installs `github.com/vladopajic/go-test-coverage/v2@v2.19.0` (policy version); `check-coverage:` target exists | Local tool equals the CI action version |

### Repo files

| Rule | File | Invariant | Why |
|---|---|---|---|
| `no-githooks` | `.githooks/` | No tracked files (`git ls-files`; filesystem check when `--root` is not a git toplevel) | `core.hooksPath` and pre-commit are mutually exclusive |
| `golangci-version` | `.golangci-version` | Equals `2.13.2` (the `go-lint.yml` default), leading `v` ignored | Local lint equals CI lint |
| `testcoverage` | `.testcoverage.yml` | Exists; leading comment block mentions `check-coverage` and `go-coverage.yml` | One file feeds the pre-push hook and CI; the comment says so |

### Engine rules

| Rule | Meaning |
|---|---|
| `yaml-parse` | A checked YAML file does not parse |
| `bad-exemption` | Marker with no rule, unknown rule, no reason, or naming `bad-exemption` / `stale-exemption`. Not exemptable |
| `stale-exemption` | Marker that suppressed nothing. Not exemptable |

## Not governed

Measured locals, never flagged:

- `timeout-minutes` values (presence only)
- `services:`
- `env:`
- test command
- coverage floors in `.testcoverage.yml`
- dependency lists
- exact third-party action SHAs (Dependabot owns them; only pinned-ness is checked)
- release-please `with:`
- extra pre-commit hooks and hygiene hook revs

## Exemptions

```yaml
uses: actions/checkout@v4 # sync-exempt: sha-pin vendored fork, tracked in #123
# sync-exempt-file: persist-credentials release job pushes tags with the checkout token
```

- `# sync-exempt: <rule> <reason>`: scope is the line the finding reports, as a trailing comment, or any line of the contiguous comment block directly above it. For an absent key (e.g. a job with no `if:`) the finding reports the parent key's line (`gitleaks:`), so put the marker above that line.
- `# sync-exempt-file: <rule> <reason>`: whole file.
- Reason is mandatory. No reason, unknown rule, or `bad-exemption` / `stale-exemption` as the rule fails with `bad-exemption`.
- A marker that suppresses nothing fails with `stale-exemption`. Delete it when the cause is fixed.
- Every applied exemption prints: `workflow-sync: EXEMPT <file>:<line> [<rule>] <reason>`.
- Markers work in YAML and Makefile (`#` comments).

## Limits

- Cannot block a human merge in the five private repos. Free plan, no branch protection (`gh api .../branches/main/protection` returns 403). Red `lint` blocks only `automerge`, via `needs: lint`. promy-event-bus is public (API returns 404 `Branch not protected`), so a ruleset requiring `lint` can make it blocking there.
- Cannot stop `SKIP=workflow-sync` or `--no-verify` locally. CI still runs the step when the caller wired `workflow-sync: true`.
- Cannot stop a caller pinning an old `rev:`. The old policy passes; visible in review only. The `ci-lint-sync` and `precommit-governance-repo` rules need the caller to be on a rev that has them.
- Cannot stop a caller dropping `with: workflow-sync: true`: `ci-lint-sync` catches it only when the local hook runs.
- Cannot read repo settings; `GITHUB_TOKEN` has no access. `scripts/check-repo-settings.sh` does, run by hand with the keyring token.
- `scripts/check-repo-settings.sh` checks per repo:
  - Actions default permissions `read`; Actions may create PRs (`can_approve_pull_request_reviews`), needed by `dependabot-automerge` and `go-vuln-fix`.
  - Squash merge on, merge commit off, rebase off. The PR title is the commit release-please parses.
  - Vulnerability alerts enabled; automated security fixes enabled.
- Output: `ok|FAIL <repo> <check> (got <value>)`. Exit 1 on any FAIL, 2 when `gh` errors. `GH=<path>` swaps the binary for tests.

## Change the policy

1. Edit `internal/syncheck/policy.go` (constants, majors, job sets, permission sets) and the rule code if the shape changes.
2. Add or update a fixture under `internal/syncheck/testdata/` and a table test.
3. Run `go test -count=1 ./...`. `governance_test.go` guards the duplicated values:
   - go-test-coverage version equals the `go-coverage.yml` pin comment.
   - golangci-lint version equals the `go-lint.yml` default.
   - Commit types equal the `pr-title.yml` `types` default.
   - Every `workflow_call` workflow has a policy major, and the reverse.
   - `templates/` (automerge job, vuln-fix caller, `Makefile.vuln.mk`, `pre-commit-go-vuln.yaml`, `dependabot.yml`) pass the rules.
4. Merge, then cut a new immutable `hooks/vX.Y.Z`. Never move an existing `hooks/v*` tag.
5. Callers bump `rev:` (`pre-commit autoupdate` or by hand). Until they do, they run the old policy. For a workflow-side change, also move the matching `go-lint/v1`.

## Rollout of hooks/v1.2.0

Governance repo, once:

1. Merge the PR adding `workflow-sync`.
2. Cut signed tags on the merge commit:
   - `hooks/v1.2.0` (new, immutable; never move `hooks/v1.0.0` or `hooks/v1.1.0`)
   - `go-lint/v1.2.0`, then move `go-lint/v1` to it
   ```bash
   git tag -s hooks/v1.2.0 -m hooks/v1.2.0 <sha>
   git tag -s go-lint/v1.2.0 -m go-lint/v1.2.0 <sha>
   git tag -sf go-lint/v1 -m go-lint/v1 <sha>
   git push origin hooks/v1.2.0 go-lint/v1.2.0
   git push --force origin go-lint/v1
   ```

Per repo, one PR, combined with the pending dependency-automation rollout ([dependency-automation.md](dependency-automation.md)); skip `hooks/v1.1.0` as a caller rev:

`.pre-commit-config.yaml`:

```yaml
  - repo: https://github.com/tclavelloux/promy-github-workflows
    rev: hooks/v1.2.0
    hooks:
      - id: no-direct-commit-to-main
      - id: go-vuln
      - id: workflow-sync
```

`.github/workflows/ci.yml`, `lint` job:

```yaml
  lint:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/go-lint.yml@go-lint/v1
    with:
      workflow-sync: true
```

Order:

1. promy-event-bus. Fix `registry.yaml` in the same PR: SHA-pin `actions/checkout` with `# vX.Y.Z`, add `persist-credentials: false`, add top-level `permissions: {contents: read}`, pin `yq` to an exact release and verify its sha256 instead of `releases/latest`.
2. promy-template-go
3. promy-identifier
4. promy-crm
5. promy-product
6. promy-user

Deliberate-failure checks (a green check proves nothing):

- Local: in each repo, drift the working copy (drop `persist-credentials: false`, or remove `- id: workflow-sync`) and run `pre-commit run workflow-sync --all-files`. Expect exit 1 and the rule id. Revert.
- CI: on promy-event-bus, push the wiring commit before the `registry.yaml` fix. Expect `lint` red with `sha-pin`, `persist-credentials`, `workflow-permissions` and `unpinned-tool` annotations. Then push the fix and expect green.
- Settings: run the script against a stub `gh` returning `write` for `default_workflow_permissions`. Expect a `FAIL` line and exit 1.
  ```bash
  GH=./stub-gh scripts/check-repo-settings.sh tclavelloux/promy-crm; echo $?
  ```
- Then run the real script: `GH_TOKEN= scripts/check-repo-settings.sh`. Expect all `ok`.
