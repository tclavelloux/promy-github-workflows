package syncheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const flutterPassFixture = "pass-flutter"

// Blocks of the fixture's .pre-commit-config.yaml, for the precommit-dart-hooks cases.
const (
	dartFormatHook = `      - id: dart-format
        name: dart format
        entry: dart format --set-exit-if-changed .
        language: system
        types: [dart]
        pass_filenames: false
        stages: [pre-commit]
`
	dartAnalyzeHook = `      - id: dart-analyze
        name: dart analyze
        entry: dart analyze --fatal-infos
        language: system
        pass_filenames: false
        always_run: true
        stages: [pre-push]
`
)

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
		{name: "pubspec.yaml + go.mod + Python files", files: map[string]string{"pubspec.yaml": "x\n", "go.mod": "x\n", "tools/uv.lock": "x\n"}, msg: "go.mod exists"},
		{name: "pubspec.yaml wins over root pyproject.toml", files: map[string]string{"pubspec.yaml": "x\n", "pyproject.toml": "x\n"}, want: flutterProfile},
		{name: "pubspec.yaml wins over nested uv.lock", files: map[string]string{"pubspec.yaml": "x\n", "tools/x/uv.lock": "x\n", "tools/x/pyproject.toml": "x\n"}, want: flutterProfile},
		{name: "nested pubspec.yaml + uv.lock is Python", files: map[string]string{"app/pubspec.yaml": "x\n", "uv.lock": "x\n"}, want: pythonProfile},
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

// A root go.mod or the docs marker next to pubspec.yaml surfaces as the one
// un-exemptable `profile` violation; Python tooling files do not.
func TestFlutterProfileConflictIsViolation(t *testing.T) {
	for name, tc := range map[string]struct{ file, content, want string }{
		"go.mod":      {"go.mod", "module x\n", "go.mod"},
		"docs marker": {ProfileMarkerFile, "docs\n", "pubspec.yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t, flutterPassFixture)
			writeFile(t, root, tc.file, tc.content)
			rep := run(t, root)
			if len(rep.Violations) != 1 || rep.Violations[0].Rule != ruleProfile || !strings.Contains(rep.Violations[0].Message, tc.want) {
				t.Fatalf("want exactly one profile violation naming %s, got:\n%s", tc.want, joinMessages(rep.Violations))
			}
		})
	}
}

