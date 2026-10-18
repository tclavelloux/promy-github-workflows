# Canonical root Makefile for a uv-managed Python repo (workflow-sync Python
# profile). Derived from promy-market-catalog. Replace src/app and src/worker.
.PHONY: setup check-coverage vuln

setup:
	pre-commit install --install-hooks

# Same pytest + coverage run as the `test` CI job. The floor is `fail_under` in each
# project's pyproject.toml; coverage reads it, so CI and this target cannot disagree.
check-coverage:
	cd src/app && uv run --frozen pytest -m "not network and not model and not gcp" --cov --cov-report=term-missing:skip-covered
	cd src/worker && uv run --frozen pytest -m "not network and not model and not gcp" --cov --cov-report=term-missing:skip-covered

# Requires the `py-vuln` hook in .pre-commit-config.yaml (rev hooks/v1.4.0 or later).
# Same pinned osv-scanner and config lookup as CI's `vuln` job; blocks on any advisory.
vuln:
	pre-commit run py-vuln --hook-stage manual
