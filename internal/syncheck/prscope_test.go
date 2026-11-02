package syncheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	prScopeScript = "scripts/pr-scope.sh"
	prScopeMarker = "pr-scope body"
	prScopeGoTest = ".github/workflows/go-test.yml"
)

// prScopeUsers are the files that inline the script; each job that does so has a
// step with `id: scope` right after its checkout.
var prScopeUsers = []string{
	".github/workflows/go-coverage.yml",
	".github/workflows/go-docker.yml",
	".github/workflows/go-lint.yml",
	".github/workflows/python-docker.yml",
	".github/workflows/go-test.yml",
}

// gitIn runs git in dir with a fixed identity; failures fail the test.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// prMergeRepo builds what actions/checkout fetches for a pull_request event: a merge
// commit of the PR head into the base, with `changed` written on the PR branch.
// base's contents are the files present before the PR.
func prMergeRepo(t *testing.T, base []string, changed map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	for _, f := range base {
		writeFile(t, dir, f, "base\n")
	}
	writeFile(t, dir, "keep.txt", "base\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	gitIn(t, dir, "checkout", "-q", "-b", "pr")
	for f, c := range changed {
		if c == "<delete>" {
			if err := os.Remove(filepath.Join(dir, f)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeFile(t, dir, f, c)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "pr")
	gitIn(t, dir, "checkout", "-q", "main")
	// Base moves on after the PR branched, as it does in real life.
	writeFile(t, dir, "other.txt", "moved\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "main moved")
	gitIn(t, dir, "merge", "-q", "--no-ff", "-m", "merge", "pr")
	return dir
}

// runPRScope executes the script in dir and returns the skip output and stdout.
func runPRScope(t *testing.T, dir, event string) (skip, stdout string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(prScopeScript)))
	if err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.Command("bash", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "EVENT_NAME="+event, "GITHUB_OUTPUT="+outFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pr-scope exited non-zero (it must fail open): %v\n%s", err, out)
	}
	b, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b)), string(out)
}

// Real git, real script, no mocks: one repo per row.
func TestPRScope(t *testing.T) {
	cases := []struct {
		name    string
		base    []string
		changed map[string]string
		want    string
	}{
		{"release PR", []string{"CHANGELOG.md", ".release-please-manifest.json"}, map[string]string{"CHANGELOG.md": "x\n", ".release-please-manifest.json": "{}\n"}, "true"},
		{"root markdown", []string{"README.md"}, map[string]string{"README.md": "x\n", "ARCHITECTURE.md": "new\n"}, "true"},
		{"docs markdown nested", nil, map[string]string{"docs/sub/guide.md": "x\n"}, "true"},
		{"docs and release together", nil, map[string]string{"docs/a.md": "x\n", "CHANGELOG.md": "x\n"}, "true"},
		{"deleted markdown", []string{"OLD.md"}, map[string]string{"OLD.md": "<delete>"}, "true"},
		{"go file", nil, map[string]string{"main.go": "package main\n"}, "false"},
		{"go file beside markdown", nil, map[string]string{"README.md": "x\n", "main.go": "package main\n"}, "false"},
		{"go.mod", []string{"go.mod"}, map[string]string{"go.mod": "x\n"}, "false"},
		{"workflow file", nil, map[string]string{".github/workflows/ci.yml": "x\n"}, "false"},
		{"nested markdown outside docs", nil, map[string]string{"internal/readme.md": "x\n"}, "false"},
		{"markdown fixture in testdata", nil, map[string]string{"docs/testdata/golden.md": "x\n"}, "false"},
		{"testdata at root", nil, map[string]string{"testdata/golden.md": "x\n"}, "false"},
		{"manifest in a subdirectory", nil, map[string]string{"pkg/.release-please-manifest.json": "{}\n"}, "false"},
		{"non-markdown under docs", nil, map[string]string{"docs/openapi.yaml": "x\n"}, "false"},
		{"markdown renamed to code", []string{"a.md"}, map[string]string{"a.md": "<delete>", "a.go": "package a\n"}, "false"},
		{"code renamed to markdown", []string{"a.go"}, map[string]string{"a.go": "<delete>", "a.md": "x\n"}, "false"},
		{"file name with a space", nil, map[string]string{"my notes.txt": "x\n"}, "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := prMergeRepo(t, c.base, c.changed)
			got, _ := runPRScope(t, dir, "pull_request")
			if want := "skip=" + c.want; got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
		})
	}
}

