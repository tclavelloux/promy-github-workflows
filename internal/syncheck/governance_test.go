package syncheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// These tests keep policy.go honest against the governance repo's own files.
// Each failure names the two files that disagree.

const (
	govCoverageYML = ".github/workflows/go-coverage.yml"
	govLintYML     = ".github/workflows/go-lint.yml"
	govPRTitleYML  = ".github/workflows/pr-title.yml"
)

func govRead(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func govYAML(t *testing.T, rel string) *yaml.Node {
	t.Helper()
	n, err := parseYAML(govRead(t, rel))
	if err != nil || n == nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return n
}

func TestPolicyGoTestCoverageVersion(t *testing.T) {
	re := regexp.MustCompile(`vladopajic/go-test-coverage@[0-9a-f]{40}\s+#\s*v(\S+)`)
	m := re.FindSubmatch(govRead(t, govCoverageYML))
	if m == nil {
		t.Fatalf("%s: no `vladopajic/go-test-coverage@<sha> # vX.Y.Z` line", govCoverageYML)
	}
	if got := string(m[1]); got != GoTestCoverageVersion {
		t.Errorf("policy.go GoTestCoverageVersion = %s but %s pins v%s; bump both together", GoTestCoverageVersion, govCoverageYML, got)
	}
}

func TestPolicyGolangciLintVersion(t *testing.T) {
	n := path(govYAML(t, govLintYML), "on", "workflow_call", "inputs", "golangci-lint-version", "default")
	got := strings.TrimPrefix(scalar(n), "v")
	if got != GolangciLintVersion {
		t.Errorf("policy.go GolangciLintVersion = %s but %s on.workflow_call.inputs.golangci-lint-version.default = %q", GolangciLintVersion, govLintYML, scalar(n))
	}
}

func TestPolicyCommitTypes(t *testing.T) {
	n := path(govYAML(t, govPRTitleYML), "on", "workflow_call", "inputs", "types", "default")
	got := setOf(strings.Fields(scalar(n))...)
	if want := setOf(CommitTypes...); !sameSet(got, want) {
		t.Errorf("policy.go CommitTypes = %s but %s `types` default = %s", fmtSet(want), govPRTitleYML, fmtSet(got))
	}
}

func TestPolicyWorkflowMajors(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	callable := map[string]bool{}
	for _, f := range files {
		rel := ".github/workflows/" + filepath.Base(f)
		if get2, _ := get(path(govYAML(t, rel), "on"), "workflow_call"); get2 != nil {
			callable[strings.TrimSuffix(filepath.Base(f), ".yml")] = true
		}
	}
	for _, name := range sortedKeys(callable) {
		if _, ok := WorkflowMajors[name]; !ok {
			t.Errorf(".github/workflows/%s.yml has on.workflow_call but policy.go WorkflowMajors has no %q entry", name, name)
		}
	}
	var stale []string
	for name := range WorkflowMajors {
		if !callable[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("policy.go WorkflowMajors has %q but .github/workflows/%s.yml has no on.workflow_call", name, name)
	}
}

func TestTemplatesAutomergeJob(t *testing.T) {
	const rel = "templates/ci-automerge-job.yml"
	job := path(govYAML(t, rel), "jobs", "automerge")
	if job == nil {
		t.Fatalf("%s: no jobs.automerge", rel)
	}
	if got := normExpr(scalar(val(job, "if"))); got != AutomergeIf {
		t.Errorf("%s automerge if = %q but policy.go AutomergeIf = %q", rel, got, AutomergeIf)
	}
	if p, ok := readPerms(val(job, "permissions")); !ok || !p.equal(PermsAutomergeJob) {
		t.Errorf("%s automerge permissions = %s but policy.go PermsAutomergeJob = %s", rel, describePerms(val(job, "permissions")), PermsAutomergeJob)
	}
}

// templateRoot builds a fixture from pass-rollout with the given template
// copied over the given repo path.
func templateRoot(t *testing.T, tmpl, dst string) Report {
	t.Helper()
	root := fixture(t, "pass-rollout")
	writeFile(t, root, dst, string(govRead(t, tmpl)))
	return run(t, root)
}

func TestTemplatesPassRules(t *testing.T) {
	t.Run("go-vuln-fix-caller.yml", func(t *testing.T) {
		rep := templateRoot(t, "templates/go-vuln-fix-caller.yml", vfRel)
		if !rep.OK() {
			t.Errorf("templates/go-vuln-fix-caller.yml as %s fails policy:\n%s", vfRel, joinMessages(rep.Violations))
		}
	})
	t.Run("dependabot.yml", func(t *testing.T) {
		rep := templateRoot(t, "templates/dependabot.yml", depRel)
		if !rep.OK() {
			t.Errorf("templates/dependabot.yml as %s fails policy:\n%s", depRel, joinMessages(rep.Violations))
		}
	})
	t.Run("Makefile.vuln.mk", func(t *testing.T) {
		mk := parseMakefile(strings.Split(string(govRead(t, "templates/Makefile.vuln.mk")), "\n"))
		tg := mk.targets["vuln"]
		if tg == nil {
			t.Fatal("templates/Makefile.vuln.mk has no vuln: target")
		}
		rec := tg.meaningful()
		if len(rec) != 1 || rec[0].text != VulnRecipe {
			t.Errorf("templates/Makefile.vuln.mk vuln recipe = %v but policy.go VulnRecipe = %q", rec, VulnRecipe)
		}
	})
	t.Run("pre-commit-go-vuln.yaml", func(t *testing.T) {
		const rel = "templates/pre-commit-go-vuln.yaml"
		seq := govYAML(t, rel)
		if !isSeq(seq) {
			t.Fatalf("%s: want a top-level list of repo entries", rel)
		}
		found := false
		for _, e := range seq.Content {
			if normRepoURL(scalar(val(e, "repo"))) != GovernanceHookRepoURL {
				continue
			}
			found = true
			if rev := scalar(val(e, "rev")); !immutableHookRev.MatchString(rev) {
				t.Errorf("%s rev = %q but the precommit-governance-repo rule requires hooks/vX.Y.Z", rel, rev)
			}
		}
		if !found {
			t.Errorf("%s has no entry for policy.go GovernanceHookRepoURL %s", rel, GovernanceHookRepoURL)
		}
	})
}
