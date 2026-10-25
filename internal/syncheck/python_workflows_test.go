package syncheck

import (
	"regexp"
	"strings"
	"testing"
)

// pythonWorkflows are the reusable workflows added with the Python profile.
var pythonWorkflows = []string{"python-lint", "python-typecheck", "python-vuln", "python-test", "python-docker"}

var (
	shaPinned   = regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`)
	versionNote = regexp.MustCompile(`#\s*v\d+(\.\d+)*\s*$`)
	ctxInShell  = regexp.MustCompile(`\$\{\{[^}]*\b(inputs|matrix|secrets|github\.event)\b`)
)

// A reusable workflow cannot read its own files at the caller's pinned ref, so three
// scripts are inlined into the workflows. These tests keep each pair byte-identical.
func TestPythonInlinedBodiesMatchScripts(t *testing.T) {
	cases := []struct{ script, workflow, marker string }{
		{"scripts/python_pins.py", ".github/workflows/python-lint.yml", "python-pins body"},
		{"scripts/python_coverage_floor.py", ".github/workflows/python-test.yml", "python-coverage-floor body"},
		{"scripts/python_vuln_fix.py", ".github/workflows/python-vuln-fix.yml", "python-vuln-fix body"},
	}
	for _, c := range cases {
		want := markedBlock(t, string(govRead(t, c.script)), c.marker, c.script)
		got := markedBlock(t, string(govRead(t, c.workflow)), c.marker, c.workflow)
		if got != want {
			t.Errorf("%s inlines a different `%s` block than %s; regenerate the workflow block from the script", c.workflow, c.marker, c.script)
		}
	}
}

// markedBlock returns the lines from `# >>> marker` to `# <<< marker`, with the
// leading indentation of the opening marker removed from every line.
func markedBlock(t *testing.T, text, marker, where string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	start, end := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case "# >>> " + marker:
			start = i
		case "# <<< " + marker:
			end = i
		}
	}
	if start < 0 || end <= start {
		t.Fatalf("%s: markers `# >>> %s` / `# <<< %s` missing or out of order", where, marker, marker)
	}
	indent := lines[start][:len(lines[start])-len(strings.TrimLeft(lines[start], " "))]
	var out []string
	for _, l := range lines[start : end+1] {
		out = append(out, strings.TrimPrefix(l, indent))
	}
	return strings.Join(out, "\n")
}

// The hard rules for every Python workflow: SHA-pinned actions, no persisted
// credentials, read-only token, inputs only through env, timeouts, no secrets.
func TestPythonWorkflowsHardRules(t *testing.T) {
	for _, name := range pythonWorkflows {
		rel := ".github/workflows/" + name + ".yml"
		doc := govYAML(t, rel)

		if p, ok := readPerms(val(doc, "permissions")); !ok || !p.equal(PermsRead) {
			t.Errorf("%s top-level permissions = %s, want %s", rel, describePerms(val(doc, "permissions")), PermsRead)
		}
		if secrets := path(doc, "on", "workflow_call", "secrets"); secrets != nil {
			t.Errorf("%s declares workflow_call secrets; Python workflows take none", rel)
		}
		jobs := val(doc, "jobs")
		if len(keys(jobs)) != 1 || keys(jobs)[0] != strings.TrimPrefix(name, "python-") {
			t.Errorf("%s jobs = %v, want the single job %q (the caller's check name depends on it)", rel, keys(jobs), strings.TrimPrefix(name, "python-"))
		}
		for _, jid := range keys(jobs) {
			job := val(jobs, jid)
			if val(job, "permissions") != nil {
				t.Errorf("%s job %s sets permissions; the top-level read applies", rel, jid)
			}
			if scalar(val(job, "timeout-minutes")) == "" {
				t.Errorf("%s job %s has no timeout-minutes", rel, jid)
			}
			if !strings.Contains(scalar(path(job, "strategy", "matrix", "project")), "fromJSON(inputs.projects)") {
				t.Errorf("%s job %s must matrix over fromJSON(inputs.projects)", rel, jid)
			}
			checkouts := 0
			for _, step := range val(job, "steps").Content {
				step = deref(step)
				if run := scalar(val(step, "run")); ctxInShell.MatchString(run) {
					t.Errorf("%s step %q interpolates a context into run:; pass it through env:", rel, scalar(val(step, "name")))
				}
				uses := scalar(val(step, "uses"))
				if uses == "" {
					continue
				}
				if !shaPinned.MatchString(uses) {
					t.Errorf("%s uses %q, want <action>@<40 hex>", rel, uses)
				}
				if _, v := get(step, "uses"); v != nil && !versionNote.MatchString(v.LineComment) {
					t.Errorf("%s uses %q without a trailing `# vX.Y.Z` comment (got %q)", rel, uses, v.LineComment)
				}
				if strings.HasPrefix(uses, "actions/checkout@") {
					checkouts++
					if !isBool(path(step, "with", "persist-credentials"), false) {
						t.Errorf("%s checkout without `persist-credentials: false`", rel)
					}
				}
			}
			if checkouts != 1 {
				t.Errorf("%s job %s has %d checkouts, want 1", rel, jid, checkouts)
			}
		}
	}
}

