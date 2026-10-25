package syncheck

import "fmt"

// Fleet policy. Plain Go so that a change is a reviewed diff and a hook rev bump.
const (
	GovernanceRepo = "tclavelloux/promy-github-workflows"

	GoTestCoverageVersion = "2.19.0"
	GolangciLintVersion   = "2.13.2"

	// Python tool versions. Ruff and osv-scanner are decided by the reusable
	// workflows; uv by this file alone (python-lint reads the caller's .uv-version).
	// The Python profile requires .ruff-version, the ruff-pre-commit rev and
	// .uv-version to equal these; python-lint ties every other pin to those files.
	RuffVersion       = "0.17.0"  // python-lint.yml ruff-version default
	OSVScannerVersion = "2.6.0"   // python-vuln.yml osv-scanner-version default, .osv-scanner-version (py-vuln hook)
	UVVersion         = "0.11.13" // fleet uv pin; templates/python/pre-commit-config.yaml uv-pre-commit rev

	// OSVIgnoreMaxDays bounds every osv-scanner ignore: Python has no
	// reachability analysis, so an ignore must expire and be re-triaged.
	OSVIgnoreMaxDays = 90

	// AutomergeIf: no status function, so GitHub ANDs success() implicitly.
	AutomergeIf = "github.event.pull_request.user.login == 'dependabot[bot]'"
	DraftSkipIf = "github.event.pull_request.draft == false"

	ConcurrencyGroup = "${{ github.workflow }}-${{ github.ref }}"
)

// WorkflowMajors maps each reusable workflow to the moving major callers pin.
var WorkflowMajors = map[string]int{
	"go-lint":              1,
	"go-vuln":              1,
	"go-coverage":          2,
	"go-docker":            1,
	"pr-title":             1,
	"dependabot-automerge": 1,
	"go-vuln-fix":          1,
	"python-lint":          1,
	"python-typecheck":     1,
	"python-vuln":          1,
	"python-test":          1,
	"python-docker":        1,
	"python-vuln-fix":      1,
}

var CommitTypes = []string{"feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert"}

var CITriggerTypes = []string{"opened", "synchronize", "ready_for_review", "reopened"}
var PRTitleTriggerTypes = []string{"opened", "edited", "reopened", "synchronize"}

// Job sets of ci.yml.
var (
	CIRequiredJobs = []string{"lint", "vuln", "test", "coverage", "gitleaks"}
	CIDockerJob    = "docker"
	CIOptionalJobs = []string{"automerge"}
	// CIReusableJobs call a governance workflow; value is the workflow name.
	CIReusableJobs = map[string]string{
		"lint": "go-lint", "vuln": "go-vuln", "docker": "go-docker",
		"coverage": "go-coverage", "automerge": "dependabot-automerge",
	}
	// CINoPermissionJobs must not carry a permissions: key (top-level read applies).
	CINoPermissionJobs = []string{"lint", "vuln", "docker", "test"}
)

// Job sets of ci.yml in the Python profile. `docker` is required iff a uv
// project has a Dockerfile; automerge is optional, as in Go.
var (
	PyCIRequiredJobs = []string{"lint", "typecheck", "vuln", "test", "gitleaks"}
	PyCIReusableJobs = map[string]string{
		"lint": "python-lint", "typecheck": "python-typecheck", "vuln": "python-vuln",
		"test": "python-test", "docker": "python-docker", "automerge": "dependabot-automerge",
	}
	PyCINoPermissionJobs = []string{"lint", "typecheck", "vuln", "test", "docker"}
	// PyCIAllProjectJobs must list every uv project in with.projects; docker
	// lists every uv project that has a Dockerfile.
	PyCIAllProjectJobs = []string{"lint", "typecheck", "vuln", "test"}
)

type perms map[string]string

var (
	PermsRead         = perms{"contents": "read"}
	PermsCoverage     = perms{"contents": "read", "pull-requests": "write"}
	PermsGitleaks     = perms{"contents": "read", "pull-requests": "read"}
	PermsPRTitleJob   = perms{"contents": "read", "pull-requests": "read"}
	PermsVulnFixJob   = perms{"contents": "write", "pull-requests": "write", "issues": "write"}
	PermsAutomergeJob = perms{"contents": "write", "pull-requests": "write", "checks": "read", "actions": "read", "statuses": "read"}
)

// govRef is the exact `uses:` value for a governance reusable workflow.
func govRef(name string) string {
	return fmt.Sprintf("%s/.github/workflows/%s.yml@%s/v%d", GovernanceRepo, name, name, WorkflowMajors[name])
}

// Pre-commit, Makefile and repo-file policy.
const (
	GovernanceHookRepoURL = "https://github.com/" + GovernanceRepo

	HookBranchGuard   = "no-direct-commit-to-main"
	HookWorkflowSync  = "workflow-sync"
	HookGoVuln        = "go-vuln"
	HookPyVuln        = "py-vuln"
	HookCheckCoverage = "check-coverage"
	HookConventional  = "conventional-pre-commit"

	MakeSetupLine = "pre-commit install --install-hooks"
	VulnRecipe    = "pre-commit run go-vuln --hook-stage manual"
	PyVulnRecipe  = "pre-commit run py-vuln --hook-stage manual"

	RuffHookRepo = "https://github.com/astral-sh/ruff-pre-commit"

	PreCommitConfig = ".pre-commit-config.yaml"
	MakefileRel     = "Makefile"
)

// InstallHookTypes is the required default_install_hook_types set.
var InstallHookTypes = []string{"pre-commit", "commit-msg", "pre-push"}
