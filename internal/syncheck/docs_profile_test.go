package syncheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const docsPassFixture = "pass-docs"

func TestDocsProfileDetection(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		dirs  []string
		want  *profile
		msg   string // substring of profileMsg when want is nil
	}{
		{name: "marker", files: map[string]string{ProfileMarkerFile: "docs\n"}, want: docsProfile},
		{name: "marker without newline", files: map[string]string{ProfileMarkerFile: "docs"}, want: docsProfile},
		{name: "marker with surrounding blanks", files: map[string]string{ProfileMarkerFile: "  docs \r\n"}, want: docsProfile},
		{name: "marker + root go.mod", files: map[string]string{ProfileMarkerFile: "docs\n", "go.mod": "x\n"}, msg: "go.mod exists"},
		{name: "marker + root pyproject.toml", files: map[string]string{ProfileMarkerFile: "docs\n", "pyproject.toml": "x\n"}, msg: "pyproject.toml exists"},
		{name: "marker + nested uv.lock", files: map[string]string{ProfileMarkerFile: "docs\n", "src/app/uv.lock": "x\n"}, msg: "src/app/uv.lock exists"},
		{name: "marker + nested go.mod is still docs", files: map[string]string{ProfileMarkerFile: "docs\n", "sub/go.mod": "x\n"}, want: docsProfile},
		{name: "marker value go", files: map[string]string{ProfileMarkerFile: "go\n"}, msg: `holds "go"`},
		{name: "empty marker", files: map[string]string{ProfileMarkerFile: ""}, msg: `holds ""`},
		{name: "marker value is case-sensitive", files: map[string]string{ProfileMarkerFile: "Docs\n"}, msg: `holds "Docs"`},
		{name: "marker directory is no marker", dirs: []string{ProfileMarkerFile}, msg: ""},
		{name: "no marker, go.mod", files: map[string]string{"go.mod": "x\n"}, want: goProfile},
		{name: "no marker, pyproject", files: map[string]string{"pyproject.toml": "x\n"}, want: pythonProfile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for f, content := range tc.files {
				writeFile(t, root, f, content)
			}
			for _, d := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			c := &checker{root: root, files: map[string][]string{}, docs: map[string]*parsed{}}
			got := c.detectProfile()
			if got != tc.want {
				t.Fatalf("profile = %s, want %s (%s)", profName(got), profName(tc.want), c.profileMsg)
			}
			if tc.want == nil && !strings.Contains(c.profileMsg, tc.msg) {
				t.Fatalf("profileMsg = %q, want it to contain %q", c.profileMsg, tc.msg)
			}
		})
	}
}

// A marker conflict surfaces as the one un-exemptable `profile` violation, and
// nothing else is checked.
func TestDocsProfileMarkerConflictIsViolation(t *testing.T) {
	root := fixture(t, docsPassFixture)
	writeFile(t, root, "go.mod", "module x\n")
	rep := run(t, root)
	if len(rep.Violations) != 1 || rep.Violations[0].Rule != ruleProfile || !strings.Contains(rep.Violations[0].Message, "go.mod") {
		t.Fatalf("want exactly one profile violation naming go.mod, got:\n%s", joinMessages(rep.Violations))
	}
}

func TestDocsProfileUnknownMarkerValueIsViolation(t *testing.T) {
	root := fixture(t, docsPassFixture)
	writeFile(t, root, ProfileMarkerFile, "python\n")
	rep := run(t, root)
	if len(rep.Violations) != 1 || rep.Violations[0].Rule != ruleProfile {
		t.Fatalf("want exactly one profile violation, got:\n%s", joinMessages(rep.Violations))
	}
}

func TestDocsProfilePasses(t *testing.T) {
	rep := run(t, fixture(t, docsPassFixture))
	if !rep.OK() {
		t.Fatalf("pass-docs fails:\n%s", joinMessages(rep.Violations))
	}
}

