package syncheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	ruleNoGithooks      = "no-githooks"
	ruleGolangciVersion = "golangci-version"
	ruleTestCoverage    = "testcoverage"

	golangciVersionFile = ".golangci-version"
	testCoverageFile    = ".testcoverage.yml"
)

var _ = register(ruleNoGithooks, ruleGolangciVersion, ruleTestCoverage)

func (c *checker) checkRepoFiles() {
	c.checkGithooks()
	c.checkGolangciVersion()
	c.checkTestCoverage()
}

// trackedGithooks lists tracked .githooks files when root is the git toplevel,
// else reports whether a .githooks directory exists on disk.
func (c *checker) trackedGithooks() []string {
	if out, err := exec.Command("git", "-C", c.root, "rev-parse", "--show-toplevel").Output(); err == nil {
		top, err1 := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
		root, err2 := filepath.EvalSymlinks(c.root)
		if err1 == nil && err2 == nil && top == root {
			ls, err := exec.Command("git", "-C", c.root, "ls-files", "--", ".githooks").Output()
			if err == nil {
				return strings.Fields(string(ls))
			}
		}
	}
	if st, err := os.Stat(filepath.Join(c.root, ".githooks")); err == nil && st.IsDir() {
		return []string{".githooks"}
	}
	return nil
}

func (c *checker) checkGithooks() {
	if files := c.trackedGithooks(); len(files) > 0 {
		c.add(".githooks", 0, ruleNoGithooks,
			"%s present; remove .githooks/: `core.hooksPath` and pre-commit are mutually exclusive, and pre-commit owns the hooks", strings.Join(files, ", "))
	}
}

func (c *checker) checkGolangciVersion() {
	data, ok := c.read(golangciVersionFile)
	if !ok {
		c.add(golangciVersionFile, 0, ruleGolangciVersion, "file missing; create it containing %s", GolangciLintVersion)
		return
	}
	got := strings.TrimPrefix(strings.TrimSpace(string(data)), "v")
	if got != GolangciLintVersion {
		c.add(golangciVersionFile, 1, ruleGolangciVersion, "version %q; want %s (go-lint.yml default)", got, GolangciLintVersion)
	}
}

func (c *checker) checkTestCoverage() {
	data, ok := c.read(testCoverageFile)
	if !ok {
		c.add(testCoverageFile, 0, ruleTestCoverage, "file missing; go-coverage.yml and `make check-coverage` both read it")
		return
	}
	var header []string
	for _, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "#") {
			break
		}
		header = append(header, t)
	}
	h := strings.Join(header, "\n")
	for _, want := range []string{"check-coverage", "go-coverage.yml"} {
		if !strings.Contains(h, want) {
			c.add(testCoverageFile, 1, ruleTestCoverage, "leading comment block does not mention %q; document that the file feeds both the pre-push hook and CI", want)
		}
	}
}