// Python tooling beside a root pubspec.yaml keeps the Flutter profile, and no
// Python-only rule fires on it.
func TestFlutterProfileIgnoresPythonTooling(t *testing.T) {
	root := fixture(t, flutterPassFixture)
	writeFile(t, root, "tools/gen/uv.lock", "x\n")
	writeFile(t, root, "tools/gen/pyproject.toml", "not = [valid\n")
	writeFile(t, root, "tools/gen/poetry.lock", "x\n")
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("want clean, got:\n%s", joinMessages(rep.Violations))
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
			replace(t, r, ciRel, "needs: [lint, vuln, test, gitleaks]", "needs: [lint, vuln, test, gitleaks, "+name+"]")
		}
	}
	cases := []struct {
		name  string
		edits []edit
		do    func(t *testing.T, root string)
		rules []string
		msg   string
	}{
		{name: "lint job missing", do: dropJob("lint", "vuln", "needs: [lint, vuln, test, gitleaks]", "needs: [vuln, test, gitleaks]"), rules: []string{"ci-jobs"}, msg: `required job "lint"`},
		{name: "test job missing", do: dropJob("test", "gitleaks", "needs: [lint, vuln, test, gitleaks]", "needs: [lint, vuln, gitleaks]"), rules: []string{"ci-jobs"}, msg: `required job "test"`},
		{name: "gitleaks job missing", do: dropJob("gitleaks", "automerge", "needs: [lint, vuln, test, gitleaks]", "needs: [lint, vuln, test]"), rules: []string{"ci-jobs"}, msg: `required job "gitleaks"`},
		{name: "docker job is unknown", do: extra("docker"), rules: []string{"ci-jobs"}, msg: `unknown job "docker"; allowed: lint, vuln, test, gitleaks, automerge`},
		{name: "vuln job missing", do: dropJob("vuln", "test", "needs: [lint, vuln, test, gitleaks]", "needs: [lint, test, gitleaks]"), rules: []string{"ci-jobs"}, msg: `required job "vuln"`},
		{name: "vuln calls python-vuln", edits: []edit{ed(ciRel, "flutter-vuln.yml@flutter-vuln/v1", "python-vuln.yml@python-vuln/v1")}, rules: []string{"ci-job-ref"}, msg: "flutter-vuln.yml@flutter-vuln/v1"},
		{name: "vuln has permissions", edits: []edit{ed(ciRel, "  vuln:\n", "  vuln:\n    permissions:\n      contents: read\n")}, rules: []string{"ci-job-permissions"}, msg: `job "vuln"`},
		{name: "vuln at a wrong major", edits: []edit{ed(ciRel, "flutter-vuln/v1", "flutter-vuln/v2")}, rules: []string{"governance-ref"}, msg: "flutter-vuln"},
		{name: "coverage job is unknown", do: extra("coverage"), rules: []string{"ci-jobs"}, msg: `unknown job "coverage"`},
		{name: "typecheck job is unknown", do: extra("typecheck"), rules: []string{"ci-jobs"}, msg: `unknown job "typecheck"`},
		{name: "lint calls go-lint", edits: []edit{ed(ciRel, "flutter-lint.yml@flutter-lint/v1", "go-lint.yml@go-lint/v1")}, rules: []string{"ci-job-ref"}, msg: "flutter-lint.yml@flutter-lint/v1"},
		{name: "test at a wrong major", edits: []edit{ed(ciRel, "flutter-test/v1", "flutter-test/v2")}, rules: []string{"governance-ref"}, msg: "flutter-test"},
		{name: "lint without workflow-sync", edits: []edit{ed(ciRel, lintSyncBlock, "")}, rules: []string{"ci-lint-sync"}, msg: "(absent)"},
		{name: "lint has permissions", edits: []edit{ed(ciRel, "  lint:\n", "  lint:\n    permissions:\n      contents: read\n")}, rules: []string{"ci-job-permissions"}, msg: `job "lint"`},
		// precommit-dart-hooks
		{name: "dart-format hook missing", edits: []edit{ed(pcRel, dartFormatHook, "")}, rules: []string{"precommit-dart-hooks"}, msg: `no ` + "`repo: local`" + ` hook "dart-format"`},
		{name: "dart-format at pre-push", edits: []edit{ed(pcRel, dartFormatHook, strings.Replace(dartFormatHook, "[pre-commit]", "[pre-push]", 1))}, rules: []string{"precommit-dart-hooks"}, msg: "dart-format: stages = {pre-push}"},
		{name: "dart-format without --set-exit-if-changed", edits: []edit{ed(pcRel, "dart format --set-exit-if-changed .", "dart format .")}, rules: []string{"precommit-dart-hooks"}, msg: "dart-format: entry"},
		{name: "dart-analyze hook missing", edits: []edit{ed(pcRel, dartAnalyzeHook, "")}, rules: []string{"precommit-dart-hooks"}, msg: `hook "dart-analyze"`},
		{name: "dart-analyze without --fatal-infos", edits: []edit{ed(pcRel, "dart analyze --fatal-infos", "dart analyze")}, rules: []string{"precommit-dart-hooks"}, msg: "dart-analyze: entry"},
		{name: "dart-analyze at pre-commit", edits: []edit{ed(pcRel, dartAnalyzeHook, strings.Replace(dartAnalyzeHook, "[pre-push]", "[pre-commit]", 1))}, rules: []string{"precommit-dart-hooks"}, msg: "dart-analyze: stages = {pre-commit}"},
		{name: "flutter-test without the env prefix", edits: []edit{ed(pcRel, "entry: env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE flutter test", "entry: flutter test")}, rules: []string{"precommit-dart-hooks"}, msg: "promy-frontend#46"},
		{name: "flutter-test with pass_filenames true", edits: []edit{ed(pcRel, "        entry: env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE flutter test\n        language: system\n        pass_filenames: false", "        entry: env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE flutter test\n        language: system\n        pass_filenames: true")}, rules: []string{"precommit-dart-hooks"}, msg: "flutter-test: pass_filenames must be false"},
		{name: "flutter-test language script", edits: []edit{ed(pcRel, "        entry: env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE flutter test\n        language: system", "        entry: env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE flutter test\n        language: script")}, rules: []string{"precommit-dart-hooks"}, msg: "flutter-test: language"},
		{name: "dart-analyze without always_run", edits: []edit{ed(pcRel, dartAnalyzeHook, strings.Replace(dartAnalyzeHook, "        always_run: true\n", "", 1))}, rules: []string{"precommit-dart-hooks"}, msg: "dart-analyze: always_run must be true"},
		{name: "flutter-test always_run false", edits: []edit{ed(pcRel, "        always_run: true\n        stages: [pre-push]\n", "        always_run: false\n        stages: [pre-push]\n")}, rules: []string{"precommit-dart-hooks"}, msg: "always_run must be true"},
		{name: "dart-analyze with files", edits: []edit{ed(pcRel, dartAnalyzeHook, dartAnalyzeHook+"        files: ^lib/\n")}, rules: []string{"precommit-dart-hooks"}, msg: "dart-analyze: files narrows"},
		{name: "flutter-test with exclude", edits: []edit{ed(pcRel, "        entry: env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE flutter test\n", "        entry: env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE flutter test\n        exclude: ^test/\n")}, rules: []string{"precommit-dart-hooks"}, msg: "flutter-test: exclude narrows"},
		{name: "dart-format with files", edits: []edit{ed(pcRel, dartFormatHook, dartFormatHook+"        files: ^lib/\n")}, rules: []string{"precommit-dart-hooks"}, msg: "dart-format: files narrows"},
		{name: "dart-format with other types", edits: []edit{ed(pcRel, "types: [dart]", "types: [markdown]")}, rules: []string{"precommit-dart-hooks"}, msg: "dart-format: types = {markdown}"},
		{name: "dart-analyze with types", edits: []edit{ed(pcRel, dartAnalyzeHook, dartAnalyzeHook+"        types: [dart]\n")}, rules: []string{"precommit-dart-hooks"}, msg: "dart-analyze: types = {dart}"},
		{name: "dart-format without types passes", edits: []edit{ed(pcRel, "        types: [dart]\n", "")}},
		{name: "non-compliant flutter-test above the compliant one", do: func(t *testing.T, r string) {
			replace(t, r, pcRel, "      - id: flutter-test\n", "      - id: flutter-test\n        name: dup\n        entry: flutter test\n        language: system\n        pass_filenames: false\n        always_run: true\n        stages: [pre-push]\n      - id: flutter-test\n")
		}, rules: []string{"precommit-dart-hooks"}, msg: "flutter-test: entry = \"flutter test\""},
		{name: "dart-format only in a non-local repo", do: func(t *testing.T, r string) {
			replace(t, r, pcRel, dartFormatHook, "")
			appendFile(t, r, pcRel, "\n  - repo: https://example.com/other\n    rev: v1\n    hooks:\n"+dartFormatHook)
		}, rules: []string{"precommit-dart-hooks"}, msg: "no `repo: local` hook \"dart-format\""},
		// vuln-target (shared rule, Flutter values)
		{name: "flutter-vuln hook without vuln target", edits: []edit{ed(mkRel, "vuln:\n\tpre-commit run flutter-vuln --hook-stage manual\n", "")}, rules: []string{"vuln-target"}, msg: `hook "flutter-vuln"`},
		{name: "vuln target without flutter-vuln hook", edits: []edit{ed(pcRel, "      - id: flutter-vuln\n", "")}, rules: []string{"vuln-target"}, msg: "templates/flutter/pre-commit-config.yaml"},
		{name: "vuln recipe runs go-vuln", edits: []edit{ed(mkRel, "run flutter-vuln", "run go-vuln")}, rules: []string{"vuln-target"}, msg: "pre-commit run flutter-vuln --hook-stage manual"},
		// osv-scanner.toml
		{name: "ignore without reason", do: func(t *testing.T, r string) {
			writeFile(t, r, "osv-scanner.toml", "[[IgnoredVulns]]\nid = \"GHSA-x\"\nignoreUntil = 2026-12-01\n")
		}, rules: []string{"osv-ignore-expiry"}, msg: "has no reason"},
		{name: "ignore without id", do: func(t *testing.T, r string) {
			writeFile(t, r, "osv-scanner.toml", "[[IgnoredVulns]]\nreason = \"r\"\nignoreUntil = 2026-12-01\n")
		}, rules: []string{"osv-ignore-expiry"}, msg: "has no id"},
		{name: "ignore 91 days ahead", do: func(t *testing.T, r string) {
			writeFile(t, r, "osv-scanner.toml", "[[IgnoredVulns]]\nid = \"GHSA-x\"\nreason = \"r\"\nignoreUntil = 2027-01-17\n")
		}, rules: []string{"osv-ignore-expiry"}, msg: "set it to 2027-01-16 or earlier"},
		{name: "ignore within 90 days passes", do: func(t *testing.T, r string) {
			writeFile(t, r, "osv-scanner.toml", "[[IgnoredVulns]]\nid = \"GHSA-x\"\nreason = \"r\"\nignoreUntil = 2027-01-16\n")
		}},
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

// The coverage rules have not shipped for Flutter: the fixture has no
// check-coverage hook and passes (TestPassFlutter).
//
// vuln-fix.yml is forbidden: Flutter has no governance vuln-fix workflow, so
// any such file is hand-rolled and fails, whatever it contains.
func TestFlutterProfileForbidsVulnFix(t *testing.T) {
	root := fixture(t, flutterPassFixture)
	writeFile(t, root, vfRel, string(govRead(t, "templates/go-vuln-fix-caller.yml")))
	rep := run(t, root)
	if got := ruleIDs(rep.Violations); !equalStrings(got, []string{ruleVulnFix}) {
		t.Fatalf("rules = %v, want [vuln-fix]\n%s", got, joinMessages(rep.Violations))
	}
	if !strings.Contains(joinMessages(rep.Violations), "flutter profile has no governance vuln-fix workflow") {
		t.Errorf("message does not explain the profile has no vuln-fix workflow:\n%s", joinMessages(rep.Violations))
	}
}

// templates/flutter must pass the Flutter profile as-is.
func TestTemplatesFlutter(t *testing.T) {
	root := fixture(t, flutterPassFixture)
	for tmpl, dst := range map[string]string{
		"templates/flutter/ci.yml":                 ciRel,
		"templates/flutter/dependabot.yml":         depRel,
		"templates/flutter/pre-commit-config.yaml": pcRel,
		"templates/flutter/Makefile":               mkRel,
	} {
		writeFile(t, root, dst, string(govRead(t, tmpl)))
	}
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("templates/flutter fails the Flutter profile:\n%s", joinMessages(rep.Violations))
	}
	// The pass fixture is a byte copy of the pre-commit template, as for docs.
	got, err := os.ReadFile(filepath.Join("testdata", flutterPassFixture, PreCommitConfig))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(govRead(t, "templates/flutter/pre-commit-config.yaml")) {
		t.Errorf("testdata/%s/%s differs from templates/flutter/pre-commit-config.yaml; copy the template over the fixture", flutterPassFixture, PreCommitConfig)
	}
}
