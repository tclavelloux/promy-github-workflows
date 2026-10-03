package syncheck

import "fmt"

// Fleet policy. Plain Go so that a change is a reviewed diff and a hook rev bump.
const (
	GovernanceRepo = "tclavelloux/promy-github-workflows"

	GoTestCoverageVersion = "2.19.0"
	GolangciLintVersion   = "2.13.2"

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
	HookCheckCoverage = "check-coverage"
	HookConventional  = "conventional-pre-commit"

	MakeSetupLine = "pre-commit install --install-hooks"
	VulnRecipe    = "pre-commit run go-vuln --hook-stage manual"

	PreCommitConfig = ".pre-commit-config.yaml"
	MakefileRel     = "Makefile"
)

// InstallHookTypes is the required default_install_hook_types set.
var InstallHookTypes = []string{"pre-commit", "commit-msg", "pre-push"}
