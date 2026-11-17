package syncheck

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	posixpath "path"
	"path/filepath"
	"sort"
	"strings"
)

// A profile is the policy for one kind of repo. Detection (first match wins):
//  1. a regular file .workflow-sync-profile at the root: docs when it holds
//     `docs`, else a `profile` violation. The marker is explicit: a repo with no
//     code cannot be told apart from a misconfigured one by what it lacks.
//     A marker next to a root go.mod or a tracked uv.lock/pyproject.toml is also
//     a violation: letting either side win would silently drop gates.
//  2. a regular file go.mod at the root: Go
//  3. any tracked uv.lock or pyproject.toml: Python
//  4. a regular file pubspec.yaml at the root: Flutter (a nested one does not count)
//  5. none: a `profile` violation and nothing else is checked
//
// A root pubspec.yaml next to the docs marker or a root go.mod is a violation
// too: there is no winner to pick between two app or service signals.
//
// Python files are tooling next to a stronger signal: a repo with a root go.mod
// is a Go repo, and a repo with a root pubspec.yaml is a Flutter repo, whatever
// uv.lock or pyproject.toml it carries (nested ones included).
type profile struct {
	name string

	requiredJobs []string
	optionalJobs []string
	reusableJobs map[string]string // job -> governance workflow
	noPermJobs   []string          // jobs that must not set permissions:

	vulnHook   string // pre-push vuln hook id, present iff the Makefile has `vuln:`
	vulnRecipe string // the `vuln:` recipe
	vulnMkTmpl string // template file for the `vuln:` target
	vulnPCTmpl string // template file for the pre-commit entry

	testEntries []string // a pre-push hook entry containing one of these duplicates check-coverage
	sourceKind  string   // for messages: "a push with no <sourceKind> files"

	ciMissing       string   // message tail when ci.yml is missing
	precommitTpl    string   // template named when .pre-commit-config.yaml is missing
	hygieneTpl      string   // template carrying the hygiene hooks (precommit-hygiene-hooks)
	generatedProbes []string // see FlutterGeneratedProbes, PythonGeneratedProbes (nil: none)
	sourceProbes    []string // hand-written source paths hygiene hooks must not hide (*SourceProbe)
	dependabotTp    string   // template named when dependabot.yml is missing

	vulnFix         vulnFixPolicy // how a .github/workflows/vuln-fix.yml is treated
	vulnFixWorkflow string        // reusable workflow vuln-fix.yml must call, and its concurrency group (vulnFixChecked only)
}

// vulnFixPolicy says what the checker does with vuln-fix.yml in a profile.
type vulnFixPolicy int

const (
	// vulnFixIgnored: the file is not looked at (docs: no code, nothing to fix).
	vulnFixIgnored vulnFixPolicy = iota
	// vulnFixChecked: optional; when present it must call vulnFixWorkflow.
	vulnFixChecked
	// vulnFixForbidden: no governance vuln-fix workflow exists for the profile,
	// so any vuln-fix.yml is hand-rolled (a write token nothing reviews): a finding.
	vulnFixForbidden
)

const ruleProfile = "profile"

var _ = register(ruleProfile)

var goProfile = &profile{
	name:         "go",
	requiredJobs: CIRequiredJobs,
	optionalJobs: CIOptionalJobs,
	reusableJobs: CIReusableJobs,
	noPermJobs:   CINoPermissionJobs,
	vulnHook:     HookGoVuln,
	vulnRecipe:   VulnRecipe,
	vulnMkTmpl:   "templates/Makefile.vuln.mk",
	vulnPCTmpl:   "templates/pre-commit-go-vuln.yaml",
	testEntries:  []string{"go test", "make test"},
	sourceKind:   ".go",
	ciMissing:    "with jobs lint, vuln, test, coverage, gitleaks (plus docker when a Dockerfile exists)",
	precommitTpl: "templates/pre-commit-go-vuln.yaml",
	hygieneTpl:   HygieneTemplate,
	sourceProbes: []string{GoSourceProbe},
	dependabotTp: "add it with gomod and github-actions ecosystems (see templates/dependabot.yml)",

	vulnFix:         vulnFixChecked,
	vulnFixWorkflow: "go-vuln-fix",
}

