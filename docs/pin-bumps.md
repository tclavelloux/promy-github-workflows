# Pin bumps

`.github/workflows/pin-drift.yml` opens one rolling issue (marker `<!-- pin-drift -->`) on the 1st of each month when a manually pinned tool is behind upstream or its upstream lookup failed. It bumps nothing; a human follows the procedures below.

- Dependabot already covers GitHub Actions SHAs, `uv.lock` dependencies and Docker tags. Do not add those to the manifest.
- `workflow-sync` and `python-lint` fail when copies of a pin disagree with each other or with `policy.go`. They do not detect "behind upstream".
- Bumping a `policy.go` constant turns every consumer's CI red until it follows. That is the forcing function: release first, then bump consumers.

## Manifest

`pin-manifest.json` at the repo root lists every pin the drift check compares with upstream.

```json
{"name": "osv-scanner",
 "upstream": {"repo": "google/osv-scanner", "kind": "release"},
 "locations": [
   {"file": "internal/syncheck/policy.go", "const": "OSVScannerVersion"},
   {"file": ".osv-scanner-version"}]}
```

- `upstream.kind: release`: `GET /repos/{repo}/releases/latest`, `.tag_name`.
- `upstream.kind: file`: `GET /repos/{repo}/contents/{path}` (`?ref=` when `ref` is set), base64-decoded; `pattern` (one capture group, multiline) must match exactly once and capture a plain version. Fields: `repo`, `path`, `pattern`, optional `ref`. Used for uv: `dependabot/dependabot-core`, `uv/helpers/requirements.txt`, `^uv==(\S+)$`.
- `upstream.exact: true` (optional): the pin must equal upstream. A current value above upstream is status `ahead` and counts in `behind=` and the issue like `behind`. Without it, ahead is `current`.
- `upstream.kind: tag`: highest plain `X.Y.Z` tag under `prefix` (no prereleases, numeric order). Use it when the project publishes no GitHub releases (govulncheck: `golang/vuln`, prefix `v`; hooks rev: prefix `hooks/v`).
- Location `{file}`: the whole file, trimmed, is the version.
- Location `{file, const}`: Go constant `NAME = "x"` in that file.
- Location `{file, pattern}`: regex with exactly one capture group that matches exactly once (multiline). Keep to the RE2 subset: Python and the Go test both compile it.
- A leading `v` is stripped everywhere. When locations of one pin disagree, the lowest value counts as current and the report lists every value.
- A location that does not resolve fails the job (exit 2). A failed upstream lookup is reported as `unknown` with the error, never as current.

Add a pin:

1. Add an entry to `pin-manifest.json` with every location that holds the version.
2. Run `go test ./internal/syncheck -run PinManifest`. It fails for each root `.*-version` file and each `policy.go` constant ending in `Version` that has no entry, and for each location that does not resolve exactly once.
3. Run `python3 scripts/pin_drift.py --manifest pin-manifest.json --out /dev/stdout` with `GH_TOKEN` set to see the live report.

## Bump procedures

uv (one PR in governance, one per consumer):

0. Trigger: pin-drift reports uv differs from Dependabot's bundled uv (behind or ahead). Target the uv in `dependabot-core` `uv/helpers/requirements.txt`, not the latest astral release.
1. Governance: `policy.go` `UVVersion`, the uv-pre-commit `rev` in `templates/python/pre-commit-config.yaml`, README pin text, `docs/workflow-sync.md`, pin tests. Release a new `hooks/vX.Y.Z` tag if the hook changed.
2. Consumer, one PR: `.uv-version`; uv-pre-commit `rev:`; every `COPY --from=ghcr.io/astral-sh/uv:X` in Dockerfiles; `required-version` floors that must rise; `make lock` (or `uvx uv@$(cat .uv-version) lock --project <dir>` per project), never bare `uv lock`: a different local uv writes another lock `revision`.
3. The pin equals the uv Dependabot bundles: it is the one lock writer that cannot be pinned, and a disagreeing writer flips the lock `revision` (uv >= 0.13.0: 5, <= 0.12.x: 3; measurements in `docs/dependency-automation.md`).
4. Leave Dependabot's `ignore: ghcr.io/astral-sh/uv`; `workflow-sync` enforces it. `python-vuln-fix` reads `.uv-version`, so nothing else changes.

ruff:

1. Governance: `policy.go` `RuffVersion`, `python-lint.yml` `ruff-version` default, ruff-pre-commit `rev` in the template.
2. Consumer: `.ruff-version`, ruff-pre-commit `rev:` (with `v`), `ruff.toml` `required-version` floor. A `.ruff-version` that differs from the workflow input only warns; a differing pre-commit rev fails.

osv-scanner:

1. Take the `linux_amd64` line from the release's `osv-scanner_SHA256SUMS`.
2. Edit in one PR: version and sha256 defaults in `python-vuln.yml` and `python-vuln-fix.yml`, `.osv-scanner-version`, `OSVScannerVersion` in `policy.go`, README input table.
3. The sha256 is not a manifest entry because it is not a version. It moves with the version: a stale digest fails the job closed.

govulncheck:

1. Edit `.govulncheck-version`, the `go-vuln.yml` default and the `go-vuln-fix.yml` default together. CI enforces equality. No policy constant.
2. Consumers get the new version through the moving `go-vuln/v1` tag. The hook reads `.govulncheck-version` at its pinned `hooks/` rev, so consumers pick it up only after bumping their hooks rev.

golangci-lint:

1. Governance: `policy.go` `GolangciLintVersion`, `go-lint.yml` default.
2. Every Go consumer: `.golangci-version` (`workflow-sync` rule `golangci-version`).

go-test-coverage:

1. Governance, one PR: bump the action SHA in `go-coverage.yml` and `GoTestCoverageVersion` in `policy.go` together. Dependabot bumps only the SHA, so merge its PR only with the constant, or replace it.
2. Resolve the SHA: `gh api repos/vladopajic/go-test-coverage/git/ref/tags/vX.Y.Z`. When `object.type` is `tag` (annotated), dereference: `gh api repos/vladopajic/go-test-coverage/git/tags/<sha>` and use `object.sha`.
3. Every Go consumer: Makefile `go install github.com/vladopajic/go-test-coverage/v2@vX.Y.Z`.

actionlint, zizmor:

1. Edit `.actionlint-version` or `.zizmor-version`.
2. Fix new findings in the same PR; both gate governance CI.

Governance hooks rev (latest tag: `hooks/v1.6.0`):

1. Governance PR: `rev:` in `templates/python/pre-commit-config.yaml`, the README example and README version text.
2. Each consumer: `pre-commit autoupdate --repo https://github.com/tclavelloux/promy-github-workflows`.
3. Never use the moving `hooks/v1`.

Template revs (pre-commit-hooks, conventional-pre-commit, gitleaks, uv-pre-commit, ruff-pre-commit):

1. Governance PR first: the template is a tested `workflow-sync` pass case.
2. `pre-commit autoupdate --repo <url>`, then restore the uv and ruff revs to match `.uv-version` and `.ruff-version`.
3. Consumers copy the template revs.