// ci-jobs: lint + gitleaks pass; every forbidden job is named as such; any
// other job is unknown; a required job left out is missing.
func TestDocsProfileCIJobs(t *testing.T) {
	extra := func(name string) string {
		return "\n  " + name + ":\n    if: github.event.pull_request.draft == false\n    runs-on: ubuntu-latest\n    timeout-minutes: 5\n    steps:\n      - run: echo hi\n"
	}
	for _, job := range DocsCIForbiddenJobs {
		t.Run("forbidden "+job, func(t *testing.T) {
			root := fixture(t, docsPassFixture)
			writeFile(t, root, ciRel, readFile(t, root, ciRel)+extra(job))
			rep := run(t, root)
			if got := ruleIDs(rep.Violations); !equalStrings(got, []string{ruleCIJobs}) {
				t.Fatalf("rules = %v, want [ci-jobs]:\n%s", got, joinMessages(rep.Violations))
			}
			if m := joinMessages(rep.Violations); !strings.Contains(m, `job "`+job+`" is not allowed in the docs profile`) {
				t.Fatalf("message does not name the forbidden job:\n%s", m)
			}
		})
	}
	t.Run("unknown job", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		writeFile(t, root, ciRel, readFile(t, root, ciRel)+extra("spellcheck"))
		rep := run(t, root)
		if m := joinMessages(rep.Violations); !strings.Contains(m, `unknown job "spellcheck"; allowed: lint, gitleaks, automerge`) {
			t.Fatalf("want unknown-job finding listing the docs set:\n%s", m)
		}
	})
	t.Run("missing gitleaks", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		s := readFile(t, root, ciRel)
		writeFile(t, root, ciRel, s[:strings.Index(s, "  gitleaks:")])
		rep := run(t, root)
		if m := joinMessages(rep.Violations); !strings.Contains(m, `required job "gitleaks" is missing`) {
			t.Fatalf("want missing gitleaks:\n%s", m)
		}
	})
	t.Run("lint must call docs-lint", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		replace(t, root, ciRel, "docs-lint.yml@docs-lint/v1", "python-lint.yml@python-lint/v1")
		rep := run(t, root)
		if got := ruleIDs(rep.Violations); !equalStrings(got, []string{ruleCIJobRef}) {
			t.Fatalf("rules = %v, want [ci-job-ref]:\n%s", got, joinMessages(rep.Violations))
		}
	})
	t.Run("docs-lint at a frozen ref", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		replace(t, root, ciRel, "@docs-lint/v1", "@docs-lint/v1.0.0")
		if got := ruleIDs(run(t, root).Violations); !equalStrings(got, []string{ruleGovRef}) {
			t.Fatalf("rules = %v, want [governance-ref]", got)
		}
	})
	t.Run("a Dockerfile does not require docker", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		writeFile(t, root, "Dockerfile", "FROM scratch\n")
		if rep := run(t, root); !rep.OK() {
			t.Fatalf("Dockerfile in a docs repo fails:\n%s", joinMessages(rep.Violations))
		}
	})
	t.Run("lint needs no workflow-sync input", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		if strings.Contains(readFile(t, root, ciRel), "workflow-sync:") {
			t.Fatal("fixture lint job must not set with.workflow-sync")
		}
		if rep := run(t, root); !rep.OK() {
			t.Fatalf("fails:\n%s", joinMessages(rep.Violations))
		}
	})
}