var pythonProfile = &profile{
	name:            "python",
	requiredJobs:    PyCIRequiredJobs,
	optionalJobs:    PyCIOptionalJobs,
	reusableJobs:    PyCIReusableJobs,
	noPermJobs:      PyCINoPermissionJobs,
	vulnHook:        HookPyVuln,
	vulnRecipe:      PyVulnRecipe,
	vulnMkTmpl:      "templates/python/Makefile",
	vulnPCTmpl:      "templates/python/pre-commit-config.yaml",
	testEntries:     []string{"pytest", "make test"},
	sourceKind:      ".py",
	ciMissing:       "with jobs check (or the legacy lint, typecheck, vuln, test) and gitleaks (plus docker when a uv project has a Dockerfile); see templates/python/ci.yml",
	precommitTpl:    "templates/python/pre-commit-config.yaml",
	hygieneTpl:      "templates/python/pre-commit-config.yaml",
	generatedProbes: PythonGeneratedProbes,
	sourceProbes:    []string{PythonSourceProbe},
	dependabotTp:    "add it with uv (every uv project), docker (every uv project with a Dockerfile) and github-actions entries (see templates/python/dependabot.yml)",

	vulnFix:         vulnFixChecked,
	vulnFixWorkflow: "python-vuln-fix",
}

var docsProfile = &profile{
	name:         "docs",
	requiredJobs: DocsCIRequiredJobs,
	optionalJobs: CIOptionalJobs,
	reusableJobs: DocsCIReusableJobs,
	noPermJobs:   DocsCINoPermJobs,
	ciMissing:    "with jobs lint, gitleaks; see templates/docs/ci.yml",
	precommitTpl: "templates/docs/pre-commit-config.yaml",
	hygieneTpl:   "templates/docs/pre-commit-config.yaml",
	dependabotTp: "add it with a github-actions entry at \"/\" (see templates/docs/dependabot.yml)",
}

// flutterProfile has no coverage or no-test-hook rules yet: those wait for the
// coverage workflow and its check-coverage hook. Its local flutter-test hook is
// required meanwhile (precommit-dart-hooks).
var flutterProfile = &profile{
	name:            "flutter",
	requiredJobs:    FlutterCIRequiredJobs,
	optionalJobs:    FlutterCIOptionalJobs,
	reusableJobs:    FlutterCIReusableJobs,
	noPermJobs:      FlutterCINoPermJobs,
	vulnHook:        HookFlutterVuln,
	vulnRecipe:      FlutterVulnRecipe,
	vulnMkTmpl:      "templates/flutter/Makefile",
	vulnPCTmpl:      "templates/flutter/pre-commit-config.yaml",
	ciMissing:       "with jobs lint, vuln, test, gitleaks; see templates/flutter/ci.yml",
	precommitTpl:    "templates/flutter/pre-commit-config.yaml",
	hygieneTpl:      "templates/flutter/pre-commit-config.yaml",
	generatedProbes: FlutterGeneratedProbes,
	sourceProbes:    []string{FlutterSourceProbe},
	dependabotTp:    "add it with pub and github-actions entries at \"/\" (see templates/flutter/dependabot.yml)",
	vulnFix:         vulnFixForbidden,
}

