package syncheck

import (
	"strings"
	"testing"
)

// Callers move from four Python jobs to one `check` job (python-ci.yml). Both
// shapes pass until every caller has migrated; a mix is a finding.

const pyCheckJob = `  check:
    if: github.event.pull_request.draft == false
    uses: tclavelloux/promy-github-workflows/.github/workflows/python-ci.yml@python-ci/v1
    with:
      projects: '["src/app","src/worker"]'
      workflow-sync: true

`

// pyMerged rewrites the pass-python ci.yml to the single-job shape.
func pyMerged(t *testing.T, root string) {
	t.Helper()
	s := readFile(t, root, ciRel)
	i, j := strings.Index(s, "  lint:\n"), strings.Index(s, "  docker:\n")
	if i < 0 || j < i {
		t.Fatal("pass-python ci.yml shape changed")
	}
	writeFile(t, root, ciRel, s[:i]+pyCheckJob+s[j:])
}

func TestPythonSingleCheckJobPasses(t *testing.T) {
	root := fixture(t, pyPassFixture)
	pyMerged(t, root)
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("single check job must pass:\n%s", joinMessages(rep.Violations))
	}
}

func TestPythonCheckJobFindings(t *testing.T) {
	cases := []struct {
		name string
		do   func(t *testing.T, root string)
		rule string
		msg  string
	}{
		{"check next to a legacy job", func(t *testing.T, r string) {
			pyMerged(t, r)
			appendFile(t, r, ciRel, "\n  lint:\n    if: github.event.pull_request.draft == false\n    uses: tclavelloux/promy-github-workflows/.github/workflows/python-lint.yml@python-lint/v1\n    with:\n      projects: "+bothProjects+"\n      workflow-sync: true\n")
		}, "ci-jobs", `job "check" (python-ci.yml) replaces`},
		{"no check and no legacy jobs", func(t *testing.T, r string) {
			pyMerged(t, r)
			replace(t, r, ciRel, pyCheckJob, "")
		}, "ci-jobs", `add the single job "check"`},
		{"check omits a project", func(t *testing.T, r string) {
			pyMerged(t, r)
			replace(t, r, ciRel, "projects: "+bothProjects+"\n      workflow-sync", "projects: '[\"src/app\"]'\n      workflow-sync")
		}, "ci-projects", "missing: [src/worker]"},
		{"check without workflow-sync", func(t *testing.T, r string) {
			pyMerged(t, r)
			replace(t, r, ciRel, "      workflow-sync: true\n", "")
		}, "ci-lint-sync", `job "check"`},
		{"check has permissions", func(t *testing.T, r string) {
			pyMerged(t, r)
			replace(t, r, ciRel, "  check:\n", "  check:\n    permissions:\n      contents: read\n")
		}, "ci-job-permissions", `job "check"`},
		{"check calls python-lint", func(t *testing.T, r string) {
			pyMerged(t, r)
			replace(t, r, ciRel, "python-ci.yml@python-ci/v1", "python-lint.yml@python-lint/v1")
		}, "ci-job-ref", "python-ci.yml@python-ci/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, pyPassFixture)
			tc.do(t, root)
			rep := run(t, root)
			if got := ruleIDs(rep.Violations); !equalStrings(got, []string{tc.rule}) {
				t.Fatalf("rules = %v, want [%s]\n%s", got, tc.rule, joinMessages(rep.Violations))
			}
			if !strings.Contains(joinMessages(rep.Violations), tc.msg) {
				t.Errorf("no message contains %q:\n%s", tc.msg, joinMessages(rep.Violations))
			}
		})
	}
}
