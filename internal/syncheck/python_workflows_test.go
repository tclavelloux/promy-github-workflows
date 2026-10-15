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

// A reusable workflow cannot read its own files at the caller's pinned ref, so two
// scripts are inlined into the workflows. These tests keep each pair byte-identical.
func TestPythonInlinedBodiesMatchScripts(t *testing.T) {
	cases := []struct{ script, workflow, marker string }{
		{"scripts/python_pins.py", ".github/workflows/python-lint.yml", "python-pins body"},
		{"scripts/python_coverage_floor.py", ".github/workflows/python-test.yml", "python-coverage-floor body"},
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
