# Canonical root Makefile for a uv-managed Python repo (workflow-sync Python
# profile). Derived from promy-market-catalog. Replace src/app and src/worker.
.PHONY: setup check-coverage lock vuln

# Every uv call goes through $(UV): make uses the fleet pin whatever uv is on PATH
# (Homebrew drifts); uvx caches it. Expanded by make at the repo root, so `cd` is safe.
UV := uvx uv@$(shell cat .uv-version)

setup:
	pre-commit install --install-hooks

# Same pytest + coverage run as the `test` CI job. The floor is `fail_under` in each
# project's pyproject.toml; coverage reads it, so CI and this target cannot disagree.
check-coverage:
	cd src/app && $(UV) run --frozen pytest -m "not network and not model and not gcp" --cov --cov-report=term-missing:skip-covered
	cd src/worker && $(UV) run --frozen pytest -m "not network and not model and not gcp" --cov --cov-report=term-missing:skip-covered

# The only sanctioned local lock writer: same uv as CI, the vuln-fix bot and Dependabot.
lock:
	$(UV) lock --project src/app
	$(UV) lock --project src/worker

# Requires the `py-vuln` hook in .pre-commit-config.yaml.
# Same pinned osv-scanner and config lookup as CI's `vuln` job; blocks on any advisory.
vuln:
	pre-commit run py-vuln --hook-stage manual
