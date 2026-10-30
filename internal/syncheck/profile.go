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
//  4. none: a `profile` violation and nothing else is checked
//
// A repo with both a root go.mod and Python files is a Go repo: the Go gates
// are the stricter, established set, and a Go service may carry Python tooling.
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

	ciMissing    string // message tail when ci.yml is missing
	precommitTpl string // template named when .pre-commit-config.yaml is missing
	dependabotTp string // template named when dependabot.yml is missing

	vulnFixWorkflow string // reusable workflow vuln-fix.yml must call, and its concurrency group
}

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
	dependabotTp: "add it with gomod and github-actions ecosystems (see templates/dependabot.yml)",

	vulnFixWorkflow: "go-vuln-fix",
}

var pythonProfile = &profile{
	name:         "python",
	requiredJobs: PyCIRequiredJobs,
	optionalJobs: CIOptionalJobs,
	reusableJobs: PyCIReusableJobs,
	noPermJobs:   PyCINoPermissionJobs,
	vulnHook:     HookPyVuln,
	vulnRecipe:   PyVulnRecipe,
	vulnMkTmpl:   "templates/python/Makefile",
	vulnPCTmpl:   "templates/python/pre-commit-config.yaml",
	testEntries:  []string{"pytest", "make test"},
	sourceKind:   ".py",
	ciMissing:    "with jobs lint, typecheck, vuln, test, gitleaks (plus docker when a uv project has a Dockerfile); see templates/python/ci.yml",
	precommitTpl: "templates/python/pre-commit-config.yaml",
	dependabotTp: "add it with uv (every uv project), docker (every uv project with a Dockerfile) and github-actions entries (see templates/python/dependabot.yml)",

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
	dependabotTp: "add it with a github-actions entry at \"/\" (see templates/docs/dependabot.yml)",
}

// detectProfile returns the repo's profile, or nil with c.profileMsg set to the
// reason (empty: nothing matched).
func (c *checker) detectProfile() *profile {
	c.profileMsg = ""
	var code []string
	if c.isRegularFile("go.mod") {
		code = append(code, "go.mod")
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
	if c.isRegularFile("go.mod") {
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
