package syncheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

const (
	covSyncYML    = ".github/workflows/coverage-pin-sync.yml"
	covSyncScript = "scripts/coverage-pin-sync.sh"
	appTokenRe    = `actions/create-github-app-token@[0-9a-f]{40}`
)

const covWorkflowFmt = "      - uses: vladopajic/go-test-coverage@6890500b00a7e5862e54662c04ea9fa7c799ce1e # v%s\n"

const covPolicyFmt = `package syncheck

const (
	// comment
	GoTestCoverageVersion = "%s"
	GolangciLintVersion   = "2.15.0"
)
`

// runSyncScript runs the real script on temp copies. Returns the workflow and
// policy paths, the GITHUB_OUTPUT contents and the run error.
func runSyncScript(t *testing.T, workflow, policy string) (policyPath, outputs string, err error) {
	t.Helper()
	dir := t.TempDir()
	wf, pol, out := filepath.Join(dir, "go-coverage.yml"), filepath.Join(dir, "policy.go"), filepath.Join(dir, "out")
	for p, c := range map[string]string{wf: workflow, pol: policy, out: ""} {
		if werr := os.WriteFile(p, []byte(c), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	script, _ := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(covSyncScript)))
	cmd := exec.Command("bash", "--noprofile", "--norc", script, wf, pol)
	cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+out)
	b, err := cmd.CombinedOutput()
	o, _ := os.ReadFile(out)
	if err != nil {
		err = &scriptErr{err: err, out: string(b)}
	}
	return pol, string(o), err
}

type scriptErr struct {
	err error
	out string
}

func (e *scriptErr) Error() string { return e.err.Error() + ": " + e.out }

func TestCoverageSyncScriptBump(t *testing.T) {
	old := strings.Replace(covPolicyFmt, "%s", "2.21.0", 1)
	pol, out, err := runSyncScript(t, strings.Replace(covWorkflowFmt, "%s", "2.23.0", 1), old)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "changed=true") || !strings.Contains(out, "version=2.23.0") {
		t.Errorf("outputs = %q, want changed=true and version=2.23.0", out)
	}
	b, _ := os.ReadFile(pol)
	if want := strings.Replace(covPolicyFmt, "%s", "2.23.0", 1); string(b) != want {
		t.Errorf("policy after bump:\n%s\nwant:\n%s", b, want)
	}
}

func TestCoverageSyncScriptNoop(t *testing.T) {
	same := strings.Replace(covPolicyFmt, "%s", "2.23.0", 1)
	pol, out, err := runSyncScript(t, strings.Replace(covWorkflowFmt, "%s", "2.23.0", 1), same)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "changed=false") || !strings.Contains(out, "version=2.23.0") {
		t.Errorf("outputs = %q, want changed=false and version=2.23.0", out)
	}
	if b, _ := os.ReadFile(pol); string(b) != same {
		t.Errorf("policy changed on a no-op:\n%s", b)
	}
}

func TestCoverageSyncScriptFailures(t *testing.T) {
	good := strings.Replace(covPolicyFmt, "%s", "2.23.0", 1)
	wf := strings.Replace(covWorkflowFmt, "%s", "2.23.0", 1)
	for _, tc := range []struct {
		name, workflow, policy string
	}{
		{"missing pin comment", "      - uses: vladopajic/go-test-coverage@6890500b00a7e5862e54662c04ea9fa7c799ce1e\n", good},
		{"two pins", wf + wf, good},
		{"missing const", wf, "package syncheck\n\nconst GolangciLintVersion = \"2.15.0\"\n"},
		{"two consts", wf, good + "\tGoTestCoverageVersion = \"1.0.0\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pol, out, err := runSyncScript(t, tc.workflow, tc.policy)
			if err == nil {
				t.Fatalf("script succeeded, outputs %q", out)
			}
			if strings.Contains(out, "changed=") {
				t.Errorf("failure wrote outputs %q", out)
			}
			if b, _ := os.ReadFile(pol); string(b) != tc.policy {
				t.Errorf("policy modified on failure:\n%s", b)
			}
		})
	}
}

func covSyncStep(t *testing.T, id string) *yaml.Node {
	t.Helper()
	steps := path(govYAML(t, covSyncYML), "jobs", "sync", "steps")
	if !isSeq(steps) {
		t.Fatalf("%s: jobs.sync.steps is not a list", covSyncYML)
	}
	for _, s := range steps.Content {
		if scalar(val(s, "id")) == id {
			return s
		}
	}
	t.Fatalf("%s: no step with id %q", covSyncYML, id)
	return nil
}

func TestCoverageSyncWorkflowGuard(t *testing.T) {
	cond := strings.Join(strings.Fields(scalar(path(govYAML(t, covSyncYML), "jobs", "sync", "if"))), " ")
	for _, want := range []string{
		"github.event.pull_request.user.login == 'dependabot[bot]'",
		"github.event.pull_request.head.repo.full_name == github.repository",
		"startsWith(github.event.pull_request.head.ref, 'dependabot/github_actions/vladopajic/go-test-coverage-')",
	} {
		if !strings.Contains(cond, want) {
			t.Errorf("%s jobs.sync.if lacks %q: %s", covSyncYML, want, cond)
		}
	}
	if strings.Contains(cond, "||") {
		t.Errorf("%s jobs.sync.if must be a pure conjunction: %s", covSyncYML, cond)
	}
}

func TestCoverageSyncWorkflowToken(t *testing.T) {
	step := covSyncStep(t, "app-token")
	with := val(step, "with")
	var perms []string
	for i := 0; i+1 < len(with.Content); i += 2 {
		k := with.Content[i].Value
		if strings.HasPrefix(k, "permission-") {
			perms = append(perms, k+"="+scalar(with.Content[i+1]))
		}
	}
	if want := []string{"permission-contents=write", "permission-pull-requests=write"}; !equalStrings(perms, want) {
		t.Errorf("token permissions = %v, want %v", perms, want)
	}
	re := regexp.MustCompile(appTokenRe)
	got := re.FindString(scalar(val(step, "uses")))
	if got == "" {
		t.Fatalf("token step uses %q, want a SHA-pinned create-github-app-token", scalar(val(step, "uses")))
	}
	if fleet := re.FindString(string(govRead(t, automergeGateYML))); fleet != got {
		t.Errorf("token action = %q, but %s pins %q", got, automergeGateYML, fleet)
	}
}

func TestCoverageSyncWorkflowSyncStepHasNoSecrets(t *testing.T) {
	step := covSyncStep(t, "sync")
	if env := val(step, "env"); env != nil {
		t.Errorf("sync step must have no env, has %d entries", len(env.Content)/2)
	}
	if run := scalar(val(step, "run")); strings.Contains(run, "secrets.") || strings.Contains(run, "app-token") {
		t.Errorf("sync step references secrets: %s", run)
	}
	perms := path(govYAML(t, covSyncYML), "permissions")
	if scalar(val(perms, "contents")) != "read" || len(perms.Content) != 2 {
		t.Errorf("top-level permissions must be exactly contents: read")
	}
}
