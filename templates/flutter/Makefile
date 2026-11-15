# Canonical root Makefile for a Flutter repo (workflow-sync Flutter profile).
.PHONY: setup vuln

setup:
	pre-commit install --install-hooks

# Requires the `flutter-vuln` hook in .pre-commit-config.yaml.
# Same pinned osv-scanner and config as CI's `vuln` job; blocks on any advisory.
vuln:
	pre-commit run flutter-vuln --hook-stage manual
