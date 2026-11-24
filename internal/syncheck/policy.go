package syncheck

import "fmt"

// Fleet policy. Plain Go so that a change is a reviewed diff and a hook rev bump.
const (
	GovernanceRepo = "tclavelloux/promy-github-workflows"

	// coverage-pin-sync.yml syncs this on Dependabot bumps of go-coverage.yml; a human still merges.
	GoTestCoverageVersion = "2.23.0"
	GolangciLintVersion   = "2.15.0"

	// ProdTraceProcessor is the only trace processor allowed in a Go service's prod
	// config (rule prod-telemetry-config). promy-go-kit's fxotlphttp provides it;
	// the sink is promy-steering#16.
	ProdTraceProcessor = "otlp-http"

	// Python tool versions. Ruff and osv-scanner are decided by the reusable
	// workflows; uv by this file alone (python-lint reads the caller's .uv-version).
	// UVVersion must equal the uv Dependabot bundles (dependabot-core
	// uv/helpers/requirements.txt): it is the one lock writer that cannot be pinned,
	// and pin-drift reports any difference.
	// The Python profile requires .ruff-version, the ruff-pre-commit rev and
	// .uv-version to equal these; python-lint ties every other pin to those files.
	RuffVersion       = "0.17.3" // python-lint.yml ruff-version default
	OSVScannerVersion = "2.7.0"  // python-vuln.yml and flutter-vuln.yml osv-scanner-version default, .osv-scanner-version (py-vuln and flutter-vuln hooks)
	UVVersion         = "0.13.5" // fleet uv pin; templates/python/pre-commit-config.yaml uv-pre-commit rev

	// FlutterVersion is the fleet Flutter SDK. The Flutter profile requires
	// .flutter-version to equal it; flutter-lint and flutter-test install the SDK
	// from that file.
	FlutterVersion = "3.50.0"

	// OSVIgnoreMaxDays bounds every osv-scanner ignore: Python has no
	// reachability analysis, so an ignore must expire and be re-triaged.
	OSVIgnoreMaxDays = 90

	// AutomergeIf: no status function, so GitHub ANDs success() implicitly.
	// Allowlisted bots: dependabot[bot] and promy-fleet-bot[bot] (vuln-fix PRs);
	// keep in step with the job `if:` of dependabot-automerge.yml.
	AutomergeIf = "github.event.pull_request.user.login == 'dependabot[bot]' || github.event.pull_request.user.login == 'promy-fleet-bot[bot]'"
	DraftSkipIf = "github.event.pull_request.draft == false"

	ConcurrencyGroup = "${{ github.workflow }}-${{ github.ref }}"
)

// WorkflowMajors maps each reusable workflow to the moving major callers pin.
var WorkflowMajors = map[string]int{
	"go-lint":              1,
	"go-vuln":              1,
	"go-test":              1,
	"go-coverage":          2,
	"go-docker":            1,
	"pr-title":             1,
	"dependabot-automerge": 1,
	"go-vuln-fix":          1,
	"python-ci":            1,
	"python-lint":          1,
	"python-typecheck":     1,
	"python-vuln":          1,
	"python-test":          1,
	"python-docker":        1,
	"python-vuln-fix":      1,
	"docs-lint":            1,
	"flutter-lint":         1,
	"flutter-test":         1,
	"flutter-vuln":         1,
	"release-please":       1,
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
		"test": "go-test", "coverage": "go-coverage", "automerge": "dependabot-automerge",
	}
	// CINoPermissionJobs must not carry a permissions: key (top-level read applies).
	CINoPermissionJobs = []string{"lint", "vuln", "docker", "test"}
)

// Job sets of ci.yml in the Python profile. `docker` is required iff a uv
// project has a Dockerfile; automerge is optional, as in Go.
var (
	// Either the single `check` job (python-ci.yml) or the four legacy jobs
	// PyCILegacyJobs (python-lint, -typecheck, -vuln, -test); never a mix.
	PyCIRequiredJobs = []string{"gitleaks"}
	PyCICheckJob     = "check"
	PyCILegacyJobs   = []string{"lint", "typecheck", "vuln", "test"}
	PyCIOptionalJobs = []string{"lint", "typecheck", "vuln", "test", "check", "automerge"}
	PyCIReusableJobs = map[string]string{
		"check": "python-ci", "lint": "python-lint", "typecheck": "python-typecheck", "vuln": "python-vuln",
		"test": "python-test", "docker": "python-docker", "automerge": "dependabot-automerge",
	}
	PyCINoPermissionJobs = []string{"check", "lint", "typecheck", "vuln", "test", "docker"}
	// PyCIAllProjectJobs must list every uv project in with.projects; docker
	// lists every uv project that has a Dockerfile.
	PyCIAllProjectJobs = []string{"check", "lint", "typecheck", "vuln", "test"}
)

