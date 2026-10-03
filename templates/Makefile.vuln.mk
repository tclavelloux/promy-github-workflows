# Requires the `go-vuln` hook in .pre-commit-config.yaml (rev hooks/v1.1.0 or later);
# its `stages: [pre-push, manual]` is inherited from the hook definition.
# Same pinned govulncheck + Go toolchain as CI's `vuln` job; blocks on any called vuln.
vuln:
	pre-commit run go-vuln --hook-stage manual