// Rules shared with the other profiles still bind a docs repo.
func TestDocsProfileSharedRulesApply(t *testing.T) {
	cases := []struct {
		name, rel, old, new, rule string
	}{
		{"ci-trigger", ciRel, "branches: [main]", "branches: [main, dev]", ruleCITrigger},
		{"ci-concurrency", ciRel, "cancel-in-progress: true", "cancel-in-progress: false", ruleCIConcurrency},
		{"ci-permissions", ciRel, "permissions:\n  contents: read\n\njobs:", "permissions:\n  contents: write\n\njobs:", ruleCIPerms},
		{"ci-draft-skip", ciRel, "  lint:\n    if: github.event.pull_request.draft == false\n", "  lint:\n", ruleCIDraftSkip},
		{"ci-timeout", ciRel, "    timeout-minutes: 5\n", "", ruleCITimeout},
		{"ci-job-permissions lint", ciRel, "    uses: tclavelloux/promy-github-workflows/.github/workflows/docs-lint.yml", "    permissions:\n      contents: read\n    uses: tclavelloux/promy-github-workflows/.github/workflows/docs-lint.yml", ruleCIJobPerms},
		{"gitleaks permissions", ciRel, "      pull-requests: read\n", "      pull-requests: write\n", ruleCIJobPerms},
		{"persist-credentials", ciRel, "          persist-credentials: false\n", "", rulePersistCreds},
		{"sha-pin", ciRel, "gitleaks/gitleaks-action@e0c47f4f8be36e29cdc102c57e68cb5cbf0e8d1e", "gitleaks/gitleaks-action@v3", ruleSHAPin},
		{"pr-title types", prRel, "types: [opened, edited, reopened, synchronize]", "types: [opened]", rulePRTitle},
		{"precommit rev", PreCommitConfig, "rev: hooks/v1.8.0", "rev: hooks/v1", rulePCGovRepo},
		{"precommit commit-msg", PreCommitConfig, "      - id: conventional-pre-commit\n", "      - id: conventional-pre-commit-x\n", rulePCCommitMsg},
		{"make-setup", MakefileRel, "pre-commit install --install-hooks", "pre-commit install", ruleMakeSetup},
		{"dependabot", depRel, "package-ecosystem: github-actions", "package-ecosystem: npm", ruleDependabot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, docsPassFixture)
			replace(t, root, tc.rel, tc.old, tc.new)
			rep := run(t, root)
			found := false
			for _, f := range rep.Violations {
				found = found || f.Rule == tc.rule
			}
			if !found {
				t.Fatalf("want a %s violation, got rules %v:\n%s", tc.rule, ruleIDs(rep.Violations), joinMessages(rep.Violations))
			}
		})
	}
	t.Run("missing pr-title.yml", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		if err := os.Remove(filepath.Join(root, prRel)); err != nil {
			t.Fatal(err)
		}
		if got := ruleIDs(run(t, root).Violations); !equalStrings(got, []string{rulePRTitle}) {
			t.Fatalf("rules = %v, want [pr-title]", got)
		}
	})
	t.Run("missing ci.yml", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		if err := os.Remove(filepath.Join(root, ciRel)); err != nil {
			t.Fatal(err)
		}
		if m := joinMessages(run(t, root).Violations); !strings.Contains(m, "templates/docs/ci.yml") {
			t.Fatalf("want the docs template named:\n%s", m)
		}
	})
}

// Rules of the code profiles that make no sense for a repo with no code.
func TestDocsProfileSkipsCodeRules(t *testing.T) {
	root := fixture(t, docsPassFixture)
	for _, rel := range []string{rpRel, vfRel, ".golangci-version", ".testcoverage.yml"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Fatalf("fixture must not carry %s", rel)
		}
	}
	// No release-please.yml, vuln-fix.yml, golangci/testcoverage files, coverage or
	// vuln hooks, Makefile targets: pass-docs has none and passes.
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("fails:\n%s", joinMessages(rep.Violations))
	}
	t.Run("release-please.yml is optional but checked when present", func(t *testing.T) {
		root := fixture(t, docsPassFixture)
		writeFile(t, root, rpRel, string(govRead(t, "templates/release-please.yml")))
		if rep := run(t, root); !rep.OK() {
			t.Fatalf("template release-please.yml fails the docs profile:\n%s", joinMessages(rep.Violations))
		}
		replace(t, root, rpRel, "branches: [main]", "branches: [main, dev]")
		if got := ruleIDs(run(t, root).Violations); !equalStrings(got, []string{ruleReleasePlease}) {
			t.Fatalf("rules = %v, want [release-please]", got)
		}
	})
	t.Run("gomod ecosystem is not required", func(t *testing.T) {
		if strings.Contains(readFile(t, fixture(t, docsPassFixture), depRel), "gomod") {
			t.Fatal("fixture dependabot.yml must not list gomod")
		}
	})
}

