package syncheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const flutterPassFixture = "pass-flutter"

func TestFlutterProfileDetection(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		dirs  []string
		want  *profile
		msg   string // substring of profileMsg when want is nil
	}{
		{name: "root pubspec.yaml", files: map[string]string{"pubspec.yaml": "x\n"}, want: flutterProfile},
		{name: "nested pubspec.yaml is not Flutter", files: map[string]string{"app/pubspec.yaml": "x\n"}, msg: ""},
		{name: "pubspec.yaml directory is not Flutter", dirs: []string{"pubspec.yaml"}, msg: ""},
		{name: "pubspec.yaml + go.mod", files: map[string]string{"pubspec.yaml": "x\n", "go.mod": "x\n"}, msg: "go.mod exists"},
		{name: "pubspec.yaml + root pyproject.toml", files: map[string]string{"pubspec.yaml": "x\n", "pyproject.toml": "x\n"}, msg: "pyproject.toml exists"},
		{name: "pubspec.yaml + nested uv.lock", files: map[string]string{"pubspec.yaml": "x\n", "src/x/uv.lock": "x\n"}, msg: "src/x/uv.lock exists"},
		{name: "pubspec.yaml + docs marker", files: map[string]string{"pubspec.yaml": "x\n", ProfileMarkerFile: "docs\n"}, msg: "pubspec.yaml exists"},
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
			if tc.want == nil && tc.msg != "" && !strings.Contains(c.profileMsg, "nothing else was checked") {
				t.Fatalf("profileMsg = %q, want it to say nothing else was checked", c.profileMsg)
			}
		})
	}
}

// A conflict surfaces as the one un-exemptable `profile` violation.
func TestFlutterProfileConflictIsViolation(t *testing.T) {
	root := fixture(t, flutterPassFixture)
	writeFile(t, root, "go.mod", "module x\n")
	rep := run(t, root)
	if len(rep.Violations) != 1 || rep.Violations[0].Rule != ruleProfile || !strings.Contains(rep.Violations[0].Message, "go.mod") {
		t.Fatalf("want exactly one profile violation naming go.mod, got:\n%s", joinMessages(rep.Violations))
	}
}

func TestPassFlutter(t *testing.T) {
	rep := run(t, fixture(t, flutterPassFixture))
	if !rep.OK() || len(rep.Exemptions) != 0 {
		t.Fatalf("want clean, got:\n%s", joinMessages(rep.Violations))
	}
}