// detectProfile returns the repo's profile, or nil with c.profileMsg set to the
// reason (empty: nothing matched).
func (c *checker) detectProfile() *profile {
	c.profileMsg = ""
	var code []string
	if c.isRegularFile("go.mod") {
		code = append(code, "go.mod")
	}
	hasPubspec := c.isRegularFile(FlutterSignalFile)
	if hasPubspec {
		code = append(code, FlutterSignalFile)
	}
	for _, f := range c.trackedFiles() {
		if b := posixpath.Base(f); b == "uv.lock" || b == "pyproject.toml" {
			code = append(code, f)
		}
	}
	if c.isRegularFile(ProfileMarkerFile) {
		data, _ := os.ReadFile(filepath.Join(c.root, ProfileMarkerFile))
		switch got := strings.TrimSpace(string(data)); {
		case got != DocsProfileName:
			c.profileMsg = fmt.Sprintf("%s holds %q; want %q (the only marker value); nothing else was checked", ProfileMarkerFile, got, DocsProfileName)
			return nil
		case len(code) > 0:
			c.profileMsg = fmt.Sprintf("%s selects the docs profile but %s exists; remove one (a docs repo has no code, and a Go or Python repo must not lose its gates); nothing else was checked", ProfileMarkerFile, strings.Join(code, ", "))
			return nil
		}
		return docsProfile
	}
	hasGoMod := c.isRegularFile("go.mod")
	switch {
	case hasPubspec && hasGoMod:
		c.profileMsg = fmt.Sprintf("%s selects the Flutter profile but go.mod exists at the root; remove one (a repo has one profile, and picking a winner would silently drop gates); nothing else was checked", FlutterSignalFile)
		return nil
	case hasPubspec:
		return flutterProfile // Python files beside it are tooling
	}
	if hasGoMod {
		return goProfile
	}
	if len(code) > 0 {
		return pythonProfile
	}
	return nil
}

// walkSkipDirs are never descended into when root is not a git work tree
// (same set as scripts/python_pins.py).
var walkSkipDirs = map[string]bool{".git": true, ".venv": true, "venv": true, "node_modules": true, "__pycache__": true, ".ruff_cache": true}

// trackedFiles lists the repo's files, forward-slashed and sorted: `git
// ls-files` (minus deleted files) when root is the git toplevel, else a walk of
// the directory. Computed once per run.
func (c *checker) trackedFiles() []string {
	if c.tracked != nil {
		return c.tracked
	}
	c.tracked = []string{}
	if c.isGitToplevel() {
		if out, err := exec.Command("git", "-C", c.root, "ls-files", "-z").Output(); err == nil {
			for _, f := range strings.Split(string(out), "\x00") {
				if f != "" && c.isRegularFile(f) {
					c.tracked = append(c.tracked, f)
				}
			}
			sort.Strings(c.tracked)
			return c.tracked
		}
	}
	_ = filepath.WalkDir(c.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != c.root && walkSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if rel, err := filepath.Rel(c.root, p); err == nil {
				c.tracked = append(c.tracked, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(c.tracked)
	return c.tracked
}

func (c *checker) isGitToplevel() bool {
	out, err := exec.Command("git", "-C", c.root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return false
	}
	top, err1 := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	root, err2 := filepath.EvalSymlinks(c.root)
	return err1 == nil && err2 == nil && top == root
}

func (c *checker) isTracked(rel string) bool {
	files := c.trackedFiles()
	i := sort.SearchStrings(files, rel)
	return i < len(files) && files[i] == rel
}

// uvProjects lists the directories ("." for the root) holding a tracked uv.lock.
// That is what python-lint's projects check calls a project.
func (c *checker) uvProjects() []string {
	var out []string
	for _, f := range c.trackedFiles() {
		if posixpath.Base(f) == "uv.lock" {
			out = append(out, posixpath.Dir(f))
		}
	}
	return out
}

// dockerProjects lists the uv projects with a Dockerfile (python-docker's default).
func (c *checker) dockerProjects() []string {
	var out []string
	for _, d := range c.uvProjects() {
		if c.isTracked(joinRel(d, "Dockerfile")) {
			out = append(out, d)
		}
	}
	return out
}

func joinRel(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}
