package syncheck

import (
	"encoding/json"
	"fmt"
	posixpath "path"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Rules of the Python profile only. ci.yml, pre-commit and Makefile rules are
// shared with Go (see profile.go for the per-profile values they read).
const (
	ruleCIProjects    = "ci-projects"
	ruleUVVersion     = "uv-version"
	ruleRuffVersion   = "ruff-version"
	rulePythonVersion = "python-version"
	rulePoetryLock    = "poetry-lock"
	ruleOSVIgnore     = "osv-ignore-expiry"
	ruleCoverageFloor = "coverage-floor"
	ruleTOMLParse     = "toml-parse"

	uvVersionFile   = ".uv-version"
	ruffVersionFile = ".ruff-version"
	osvConfigFile   = "osv-scanner.toml"
	uvImage         = "ghcr.io/astral-sh/uv"
)

var _ = register(ruleCIProjects, ruleUVVersion, ruleRuffVersion, rulePythonVersion, rulePoetryLock,
	ruleOSVIgnore, ruleCoverageFloor, ruleTOMLParse)

// now is the clock osv-ignore-expiry measures against; tests pin it.
var now = time.Now

// --- ci.yml ------------------------------------------------------------------

// parseProjects reads a reusable workflow's `projects` input: a YAML string
// holding a JSON array of directory strings. Entries come back path.Clean-ed.
func parseProjects(n *yaml.Node) ([]string, error) {
	if n == nil {
		return nil, fmt.Errorf("absent")
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return nil, fmt.Errorf("not a string; the input is typed string, write it as a quoted JSON array")
	}
	var raw []string
	if err := json.Unmarshal([]byte(n.Value), &raw); err != nil {
		return nil, fmt.Errorf("%q is not a JSON array of strings", n.Value)
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		out = append(out, posixpath.Clean(strings.TrimSpace(e)))
	}
	return out, nil
}

func jsonList(items []string) string {
	b, _ := json.Marshal(append([]string{}, items...))
	return string(b)
}

// ciProjects checks that every Python job gates the right projects: lint,
// typecheck, vuln and test list every uv project; docker lists every uv
// project with a Dockerfile. A project left out has no gate at all.
func (c *checker) ciProjects(byName map[string]ciJob) {
	all, images := c.uvProjects(), c.dockerProjects()
	for _, name := range append(append([]string{}, PyCIAllProjectJobs...), CIDockerJob) {
		j, ok := byName[name]
		if !ok {
			continue
		}
		want, what := all, "every uv project (a directory with a tracked uv.lock)"
		if name == CIDockerJob {
			if len(images) == 0 {
				continue // ci-jobs already says to remove the job
			}
			want, what = images, "every uv project with a Dockerfile"
		}
		n := path(j.node, "with", "projects")
		got, err := parseProjects(n)
		if err != nil {
			c.add(ciFile, line(n, j.line()), ruleCIProjects, "job %q with.projects is %s; want '%s' (%s)", name, err, jsonList(want), what)
			continue
		}
		gotSet, wantSet := setOf(got...), setOf(want...)
		var missing, unknown []string
		for _, d := range want {
			if !gotSet[d] {
				missing = append(missing, d)
			}
		}
		for _, d := range sortedKeys(gotSet) {
			if !wantSet[d] {
				unknown = append(unknown, d)
			}
		}
		if len(missing)+len(unknown) > 0 {
			c.add(ciFile, n.Line, ruleCIProjects, "job %q with.projects = %s; want '%s', %s (missing: [%s], unknown: [%s]); a project left out is never gated",
				name, n.Value, jsonList(want), what, strings.Join(missing, ", "), strings.Join(unknown, ", "))
		}
	}
}

// testedProjects is the test job's project list when it parses (unknown entries
// are ci-projects' finding), else every uv project.
func (c *checker) testedProjects() []string {
	all := c.uvProjects()
	p := c.loadYAML(ciFile)
	got, err := parseProjects(path(p.root, "jobs", "test", "with", "projects"))
	if err != nil {
		return all
	}
	known := setOf(all...)
	var out []string
	for _, d := range sortedKeys(setOf(got...)) {
		if known[d] {
			out = append(out, d)
		}
	}
	return out
}

// --- dependabot.yml ------------------------------------------------------------

// dependabotDirs normalises an updates entry's directory/directories to
// "/a/b" patterns (Dependabot accepts globs in directories).
func dependabotDirs(u *yaml.Node) []string {
	var out []string
	add := func(s string) {
		s = "/" + strings.Trim(strings.TrimSpace(s), "/")
		out = append(out, s)
	}
	if d := scalar(val(u, "directory")); d != "" {
		add(d)
	}
	if ds := val(u, "directories"); isSeq(ds) {
		for _, d := range ds.Content {
			add(scalar(deref(d)))
		}
	}
	return out
}

// dirCovered reports whether a Dependabot directory pattern covers dir ("/a/b").
func dirCovered(pattern, dir string) bool {
	if pattern == dir {
		return true
	}
	if i := strings.Index(pattern, "**"); i >= 0 {
		return strings.HasPrefix(dir, pattern[:i])
	}
	ok, err := posixpath.Match(pattern, dir)
	return err == nil && ok
}

func dependabotDir(project string) string {
	if project == "." {
		return "/"
	}
	return "/" + project
}

func (c *checker) checkPythonDependabot(root *yaml.Node) {
	uk, updates := get(root, "updates")
	patterns := map[string][]string{}
	if isSeq(updates) {
		for _, u := range updates.Content {
			u = deref(u)
			eco := scalar(val(u, "package-ecosystem"))
			patterns[eco] = append(patterns[eco], dependabotDirs(u)...)
			if eco == "docker" && !ignoresUVImage(u) {
				c.add(dependabotYML, line(val(u, "package-ecosystem"), line(uk, 1)), ruleDependabot,
					"docker entry does not ignore %s; add `ignore: [{dependency-name: %s}]`: python-lint requires every uv pin to move in one PR, so a Dependabot bump of the Dockerfile tag alone is red", uvImage, uvImage)
			}
		}
	}
	covered := func(eco, project string) bool {
		for _, p := range patterns[eco] {
			if dirCovered(p, dependabotDir(project)) {
				return true
			}
		}
		return false
	}
	if !covered("github-actions", ".") {
		c.add(dependabotYML, line(uk, 1), ruleDependabot, "no updates entry for package-ecosystem %q at directory \"/\"; add one", "github-actions")
	}
	for _, need := range []struct {
		eco, why string
		projects []string
	}{
		{"uv", "every uv project gets version and security updates", c.uvProjects()},
		{"docker", "base-image patches for every uv project with a Dockerfile", c.dockerProjects()},
	} {
		var missing []string
		for _, d := range need.projects {
			if !covered(need.eco, d) {
				missing = append(missing, dependabotDir(d))
			}
		}
		if len(missing) > 0 {
			c.add(dependabotYML, line(uk, 1), ruleDependabot, "no %q updates entry covers %s; add them to its directories (%s)", need.eco, strings.Join(missing, ", "), need.why)
		}
	}
}

// ignoresUVImage reports an unconditional ignore of the uv image.
func ignoresUVImage(u *yaml.Node) bool {
	ig := val(u, "ignore")
	if !isSeq(ig) {
		return false
	}
	for _, e := range ig.Content {
		e = deref(e)
		if scalar(val(e, "dependency-name")) == uvImage && val(e, "update-types") == nil && val(e, "versions") == nil {
			return true
		}
	}
	return false
}

// --- repo files ------------------------------------------------------------------

var pythonVersionRe = regexp.MustCompile(`^(cpython-)?\d+\.\d+`)

func (c *checker) checkPythonRepoFiles() {
	c.checkPinFile(uvVersionFile, ruleUVVersion, UVVersion, "the fleet uv pin; python-lint ties the uv-pre-commit rev, the [tool.uv] required-version floors and the Dockerfile uv tags to this file")
	c.checkPinFile(ruffVersionFile, ruleRuffVersion, RuffVersion, "the python-lint.yml ruff-version default")
	c.checkRuffHookRev()
	for _, d := range c.uvProjects() {
		rel := joinRel(d, ".python-version")
		data, ok := c.read(rel)
		switch {
		case !ok:
			c.add(rel, 0, rulePythonVersion, "file missing; uv project %s needs it: uv, requires-python and the Dockerfile `FROM python:X.Y` must agree on one Python (python-lint compares them)", d)
		case !pythonVersionRe.MatchString(strings.TrimSpace(string(data))):
			c.add(rel, 1, rulePythonVersion, "content %q does not start with X.Y; write the Python minor, e.g. 3.12", strings.TrimSpace(string(data)))
		}
	}
	for _, f := range c.trackedFiles() {
		if posixpath.Base(f) == "poetry.lock" {
			c.add(f, 0, rulePoetryLock, "tracked poetry.lock; the fleet's Python toolchain is uv (one uv.lock per project), and a stale Poetry lock misleads Dependabot and scanners: delete it")
		}
	}
}

// checkPinFile requires a one-line version file equal to want (leading v ignored).
func (c *checker) checkPinFile(rel, rule, want, source string) {
	data, ok := c.read(rel)
	if !ok {
		c.add(rel, 0, rule, "file missing; create it containing %s (%s)", want, source)
		return
	}
	got := strings.TrimPrefix(strings.TrimSpace(string(data)), "v")
	if got != want {
		c.add(rel, 1, rule, "version %q; want %s (%s)", got, want, source)
	}
}

func (c *checker) checkRuffHookRev() {
	p := c.loadYAML(PreCommitConfig)
	if p.root == nil {
		return // missing or unparsable: other rules report it
	}
	want := "v" + RuffVersion
	found := false
	for _, r := range readPCRepos(p.root) {
		if normRepoURL(r.url) != RuffHookRepo {
			continue
		}
		found = true
		if rev := scalar(r.rev); rev != want {
			c.add(PreCommitConfig, line(r.rev, r.line), ruleRuffVersion, "%s rev %q; want %s (equals .ruff-version and the python-lint ruff-version default)", RuffHookRepo, rev, want)
		}
	}
	if !found {
		c.add(PreCommitConfig, 0, ruleRuffVersion, "no `repo: %s` entry; add it at rev %s with hooks ruff-check and ruff-format", RuffHookRepo, want)
	}
}

// --- TOML-backed rules ------------------------------------------------------------

func (c *checker) loadTOML(rel string) (*tomlDoc, bool) {
	data, ok := c.read(rel)
	if !ok {
		return nil, false
	}
	doc, err := parseTOML(data)
	if err != nil {
		ln := 0
		if te, ok := err.(*tomlError); ok {
			ln = te.line
		}
		c.add(rel, ln, ruleTOMLParse, "invalid TOML: %v; fix the syntax so the other rules can run", err)
		return nil, true
	}
	return doc, true
}

// checkOSVIgnores: every osv-scanner ignore names a reason and expires within
// OSVIgnoreMaxDays. An already expired ignore is not flagged: osv-scanner then
// reports the advisory again and the vuln job is red on its own.
func (c *checker) checkOSVIgnores() {
	y, m, d := now().Date()
	limit := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, OSVIgnoreMaxDays)
	for _, f := range c.trackedFiles() {
		if posixpath.Base(f) != osvConfigFile {
			continue
		}
		doc, ok := c.loadTOML(f)
		if !ok || doc == nil {
			continue
		}
		for _, key := range []string{"IgnoredVulns", "PackageOverrides"} {
			if v, ok := doc.values[key]; ok {
				c.add(f, v.line, ruleOSVIgnore, "%s is an inline array; write each entry as a [[%s]] table so its expiry can be checked", key, key)
			}
		}
		for _, e := range doc.arrays["IgnoredVulns"] {
			c.osvEntry(f, e, "[[IgnoredVulns]] "+orUnnamed(e, "id"), "ignoreUntil", limit, true)
		}
		for _, e := range doc.arrays["PackageOverrides"] {
			if isTOMLTrue(e.values["ignore"]) || isTOMLTrue(e.values["vulnerability.ignore"]) {
				c.osvEntry(f, e, "[[PackageOverrides]] "+orUnnamed(e, "name")+" (ignores vulnerabilities)", "effectiveUntil", limit, false)
			}
		}
	}
}