func TestFlutterDrift(t *testing.T) {
	rm := func(rel string) func(t *testing.T, r string) {
		return func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, rel)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// dropJob removes the job block that starts at `  name:\n` and ends at `  next:\n`,
	// and drops name from automerge's needs.
	dropJob := func(name, next, needsOld, needsNew string) func(t *testing.T, r string) {
		return func(t *testing.T, r string) {
			s := readFile(t, r, ciRel)
			i, j := strings.Index(s, "  "+name+":\n"), strings.Index(s, "  "+next+":\n")
			writeFile(t, r, ciRel, s[:i]+s[j:])
			replace(t, r, ciRel, needsOld, needsNew)
		}
	}
	extra := func(name string) func(t *testing.T, r string) {
		return func(t *testing.T, r string) {
			replace(t, r, ciRel, "\n  automerge:", extraJob+"\n  automerge:")
			replace(t, r, ciRel, "  extra:\n", "  "+name+":\n")
			replace(t, r, ciRel, "needs: [lint, test, gitleaks]", "needs: [lint, test, gitleaks, "+name+"]")
		}
	}
	cases := []struct {
		name  string
		edits []edit
		do    func(t *testing.T, root string)
		rules []string
		msg   string
	}{
		{name: "lint job missing", do: dropJob("lint", "test", "needs: [lint, test, gitleaks]", "needs: [test, gitleaks]"), rules: []string{"ci-jobs"}, msg: `required job "lint"`},
		{name: "test job missing", do: dropJob("test", "gitleaks", "needs: [lint, test, gitleaks]", "needs: [lint, gitleaks]"), rules: []string{"ci-jobs"}, msg: `required job "test"`},
		{name: "gitleaks job missing", do: dropJob("gitleaks", "automerge", "needs: [lint, test, gitleaks]", "needs: [lint, test]"), rules: []string{"ci-jobs"}, msg: `required job "gitleaks"`},
		{name: "docker job is unknown", do: extra("docker"), rules: []string{"ci-jobs"}, msg: `unknown job "docker"; allowed: lint, test, gitleaks, automerge`},
		{name: "vuln job is unknown", do: extra("vuln"), rules: []string{"ci-jobs"}, msg: `unknown job "vuln"`},
		{name: "coverage job is unknown", do: extra("coverage"), rules: []string{"ci-jobs"}, msg: `unknown job "coverage"`},
		{name: "typecheck job is unknown", do: extra("typecheck"), rules: []string{"ci-jobs"}, msg: `unknown job "typecheck"`},
		{name: "lint calls go-lint", edits: []edit{ed(ciRel, "flutter-lint.yml@flutter-lint/v1", "go-lint.yml@go-lint/v1")}, rules: []string{"ci-job-ref"}, msg: "flutter-lint.yml@flutter-lint/v1"},
		{name: "test at a wrong major", edits: []edit{ed(ciRel, "flutter-test/v1", "flutter-test/v2")}, rules: []string{"governance-ref"}, msg: "flutter-test"},
		{name: "lint without workflow-sync", edits: []edit{ed(ciRel, lintSyncBlock, "")}, rules: []string{"ci-lint-sync"}, msg: "(absent)"},
		{name: "lint has permissions", edits: []edit{ed(ciRel, "  lint:\n", "  lint:\n    permissions:\n      contents: read\n")}, rules: []string{"ci-job-permissions"}, msg: `job "lint"`},
		{name: "flutter-version missing", do: rm(".flutter-version"), rules: []string{"flutter-version"}, msg: "file missing"},
		{name: "flutter-version drift", do: func(t *testing.T, r string) { writeFile(t, r, ".flutter-version", "3.47.9\n") }, rules: []string{"flutter-version"}, msg: "want 3.50.0"},
		{name: "flutter-version with v prefix passes", do: func(t *testing.T, r string) { writeFile(t, r, ".flutter-version", "v3.50.0\n") }},
		{name: "pub entry dropped", edits: []edit{ed(depRel, "package-ecosystem: pub", "package-ecosystem: npm")}, rules: []string{"dependabot"}, msg: `"pub"`},
		{name: "github-actions entry dropped", edits: []edit{ed(depRel, "package-ecosystem: github-actions", "package-ecosystem: npm")}, rules: []string{"dependabot"}, msg: `"github-actions"`},
		{name: "dependabot.yml missing", do: rm(depRel), rules: []string{"dependabot"}, msg: "templates/flutter/dependabot.yml"},
		{name: "pr-title on pull_request_target", edits: []edit{ed(prRel, "  pull_request:", "  pull_request_target:")}, rules: []string{"dangerous-trigger", "pr-title"}, msg: "pull_request_target"},
		{name: "mutable flutter-action in ci.yml", do: func(t *testing.T, r string) {
			replace(t, r, ciRel, "      - name: Run gitleaks\n", "      - uses: subosito/flutter-action@v2\n      - name: Run gitleaks\n")
		}, rules: []string{"sha-pin"}, msg: "subosito/flutter-action@v2"},
		{name: "release-please.yml missing", do: rm(rpRel), rules: []string{"release-please"}, msg: "file missing"},
		{name: "go-only rules stay silent", do: func(t *testing.T, r string) {
			for _, f := range []string{".golangci-version", ".testcoverage.yml", ".uv-version", ".ruff-version"} {
				if _, err := os.Stat(filepath.Join(r, f)); err == nil {
					t.Fatalf("fixture must not carry %s", f)
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, flutterPassFixture)
			for _, e := range tc.edits {
				replace(t, root, e.file, e.old, e.new)
			}
			if tc.do != nil {
				tc.do(t, root)
			}
			rep := run(t, root)
			if got := ruleIDs(rep.Violations); !equalStrings(got, tc.rules) {
				t.Fatalf("rules = %v, want %v\n%s", got, tc.rules, joinMessages(rep.Violations))
			}
			if tc.msg != "" && !strings.Contains(joinMessages(rep.Violations), tc.msg) {
				t.Errorf("no message contains %q:\n%s", tc.msg, joinMessages(rep.Violations))
			}
		})
	}
}

// Rules whose gates have not shipped for Flutter stay silent: no coverage hook,
// no vuln target, no vuln-fix.yml check.
func TestFlutterProfileSkipsUnshippedRules(t *testing.T) {
	root := fixture(t, flutterPassFixture)
	// A vuln-fix.yml that would fail the Go and Python rule is not checked.
	writeFile(t, root, vfRel, "name: x\npermissions:\n  contents: read\non:\n  schedule:\n    - cron: '0 0 * * *'\njobs:\n  fix:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n")
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("want clean, got:\n%s", joinMessages(rep.Violations))
	}
}

// templates/flutter must pass the Flutter profile as-is.
func TestTemplatesFlutter(t *testing.T) {
	root := fixture(t, flutterPassFixture)
	for tmpl, dst := range map[string]string{
		"templates/flutter/ci.yml":         ciRel,
		"templates/flutter/dependabot.yml": depRel,
	} {
		writeFile(t, root, dst, string(govRead(t, tmpl)))
	}
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("templates/flutter fails the Flutter profile:\n%s", joinMessages(rep.Violations))
	}
}