// A hostile file name must not inject workflow commands into the log.
func TestPRScopeSanitisesFileNames(t *testing.T) {
	dir := prMergeRepo(t, nil, map[string]string{"x\n::error::boom.go": "package x\n"})
	got, out := runPRScope(t, dir, "pull_request")
	if got != "skip=false" {
		t.Fatalf("output = %q, want skip=false", got)
	}
	if strings.Contains(out, "\n::error::") || strings.Count(out, "::") != 2 {
		t.Fatalf("log carries an injected workflow command:\n%q", out)
	}
}

// Anything unexpected must run the gate, never skip it.
func TestPRScopeFailsOpen(t *testing.T) {
	docsOnly := map[string]string{"README.md": "x\n"}

	t.Run("push event", func(t *testing.T) {
		got, _ := runPRScope(t, prMergeRepo(t, nil, docsOnly), "push")
		if got != "skip=false" {
			t.Fatalf("output = %q, want skip=false", got)
		}
	})
	t.Run("event unset", func(t *testing.T) {
		got, _ := runPRScope(t, prMergeRepo(t, nil, docsOnly), "")
		if got != "skip=false" {
			t.Fatalf("output = %q, want skip=false", got)
		}
	})
	t.Run("depth 1 checkout", func(t *testing.T) {
		src := prMergeRepo(t, nil, docsOnly)
		dst := filepath.Join(t.TempDir(), "clone")
		gitIn(t, filepath.Dir(dst), "clone", "-q", "--depth", "1", "file://"+src, dst)
		got, out := runPRScope(t, dst, "pull_request")
		if got != "skip=false" || !strings.Contains(out, "fetch-depth: 2") {
			t.Fatalf("output = %q (log %q), want skip=false naming fetch-depth: 2", got, out)
		}
	})
	t.Run("depth 2 checkout", func(t *testing.T) {
		src := prMergeRepo(t, nil, docsOnly)
		dst := filepath.Join(t.TempDir(), "clone")
		gitIn(t, filepath.Dir(dst), "clone", "-q", "--depth", "2", "file://"+src, dst)
		if got, _ := runPRScope(t, dst, "pull_request"); got != "skip=true" {
			t.Fatalf("output = %q, want skip=true: actions/checkout fetch-depth 2 must be enough", got)
		}
	})
	t.Run("not a merge commit", func(t *testing.T) {
		dir := t.TempDir()
		gitIn(t, dir, "init", "-q", "-b", "main")
		writeFile(t, dir, "README.md", "x\n")
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "one")
		writeFile(t, dir, "README.md", "y\n")
		gitIn(t, dir, "commit", "-q", "-a", "-m", "two")
		if got, _ := runPRScope(t, dir, "pull_request"); got != "skip=false" {
			t.Fatalf("output = %q, want skip=false", got)
		}
	})
	t.Run("not a git repository", func(t *testing.T) {
		if got, _ := runPRScope(t, t.TempDir(), "pull_request"); got != "skip=false" {
			t.Fatalf("output = %q, want skip=false", got)
		}
	})
	t.Run("empty change set", func(t *testing.T) {
		dir := t.TempDir()
		gitIn(t, dir, "init", "-q", "-b", "main")
		writeFile(t, dir, "a.txt", "x\n")
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "base")
		gitIn(t, dir, "checkout", "-q", "-b", "pr")
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "empty")
		gitIn(t, dir, "checkout", "-q", "main")
		writeFile(t, dir, "b.txt", "x\n")
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "moved")
		gitIn(t, dir, "merge", "-q", "--no-ff", "-m", "merge", "pr")
		// The merge adds nothing relative to the base parent.
		if got, _ := runPRScope(t, dir, "pull_request"); got != "skip=false" {
			t.Fatalf("output = %q, want skip=false", got)
		}
	})
}