// Job sets of ci.yml in the docs profile: a repo with no code has nothing to
// build, test or scan. automerge is optional, as in the other profiles.
var (
	DocsCIRequiredJobs  = []string{"lint", "gitleaks"}
	DocsCIForbiddenJobs = []string{"test", "coverage", "vuln", "docker", "typecheck"}
	DocsCIReusableJobs  = map[string]string{"lint": "docs-lint", "automerge": "dependabot-automerge"}
	DocsCINoPermJobs    = []string{"lint"}
)

// Job sets of ci.yml in the Flutter profile. automerge is optional, as in the
// other profiles; coverage is an unknown job until its workflow lands.
var (
	FlutterCIRequiredJobs = []string{"lint", "vuln", "test", "gitleaks"}
	FlutterCIOptionalJobs = []string{"automerge"}
	FlutterCIReusableJobs = map[string]string{"lint": "flutter-lint", "vuln": "flutter-vuln", "test": "flutter-test", "automerge": "dependabot-automerge"}
	FlutterCINoPermJobs   = []string{"lint", "vuln", "test"}
)

// Files that select and pin the Flutter profile.
const (
	FlutterSignalFile  = "pubspec.yaml"
	FlutterVersionFile = ".flutter-version"
)

// ProfileMarkerFile selects the docs profile; its content is DocsProfileName.
const (
	ProfileMarkerFile = ".workflow-sync-profile"
	DocsProfileName   = "docs"
)

type perms map[string]string

var (
	PermsRead             = perms{"contents": "read"}
	PermsCoverage         = perms{"contents": "read", "pull-requests": "write"}
	PermsGitleaks         = perms{"contents": "read", "pull-requests": "read"}
	PermsPRTitleJob       = perms{"contents": "read", "pull-requests": "read"}
	PermsVulnFixJob       = perms{"contents": "write", "pull-requests": "write", "issues": "write"}
	PermsReleasePleaseJob = perms{"contents": "write", "pull-requests": "write"}
	PermsAutomergeJob     = perms{"contents": "write", "pull-requests": "write", "checks": "read", "actions": "read", "statuses": "read"}
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
	HookFlutterVuln   = "flutter-vuln"
	HookCheckCoverage = "check-coverage"
	HookConventional  = "conventional-pre-commit"

	MakeSetupLine = "pre-commit install --install-hooks"
	VulnRecipe    = "pre-commit run go-vuln --hook-stage manual"
	PyVulnRecipe  = "pre-commit run py-vuln --hook-stage manual"

	FlutterVulnRecipe = "pre-commit run flutter-vuln --hook-stage manual"

	// Local Dart hooks the Flutter profile requires (precommit-dart-hooks).
	HookDartFormat   = "dart-format"
	HookDartAnalyze  = "dart-analyze"
	HookFlutterTest  = "flutter-test"
	DartFormatEntry  = "dart format --set-exit-if-changed ."
	DartAnalyzeEntry = "dart analyze --fatal-infos"
	// The env prefix keeps a worktree push from reporting SDK 0.0.0-unknown.
	FlutterTestEntry = "env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE flutter test"

	RuffHookRepo = "https://github.com/astral-sh/ruff-pre-commit"

	PreCommitConfig = ".pre-commit-config.yaml"
	MakefileRel     = "Makefile"
)

// InstallHookTypes is the required default_install_hook_types set.
var InstallHookTypes = []string{"pre-commit", "commit-msg", "pre-push"}

// Generic pre-commit-hooks hygiene set (precommit-hygiene-hooks). Every profile
// requires these ids under the pre-commit-hooks repo; check-toml stays optional.
var HygieneHooks = []string{
	"check-added-large-files", "check-json", "check-merge-conflict", "check-yaml",
	"end-of-file-fixer", "trailing-whitespace",
}

// HygieneKeepProbes are the paths no hygiene hook may hide, by exclude or by a
// top-level files/exclude: hiding them disables the gate where it matters most.
// Each profile adds one hand-written source file (the *SourceProbe constants).
var HygieneKeepProbes = []string{".pre-commit-config.yaml", ".github/workflows/ci.yml", "README.md", ".vscode/settings.json"}

const (
	GoSourceProbe      = "main.go"
	PythonSourceProbe  = "src/app/main.py"
	FlutterSourceProbe = "lib/main.dart"
)

// HygieneGeneratedHooks are at least the fixers that rewrite files; a profile
// with generated files must exclude them on these hooks.
var HygieneGeneratedHooks = []string{"end-of-file-fixer", "trailing-whitespace"}

// Generated-file probes: the excludes of HygieneGeneratedHooks must match each.
var (
	FlutterGeneratedProbes = []string{"lib/x.g.dart", "lib/x.freezed.dart"} // build_runner output
	// basedpyright writes its baseline without a final newline.
	PythonGeneratedProbes = []string{"src/app/.basedpyright/baseline.json"}
)

// HygieneTemplate is the snippet the Go profile copies (the others carry the
// hooks in their profile template).
const HygieneTemplate = "templates/pre-commit-hygiene.yaml"