// The docs templates are what a docs repo copies; they must pass the profile.
func TestTemplatesDocsPassProfile(t *testing.T) {
	root := fixture(t, docsPassFixture)
	for tmpl, dst := range map[string]string{
		"templates/docs/ci.yml":                 ciRel,
		"templates/docs/Makefile":               MakefileRel,
		"templates/docs/pre-commit-config.yaml": PreCommitConfig,
		"templates/docs/dependabot.yml":         depRel,
	} {
		writeFile(t, root, dst, string(govRead(t, tmpl)))
	}
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("templates/docs/* fail the docs profile:\n%s", joinMessages(rep.Violations))
	}
	// The fixture is a copy of the templates plus the marker and pr-title.yml;
	// drift between them would make the tests above prove nothing.
	for tmpl, dst := range map[string]string{
		"templates/docs/ci.yml":                 ciRel,
		"templates/docs/Makefile":               MakefileRel,
		"templates/docs/pre-commit-config.yaml": PreCommitConfig,
		"templates/docs/dependabot.yml":         depRel,
	} {
		got, err := os.ReadFile(filepath.Join("testdata", docsPassFixture, filepath.FromSlash(dst)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(govRead(t, tmpl)) {
			t.Errorf("testdata/%s/%s differs from %s; copy the template over the fixture", docsPassFixture, dst, tmpl)
		}
	}
}

// docs-lint.yml hosts the governance check for repos with no go-lint/python-lint.
func TestDocsLintWorkflowHardRules(t *testing.T) {
	const rel = ".github/workflows/docs-lint.yml"
	doc := govYAML(t, rel)

	if p, ok := readPerms(val(doc, "permissions")); !ok || !p.equal(PermsRead) {
		t.Errorf("%s top-level permissions = %s, want %s", rel, describePerms(val(doc, "permissions")), PermsRead)
	}
	if in := path(doc, "on", "workflow_call", "inputs"); in != nil {
		t.Errorf("%s declares inputs; the docs ci-lint-sync exemption assumes it has none", rel)
	}
	if secrets := path(doc, "on", "workflow_call", "secrets"); secrets != nil {
		t.Errorf("%s declares workflow_call secrets; it takes none", rel)
	}
	jobs := val(doc, "jobs")
	if got := keys(jobs); len(got) != 1 || got[0] != "lint" {
		t.Fatalf("%s jobs = %v, want the single job \"lint\" (the caller's check name depends on it)", rel, got)
	}
	job := val(jobs, "lint")
	if val(job, "permissions") != nil {
		t.Errorf("%s job lint sets permissions; the top-level read applies", rel)
	}
	if scalar(val(job, "timeout-minutes")) == "" {
		t.Errorf("%s job lint has no timeout-minutes", rel)
	}
	checkouts, runsSync := 0, false
	for _, step := range val(job, "steps").Content {
		step = deref(step)
		run := scalar(val(step, "run"))
		if ctxInShell.MatchString(run) {
			t.Errorf("%s step %q interpolates a context into run:; pass it through env:", rel, scalar(val(step, "name")))
		}
		runsSync = runsSync || strings.Contains(run, "pre-commit run workflow-sync --all-files")
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
	if !runsSync {
		t.Errorf("%s never runs `pre-commit run workflow-sync --all-files`", rel)
	}
	// Same pre-commit pin as go-lint and python-lint: one hook runner fleet-wide.
	pin := func(rel string) string {
		for _, line := range strings.Split(string(govRead(t, rel)), "\n") {
			if strings.Contains(line, "PRE_COMMIT_VERSION:") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	if got, want := pin(rel), pin(govLintYML); got == "" || got != want {
		t.Errorf("%s pins %q but %s pins %q; change them together", rel, got, govLintYML, want)
	}
}