// The inlined copies cannot read scripts/pr-scope.sh at the caller's pinned ref.
// This keeps each one byte-identical to the tested script.
func TestPRScopeInlinedBodyMatchesScript(t *testing.T) {
	want := markedBlock(t, string(govRead(t, prScopeScript)), prScopeMarker, prScopeScript)
	for _, rel := range prScopeUsers {
		if got := markedBlock(t, string(govRead(t, rel)), prScopeMarker, rel); got != want {
			t.Errorf("%s inlines a different `%s` block than %s; regenerate it from the script", rel, prScopeMarker, prScopeScript)
		}
	}
}

// Each skipping job needs a depth-2 checkout (else the script fails open and never
// skips), the scope step directly after it, and at least one step gated on it.
func TestPRScopeWiring(t *testing.T) {
	for _, rel := range prScopeUsers {
		raw := string(govRead(t, rel))
		if !strings.Contains(raw, "steps.scope.outputs.skip != 'true'") {
			t.Errorf("%s inlines pr-scope but gates no step on steps.scope.outputs.skip", rel)
		}
		jobs := path(govYAML(t, rel), "jobs")
		found := false
		for _, name := range keys(jobs) {
			steps := val(val(jobs, name), "steps")
			if !isSeq(steps) {
				continue
			}
			for i, s := range steps.Content {
				if scalar(val(deref(s), "id")) != "scope" {
					continue
				}
				found = true
				if scalar(val(deref(s), "shell")) != "bash" {
					t.Errorf("%s job %q: pr-scope step needs `shell: bash`", rel, name)
				}
				if got := scalar(path(deref(s), "env", "EVENT_NAME")); got != "${{ github.event_name }}" {
					t.Errorf("%s job %q: pr-scope step env EVENT_NAME = %q, want ${{ github.event_name }}", rel, name, got)
				}
				if i == 0 {
					t.Errorf("%s job %q: pr-scope must directly follow actions/checkout", rel, name)
					continue
				}
				prev := deref(steps.Content[i-1])
				if !strings.HasPrefix(scalar(val(prev, "uses")), "actions/checkout@") || scalar(path(prev, "with", "fetch-depth")) != "2" {
					t.Errorf("%s job %q: pr-scope must directly follow an actions/checkout with fetch-depth: 2", rel, name)
				}
			}
		}
		if !found {
			t.Errorf("%s has no step with id: scope", rel)
		}
	}
}

// Steps a docs-only PR must still run: they are the required checks' real content.
func TestPRScopeKeepsStaticGates(t *testing.T) {
	for _, c := range []struct{ file, step string }{
		{"go-lint.yml", "Check fleet governance (workflow-sync)"},
		{"go-docker.yml", "Check Dockerfile base images against go.mod"},
		{"go-coverage.yml", "Require .testcoverage.yml"},
		{"python-docker.yml", "Require Dockerfile"},
	} {
		rel := ".github/workflows/" + c.file
		jobs := path(govYAML(t, rel), "jobs")
		found := false
		for _, name := range keys(jobs) {
			for _, s := range val(val(jobs, name), "steps").Content {
				if scalar(val(deref(s), "name")) != c.step {
					continue
				}
				found = true
				if strings.Contains(scalar(val(deref(s), "if")), "steps.scope.outputs.skip") {
					t.Errorf("%s step %q must run on docs-only PRs", rel, c.step)
				}
			}
		}
		if !found {
			t.Errorf("%s has no step %q", rel, c.step)
		}
	}
}

// go-test.yml keeps the artifact contract go-coverage.yml reads, so a
// skipped run is the only way the artifact goes missing, and coverage skips with it.
func TestPRScopeGoTestKeepsArtifactContract(t *testing.T) {
	for _, w := range [][2]string{{"name", "coverage-profile"}, {"path", "coverage.raw.out"}, {"retention-days", "1"}, {"if-no-files-found", "error"}} {
		found := false
		for _, s := range val(path(govYAML(t, prScopeGoTest), "jobs", "test"), "steps").Content {
			if strings.HasPrefix(scalar(val(deref(s), "uses")), "actions/upload-artifact@") {
				found = scalar(path(deref(s), "with", w[0])) == w[1]
			}
		}
		if !found {
			t.Errorf("%s: upload-artifact with.%s != %q", prScopeGoTest, w[0], w[1])
		}
	}
}