func isTOMLTrue(v tomlValue) bool { return v.kind == tomlBool && v.str == "true" }

func orUnnamed(e *tomlTable, key string) string {
	if v, ok := e.values[key]; ok && v.kind == tomlString && strings.TrimSpace(v.str) != "" {
		return fmt.Sprintf("%q", v.str)
	}
	return "(no " + key + ")"
}

func (c *checker) osvEntry(f string, e *tomlTable, what, untilKey string, limit time.Time, needID bool) {
	if v, ok := e.values["id"]; needID && (!ok || v.kind != tomlString || strings.TrimSpace(v.str) == "") {
		c.add(f, e.line, ruleOSVIgnore, "%s has no id; name the advisory (e.g. GHSA-xxxx or PYSEC-yyyy)", what)
	}
	if v, ok := e.values["reason"]; !ok || v.kind != tomlString || strings.TrimSpace(v.str) == "" {
		c.add(f, line2(v, ok, e.line), ruleOSVIgnore, "%s has no reason; say why the advisory is unreachable or unfixable, and what clears it", what)
	}
	v, ok := e.values[untilKey]
	if !ok {
		c.add(f, e.line, ruleOSVIgnore, "%s has no %s; an ignore without expiry silences the advisory forever: set a date at most %d days ahead (<= %s)", what, untilKey, OSVIgnoreMaxDays, limit.Format("2006-01-02"))
		return
	}
	day, ok := tomlDay(v)
	switch {
	case !ok:
		c.add(f, v.line, ruleOSVIgnore, "%s %s = %q is not a date; write a TOML date such as %s", what, untilKey, v.str, limit.Format("2006-01-02"))
	case day.After(limit):
		c.add(f, v.line, ruleOSVIgnore, "%s %s = %s is more than %d days ahead; set it to %s or earlier and re-triage then", what, untilKey, day.Format("2006-01-02"), OSVIgnoreMaxDays, limit.Format("2006-01-02"))
	}
}

