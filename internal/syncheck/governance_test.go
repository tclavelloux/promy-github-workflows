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
	govPyLintYML   = ".github/workflows/python-lint.yml"
	govPyVulnYML   = ".github/workflows/python-vuln.yml"
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

func TestPolicyRuffVersion(t *testing.T) {
	n := path(govYAML(t, govPyLintYML), "on", "workflow_call", "inputs", "ruff-version", "default")
	got := strings.TrimPrefix(scalar(n), "v")
	if got != RuffVersion {
		t.Errorf("policy.go RuffVersion = %s but %s on.workflow_call.inputs.ruff-version.default = %q", RuffVersion, govPyLintYML, scalar(n))
	}
}

func TestPolicyOSVScannerVersion(t *testing.T) {
	in := path(govYAML(t, govPyVulnYML), "on", "workflow_call", "inputs")
	got := strings.TrimPrefix(scalar(path(in, "osv-scanner-version", "default")), "v")
	if got != OSVScannerVersion {
		t.Errorf("policy.go OSVScannerVersion = %s but %s osv-scanner-version default = %q", OSVScannerVersion, govPyVulnYML, got)
	}
	// The digest cannot be derived offline; the workflow fails closed on a mismatch,
	// so only its shape is checked here. Re-derive it from the release's
	// osv-scanner_SHA256SUMS (linux_amd64 line) whenever the version changes.
	sum := scalar(path(in, "osv-scanner-sha256", "default"))
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(sum) {
		t.Errorf("%s osv-scanner-sha256 default = %q, want 64 lowercase hex characters", govPyVulnYML, sum)
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

// The py-vuln hook reads .osv-scanner-version; CI's python-vuln.yml has its own default.
func TestPolicyOSVScannerPinFile(t *testing.T) {
	got := strings.TrimPrefix(strings.TrimSpace(string(govRead(t, ".osv-scanner-version"))), "v")
	if got != OSVScannerVersion {
		t.Errorf(".osv-scanner-version = %s but policy.go OSVScannerVersion = %s (python-vuln.yml default); bump them together", got, OSVScannerVersion)
	}
}

func TestPreCommitHooksPyVuln(t *testing.T) {
	const rel = ".pre-commit-hooks.yaml"
	var hook *yaml.Node
	for _, h := range govYAML(t, rel).Content {
		if scalar(val(h, "id")) == HookPyVuln {
			hook = h
		}
	}
	if hook == nil {
		t.Fatalf("%s has no %q hook (policy.go HookPyVuln)", rel, HookPyVuln)
	}
	if st, ok := strSet(val(hook, "stages")); !ok || !sameSet(st, setOf("pre-push", "manual")) {
		t.Errorf("%s %s stages = %s, want {manual, pre-push} (PyVulnRecipe runs --hook-stage manual)", rel, HookPyVuln, fmtSet(st))
	}
	entry := scalar(val(hook, "entry"))
	info, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(entry)))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("%s %s entry %q must be an executable file (language: script): %v", rel, HookPyVuln, entry, err)
	}
}

// The uv pin has no workflow default to tie to; the Python template carries it.
func TestTemplatesPythonPins(t *testing.T) {
	const rel = "templates/python/pre-commit-config.yaml"
	want := map[string]string{
		"https://github.com/astral-sh/uv-pre-commit": UVVersion,
		RuffHookRepo: "v" + RuffVersion,
	}
	for _, r := range readPCRepos(govYAML(t, rel)) {
		u := normRepoURL(r.url)
		if w, ok := want[u]; ok {
			if got := scalar(r.rev); got != w {
				t.Errorf("%s %s rev = %q but policy.go wants %q", rel, u, got, w)
			}
			delete(want, u)
		}
		if u == GovernanceHookRepoURL && !immutableHookRev.MatchString(scalar(r.rev)) {
			t.Errorf("%s governance rev = %q, want hooks/vX.Y.Z", rel, scalar(r.rev))
		}
	}
	for _, u := range sortedKeys(want) {
		t.Errorf("%s has no %s entry", rel, u)
	}
}