// python-vuln-fix.yml is the one Python workflow that writes: it cannot share the
// read-only rules above, so its own hard rules live here.
func TestPythonVulnFixHardRules(t *testing.T) {
	const rel = ".github/workflows/python-vuln-fix.yml"
	doc := govYAML(t, rel)

	if p, ok := readPerms(val(doc, "permissions")); !ok || !p.equal(PermsVulnFixJob) {
		t.Errorf("%s top-level permissions = %s, want %s (policy.go PermsVulnFixJob)", rel, describePerms(val(doc, "permissions")), PermsVulnFixJob)
	}
	secrets := path(doc, "on", "workflow_call", "secrets")
	if got := keys(secrets); !equalStrings(got, vulnFixSecrets) {
		t.Errorf("%s declares workflow_call secrets %v, want exactly %v", rel, got, vulnFixSecrets)
	}
	for _, name := range keys(secrets) {
		if !isBool(path(secrets, name, "required"), false) {
			t.Errorf("%s secret %s must be `required: false`: the App is optional", rel, name)
		}
	}
	jobs := val(doc, "jobs")
	if got := keys(jobs); len(got) != 1 || got[0] != "fix" {
		t.Fatalf("%s jobs = %v, want the single job \"fix\"", rel, got)
	}
	job := val(jobs, "fix")
	if val(job, "permissions") != nil {
		t.Errorf("%s job fix sets permissions; the top-level grant applies", rel)
	}
	if scalar(val(job, "timeout-minutes")) == "" {
		t.Errorf("%s job fix has no timeout-minutes", rel)
	}
	checkouts := 0
	for _, step := range val(job, "steps").Content {
		step = deref(step)
		if run := scalar(val(step, "run")); ctxInShell.MatchString(run) {
			t.Errorf("%s step %q interpolates a context into run:; pass it through env:", rel, scalar(val(step, "name")))
		}
		uses := scalar(val(step, "uses"))
		if uses == "" {
			continue
		}
		if !shaPinned.MatchString(uses) {
			t.Errorf("%s uses %q, want <action>@<40 hex>", rel, uses)
		}
		if _, v := get(step, "uses"); v != nil && !versionNote.MatchString(v.LineComment) {
			t.Errorf("%s uses %q without a trailing `# vX.Y.Z` comment (got %q)", rel, uses, v.LineComment)
		}
		if strings.HasPrefix(uses, "actions/checkout@") {
			checkouts++
			if !isBool(path(step, "with", "persist-credentials"), false) {
				t.Errorf("%s checkout without `persist-credentials: false`", rel)
			}
		}
	}
	if checkouts != 1 {
		t.Errorf("%s has %d checkouts, want 1", rel, checkouts)
	}
}

// python-vuln-fix.yml installs the same osv-scanner as python-vuln.yml and the
// hook (.osv-scanner-version): a different scanner would disagree with the `vuln`
// job on what is fixed.
func TestPythonVulnFixOSVDefaultsMatchPythonVuln(t *testing.T) {
	def := func(rel, input string) string {
		return scalar(path(govYAML(t, rel), "on", "workflow_call", "inputs", input, "default"))
	}
	for _, input := range []string{"osv-scanner-version", "osv-scanner-sha256"} {
		fix, vuln := def(".github/workflows/python-vuln-fix.yml", input), def(".github/workflows/python-vuln.yml", input)
		if fix == "" || fix != vuln {
			t.Errorf("python-vuln-fix.yml %s default = %q but python-vuln.yml has %q; change both together", input, fix, vuln)
		}
	}
	pin := strings.TrimPrefix(strings.TrimSpace(string(govRead(t, ".osv-scanner-version"))), "v")
	if got := strings.TrimPrefix(def(".github/workflows/python-vuln-fix.yml", "osv-scanner-version"), "v"); got != pin {
		t.Errorf("python-vuln-fix.yml osv-scanner-version default = %s but .osv-scanner-version = %s", got, pin)
	}
}