func line2(v tomlValue, ok bool, fallback int) int {
	if ok {
		return v.line
	}
	return fallback
}

// checkCoverageFloors mirrors scripts/python_coverage_floor.py: every tested
// project's [tool.coverage.report] fail_under is a number in (0, 100].
func (c *checker) checkCoverageFloors() {
	for _, d := range c.testedProjects() {
		rel := joinRel(d, "pyproject.toml")
		doc, ok := c.loadTOML(rel)
		if !ok {
			c.add(rel, 0, ruleCoverageFloor, "file missing; uv project %s is tested, and python-test reads its fail_under", d)
			continue
		}
		if doc == nil {
			continue
		}
		v, ok := doc.values["tool.coverage.report.fail_under"]
		switch {
		case !ok:
			c.add(rel, doc.tables["tool.coverage.report"], ruleCoverageFloor, "[tool.coverage.report] fail_under is missing; a missing floor is an error, never zero: set it to floor(measured coverage)")
		case v.kind != tomlInt && v.kind != tomlFloat:
			c.add(rel, v.line, ruleCoverageFloor, "[tool.coverage.report] fail_under must be a number, found %s %q", v.kind, v.str)
		default:
			if f, _ := tomlNumber(v); !(f > 0 && f <= 100) {
				c.add(rel, v.line, ruleCoverageFloor, "[tool.coverage.report] fail_under = %s must be in (0, 100]; zero is not a gate", v.str)
			}
		}
	}
}
