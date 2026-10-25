package syncheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedNow pins osv-ignore-expiry's clock: the 90-day limit is 2027-01-16.
var fixedNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	now = func() time.Time { return fixedNow }
	os.Exit(m.Run())
}

const (
	pyPassFixture = "pass-python"
	appPy         = "src/app/pyproject.toml"
	workerPy      = "src/worker/pyproject.toml"
	appOSV        = "src/app/osv-scanner.toml"
	bothProjects  = `'["src/app","src/worker"]'`
)

func TestProfileDetection(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		dirs  []string
		want  *profile
	}{
		{name: "root go.mod", files: []string{"go.mod"}, want: goProfile},
		{name: "go.mod wins over Python files", files: []string{"go.mod", "tools/uv.lock", "pyproject.toml"}, want: goProfile},
		{name: "nested uv.lock", files: []string{"src/app/uv.lock"}, want: pythonProfile},
		{name: "root pyproject.toml only", files: []string{"pyproject.toml"}, want: pythonProfile},
		{name: "nested go.mod is not Go", files: []string{"sub/go.mod"}, want: nil},
		{name: "go.mod directory is not Go", dirs: []string{"go.mod"}, want: nil},
		{name: "uv.lock inside .venv is skipped", files: []string{".venv/lib/uv.lock", "node_modules/x/pyproject.toml"}, want: nil},
		{name: "empty repo", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range tc.files {
				writeFile(t, root, f, "x\n")
			}
			for _, d := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			c := &checker{root: root, files: map[string][]string{}, docs: map[string]*parsed{}}
			if got := c.detectProfile(); got != tc.want {
				t.Fatalf("profile = %v, want %v", profName(got), profName(tc.want))
			}
		})
	}
}

func profName(p *profile) string {
	if p == nil {
		return "(none)"
	}
	return p.name
}

// In a git work tree only tracked files count: an untracked uv.lock or
// poetry.lock is invisible, a tracked one is not.
func TestProfileTrackedFilesInGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init", "-q")
	writeFile(t, root, "src/app/uv.lock", "x\n")
	if p := (&checker{root: root}).detectProfile(); p != nil {
		t.Fatalf("untracked uv.lock: profile = %s, want none", profName(p))
	}
	gitRun("add", "src/app/uv.lock")
	if p := (&checker{root: root}).detectProfile(); p != pythonProfile {
		t.Fatalf("tracked uv.lock: profile = %s, want python", profName(p))
	}
}

func TestNoProfileFixture(t *testing.T) {
	rep := run(t, fixture(t, "no-profile"))
	if len(rep.Violations) != 1 || rep.Violations[0].Rule != "profile" || rep.Violations[0].File != "." {
		t.Fatalf("want exactly one profile violation, got:\n%s", joinMessages(rep.Violations))
	}
	if !strings.Contains(rep.Violations[0].Message, "nothing else was checked") {
		t.Errorf("message = %q", rep.Violations[0].Message)
	}
}

func TestPassPython(t *testing.T) {
	rep := run(t, fixture(t, pyPassFixture))
	if !rep.OK() || len(rep.Exemptions) != 0 {
		t.Fatalf("want clean, got:\n%s", joinMessages(rep.Violations))
	}
}

// The Python-only rules never fire in a Go repo, whatever Python files it carries.
func TestGoProfileIgnoresPythonFiles(t *testing.T) {
	root := fixture(t, "pass-service")
	writeFile(t, root, "tools/uv.lock", "x\n")
	writeFile(t, root, "tools/poetry.lock", "x\n")
	writeFile(t, root, "tools/osv-scanner.toml", "[[IgnoredVulns]]\nid = \"X\"\n")
	writeFile(t, root, "tools/pyproject.toml", "not = [valid\n")
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("want clean, got:\n%s", joinMessages(rep.Violations))
	}
}

func TestPythonDrift(t *testing.T) {
	rm := func(rel string) func(t *testing.T, r string) {
		return func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, rel)); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name  string
		edits []edit
		do    func(t *testing.T, root string)
		rules []string
		msg   string
	}{
		// ci.yml
		{name: "typecheck job missing", do: func(t *testing.T, r string) {
			s := readFile(t, r, ciRel)
			i, j := strings.Index(s, "  typecheck:\n"), strings.Index(s, "  vuln:\n")
			writeFile(t, r, ciRel, s[:i]+s[j:])
		}, rules: []string{"ci-jobs"}, msg: `required job "typecheck"`},
		{name: "go job set is unknown", do: func(t *testing.T, r string) {
			appendFile(t, r, ciRel, "\n  coverage:\n    if: github.event.pull_request.draft == false\n    runs-on: ubuntu-latest\n    timeout-minutes: 5\n    steps:\n      - run: echo hi\n")
		}, rules: []string{"ci-jobs"}, msg: `unknown job "coverage"; allowed: lint, typecheck, vuln, test, gitleaks, docker, automerge`},
		{name: "docker job without Dockerfiles", do: func(t *testing.T, r string) {
			rm("src/app/Dockerfile")(t, r)
			rm("src/worker/Dockerfile")(t, r)
		}, rules: []string{"ci-jobs"}, msg: "no uv project has a Dockerfile"},
		{name: "Dockerfile without docker job", do: func(t *testing.T, r string) {
			s := readFile(t, r, ciRel)
			i, j := strings.Index(s, "  docker:\n"), strings.Index(s, "  gitleaks:\n")
			writeFile(t, r, ciRel, s[:i]+s[j:])
		}, rules: []string{"ci-jobs"}, msg: "src/app, src/worker have a Dockerfile"},
		{name: "lint calls go-lint", edits: []edit{ed(ciRel, "python-lint.yml@python-lint/v1", "go-lint.yml@go-lint/v1")}, rules: []string{"ci-job-ref"}, msg: "python-lint.yml@python-lint/v1"},
		{name: "typecheck has permissions", edits: []edit{ed(ciRel, "  typecheck:\n", "  typecheck:\n    permissions:\n      contents: read\n")}, rules: []string{"ci-job-permissions"}, msg: `job "typecheck"`},
		{name: "missing lint workflow-sync", edits: []edit{ed(ciRel, "      workflow-sync: true\n", "")}, rules: []string{"ci-lint-sync"}, msg: "(absent)"},
		{name: "test omits a project", edits: []edit{ed(ciRel, "python-test/v1\n    with:\n      projects: "+bothProjects, "python-test/v1\n    with:\n      projects: '[\"src/app\"]'")}, rules: []string{"ci-projects"}, msg: "missing: [src/worker]"},
		{name: "lint lists an unknown project", edits: []edit{ed(ciRel, "python-lint/v1\n    with:\n      projects: "+bothProjects, "python-lint/v1\n    with:\n      projects: '[\"src/app\",\"./src/worker/\",\"src/nope\"]'")}, rules: []string{"ci-projects"}, msg: "unknown: [src/nope]"},
		{name: "docker omits an image", edits: []edit{ed(ciRel, "python-docker/v1\n    with:\n      projects: "+bothProjects, "python-docker/v1\n    with:\n      projects: '[\"src/worker\"]'")}, rules: []string{"ci-projects"}, msg: "every uv project with a Dockerfile (missing: [src/app]"},
		{name: "projects not JSON", edits: []edit{ed(ciRel, "python-vuln/v1\n    with:\n      projects: "+bothProjects, "python-vuln/v1\n    with:\n      projects: src/app")}, rules: []string{"ci-projects"}, msg: "is not a JSON array"},
		{name: "projects as YAML list", edits: []edit{ed(ciRel, "python-vuln/v1\n    with:\n      projects: "+bothProjects, "python-vuln/v1\n    with:\n      projects: [src/app, src/worker]")}, rules: []string{"ci-projects"}, msg: "not a string"},
		{name: "projects absent", edits: []edit{ed(ciRel, "python-typecheck/v1\n    with:\n      projects: "+bothProjects+"\n", "python-typecheck/v1\n")}, rules: []string{"ci-projects"}, msg: "is absent"},
		{name: "vuln-fix.yml from the template passes", do: func(t *testing.T, r string) {
			writeFile(t, r, vfRel, pyVulnFixTemplate(t))
		}},
		{name: "vuln-fix.yml calls go-vuln-fix", do: func(t *testing.T, r string) {
			writeVulnFix(t, r, "python-vuln-fix.yml@python-vuln-fix/v1", "go-vuln-fix.yml@go-vuln-fix/v1")
		}, rules: []string{"vuln-fix"}, msg: "python-vuln-fix.yml@python-vuln-fix/v1"},
		{name: "vuln-fix.yml concurrency group go-vuln-fix", do: func(t *testing.T, r string) {
			writeVulnFix(t, r, "group: python-vuln-fix", "group: go-vuln-fix")
		}, rules: []string{"vuln-fix"}, msg: "want {group: python-vuln-fix, cancel-in-progress: false}"},
		{name: "vuln-fix.yml projects omit a project", do: func(t *testing.T, r string) {
			writeVulnFix(t, r, bothProjects, `'["src/app"]'`)
		}, rules: []string{"vuln-fix"}, msg: "missing: [src/worker]"},
		{name: "vuln-fix.yml projects list an unknown project", do: func(t *testing.T, r string) {
			writeVulnFix(t, r, bothProjects, `'["src/app","src/worker","src/nope"]'`)
		}, rules: []string{"vuln-fix"}, msg: "unknown: [src/nope]"},
		{name: "vuln-fix.yml projects absent", do: func(t *testing.T, r string) {
			writeVulnFix(t, r, "      projects: "+bothProjects+"\n", "")
		}, rules: []string{"vuln-fix"}, msg: "with.projects is absent"},
		{name: "vuln-fix.yml projects not JSON", do: func(t *testing.T, r string) {
			writeVulnFix(t, r, bothProjects, "src/app")
		}, rules: []string{"vuln-fix"}, msg: "is not a JSON array"},
		{name: "vuln-fix.yml secrets inherit", do: func(t *testing.T, r string) {
			writeVulnFix(t, r, "    secrets:\n      APP_CLIENT_ID: ${{ secrets.APP_CLIENT_ID }}\n      APP_PRIVATE_KEY: ${{ secrets.APP_PRIVATE_KEY }}\n", "    secrets: inherit\n")
		}, rules: []string{"vuln-fix"}, msg: "least privilege"},
		// dependabot.yml
		{name: "uv entry dropped", edits: []edit{ed(depRel, "package-ecosystem: uv", "package-ecosystem: pip")}, rules: []string{"dependabot"}, msg: `no "uv" updates entry covers /src/app, /src/worker`},
		{name: "uv entry misses a project", edits: []edit{ed(depRel, "  - package-ecosystem: uv\n    directories:\n      - /src/app\n      - /src/worker\n", "  - package-ecosystem: uv\n    directories:\n      - /src/app\n")}, rules: []string{"dependabot"}, msg: `no "uv" updates entry covers /src/worker`},
		{name: "uv entry by glob passes", edits: []edit{ed(depRel, "  - package-ecosystem: uv\n    directories:\n      - /src/app\n      - /src/worker\n", "  - package-ecosystem: uv\n    directories:\n      - /src/*\n")}},
		{name: "docker entry dropped", edits: []edit{ed(depRel, "package-ecosystem: docker", "package-ecosystem: npm")}, rules: []string{"dependabot"}, msg: `no "docker" updates entry`},
		{name: "docker entry bumps the uv image", edits: []edit{ed(depRel, "      - dependency-name: ghcr.io/astral-sh/uv\n", "")}, rules: []string{"dependabot"}, msg: "does not ignore ghcr.io/astral-sh/uv"},
		{name: "github-actions dropped", edits: []edit{ed(depRel, "package-ecosystem: github-actions", "package-ecosystem: gitsubmodule")}, rules: []string{"dependabot"}, msg: `"github-actions"`},
		{name: "dependabot.yml missing", do: rm(depRel), rules: []string{"dependabot"}, msg: "templates/python/dependabot.yml"},
		// repo files
		{name: "uv-version drift", do: func(t *testing.T, r string) { writeFile(t, r, ".uv-version", "0.12.17\n") }, rules: []string{"uv-version"}, msg: "want 0.12.18"},
		{name: "uv-version missing", do: rm(".uv-version"), rules: []string{"uv-version"}, msg: "file missing"},
		{name: "ruff-version drift", do: func(t *testing.T, r string) { writeFile(t, r, ".ruff-version", "0.16.0\n") }, rules: []string{"ruff-version"}, msg: "want 0.17.0"},
		{name: "ruff hook rev drift", edits: []edit{ed(pcRel, "rev: v0.17.0", "rev: v0.16.0")}, rules: []string{"ruff-version"}, msg: `rev "v0.16.0"; want v0.17.0`},
		{name: "ruff hook absent", edits: []edit{ed(pcRel, "https://github.com/astral-sh/ruff-pre-commit", "https://github.com/acme/ruff-fork")}, rules: []string{"ruff-version"}, msg: "no `repo: https://github.com/astral-sh/ruff-pre-commit` entry"},
		{name: "python-version missing", do: rm("src/worker/.python-version"), rules: []string{"python-version"}, msg: "uv project src/worker"},
		{name: "python-version garbage", do: func(t *testing.T, r string) { writeFile(t, r, "src/app/.python-version", "latest\n") }, rules: []string{"python-version"}, msg: "X.Y"},
		{name: "poetry.lock present", do: func(t *testing.T, r string) { writeFile(t, r, "src/app/poetry.lock", "x\n") }, rules: []string{"poetry-lock"}, msg: "delete it"},
		// osv-scanner.toml
		{name: "ignore without ignoreUntil", edits: []edit{ed(appOSV, "ignoreUntil = 2027-01-13\n", "")}, rules: []string{"osv-ignore-expiry"}, msg: `"PYSEC-2026-139" has no ignoreUntil`},
		{name: "ignore without reason", edits: []edit{ed(appOSV, "reason = \"Not reachable: only a local trusted checkpoint is loaded. No fixed release exists.\"\n", "reason = \"  \"\n")}, rules: []string{"osv-ignore-expiry"}, msg: "has no reason"},
		{name: "ignore without id", edits: []edit{ed(appOSV, "id = \"GHSA-qfhq-4f3w-5fph\"\n", "")}, rules: []string{"osv-ignore-expiry"}, msg: "has no id"},
		{name: "ignore 91 days ahead", edits: []edit{ed(appOSV, "ignoreUntil = 2027-01-16T00:00:00Z", "ignoreUntil = 2027-01-17")}, rules: []string{"osv-ignore-expiry"}, msg: "set it to 2027-01-16 or earlier"},
		{name: "ignoreUntil not a date", edits: []edit{ed(appOSV, "ignoreUntil = 2027-01-13", `ignoreUntil = "soon"`)}, rules: []string{"osv-ignore-expiry"}, msg: "is not a date"},
		{name: "ignoreUntil as a date string passes", edits: []edit{ed(appOSV, "ignoreUntil = 2027-01-13", `ignoreUntil = "2027-01-13"`)}},
		{name: "expired ignore is osv-scanner's finding", edits: []edit{ed(appOSV, "ignoreUntil = 2027-01-13", "ignoreUntil = 2026-01-01")}},
		{name: "inline IgnoredVulns", do: func(t *testing.T, r string) {
			writeFile(t, r, appOSV, "IgnoredVulns = [{ id = \"X\", reason = \"r\" }]\n")
		}, rules: []string{"osv-ignore-expiry"}, msg: "inline array"},
		{name: "package override ignoring vulns needs expiry", do: func(t *testing.T, r string) {
			appendFile(t, r, appOSV, "\n[[PackageOverrides]]\nname = \"pillow\"\n[PackageOverrides.vulnerability]\nignore = true\n")
		}, rules: []string{"osv-ignore-expiry"}, msg: `[[PackageOverrides]] "pillow" (ignores vulnerabilities) has no effectiveUntil`},
		{name: "root osv-scanner.toml is checked", do: func(t *testing.T, r string) {
			writeFile(t, r, "osv-scanner.toml", "[[IgnoredVulns]]\nid = \"X\"\nreason = \"r\"\n")
		}, rules: []string{"osv-ignore-expiry"}, msg: "osv-scanner.toml"},
		{name: "osv-scanner.toml unparsable", do: func(t *testing.T, r string) { appendFile(t, r, appOSV, "\n[[IgnoredVulns]\n") }, rules: []string{"toml-parse"}, msg: "invalid TOML"},
		// pyproject.toml coverage floor
		{name: "fail_under zero", edits: []edit{ed(appPy, "fail_under = 94", "fail_under = 0")}, rules: []string{"coverage-floor"}, msg: "zero is not a gate"},
		{name: "fail_under above 100", edits: []edit{ed(workerPy, "= 73.5", "= 100.5")}, rules: []string{"coverage-floor"}, msg: "(0, 100]"},
		{name: "fail_under missing", edits: []edit{ed(appPy, "fail_under = 94\n", "")}, rules: []string{"coverage-floor"}, msg: "missing"},
		{name: "fail_under string", edits: []edit{ed(appPy, "fail_under = 94", `fail_under = "94"`)}, rules: []string{"coverage-floor"}, msg: `must be a number, found string "94"`},
		{name: "fail_under bool", edits: []edit{ed(appPy, "fail_under = 94", "fail_under = true")}, rules: []string{"coverage-floor"}, msg: "found boolean"},
		{name: "fail_under in an inline table passes", edits: []edit{ed(workerPy, "tool.coverage.report.fail_under = 73.5", "tool.coverage = { report = { fail_under = 73 } }")}},
		{name: "untested project is not floor-checked", edits: []edit{
			ed(ciRel, "python-test/v1\n    with:\n      projects: "+bothProjects, "python-test/v1\n    with:\n      projects: '[\"src/app\"]'"),
			ed(workerPy, "tool.coverage.report.fail_under = 73.5\n", ""),
		}, rules: []string{"ci-projects"}, msg: "missing: [src/worker]"},
		{name: "pyproject unparsable", edits: []edit{ed(appPy, "fail_under = 94", "fail_under = 94 94")}, rules: []string{"toml-parse"}, msg: "src/app/pyproject.toml"},
		// pre-commit and Makefile (shared rules, Python values)
		{name: "py-vuln hook without vuln target", edits: []edit{ed(mkRel, "vuln:\n\tpre-commit run py-vuln --hook-stage manual\n", "")}, rules: []string{"vuln-target"}, msg: `hook "py-vuln"`},
		{name: "vuln target without py-vuln hook", edits: []edit{ed(pcRel, "      - id: py-vuln\n", "")}, rules: []string{"vuln-target"}, msg: "templates/python/pre-commit-config.yaml"},
		{name: "vuln recipe runs go-vuln", edits: []edit{ed(mkRel, "run py-vuln", "run go-vuln")}, rules: []string{"vuln-target"}, msg: "pre-commit run py-vuln --hook-stage manual"},
		{name: "pytest hook at pre-push", do: func(t *testing.T, r string) {
			appendFile(t, r, pcRel, localHook("unit", "uv run pytest", "[pre-push]"))
		}, rules: []string{"precommit-no-test-hook"}, msg: "uv run pytest"},
		{name: "check-coverage without always_run", edits: []edit{ed(pcRel, "        always_run: true\n", "")}, rules: []string{"precommit-coverage-hook"}, msg: "no .py files"},
		{name: "check-coverage target missing", edits: []edit{ed(mkRel, "check-coverage:\n", "coverage:\n")}, rules: []string{"make-coverage-pin"}, msg: "no `check-coverage` target"},
		{name: "workflow-sync hook missing", edits: []edit{ed(pcRel, "      - id: workflow-sync\n", "")}, rules: []string{"precommit-governance-repo"}, msg: "workflow-sync"},
		// Go-only rules stay silent in a Python repo.
		{name: "no golangci or testcoverage files needed", do: func(t *testing.T, r string) {
			if _, err := os.Stat(filepath.Join(r, ".golangci-version")); err == nil {
				t.Fatal("fixture must not carry Go files")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, pyPassFixture)
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

// Markers work in TOML files too (`#` comments).
func TestPythonExemptionInTOML(t *testing.T) {
	root := fixture(t, pyPassFixture)
	replace(t, root, appPy, "fail_under = 94", "# sync-exempt: coverage-floor tests land in #12\nfail_under = 0")
	rep := run(t, root)
	if !rep.OK() || len(rep.Exemptions) != 1 || rep.Exemptions[0].Rule != "coverage-floor" {
		t.Fatalf("violations:\n%s\nexemptions: %v", joinMessages(rep.Violations), rep.Exemptions)
	}
}

func pyVulnFixTemplate(t *testing.T) string {
	t.Helper()
	return string(govRead(t, "templates/python/vuln-fix.yml"))
}

// writeVulnFix installs the Python vuln-fix template into the repo with one edit.
func writeVulnFix(t *testing.T, root, old, new string) {
	t.Helper()
	tmpl := pyVulnFixTemplate(t)
	if !strings.Contains(tmpl, old) {
		t.Fatalf("templates/python/vuln-fix.yml does not contain %q", old)
	}
	writeFile(t, root, vfRel, strings.Replace(tmpl, old, new, 1))
}

// templates/python must pass the Python profile as-is.
func TestTemplatesPython(t *testing.T) {
	root := fixture(t, pyPassFixture)
	for tmpl, dst := range map[string]string{
		"templates/python/ci.yml":                 ciRel,
		"templates/python/dependabot.yml":         depRel,
		"templates/python/pre-commit-config.yaml": pcRel,
		"templates/python/Makefile":               mkRel,
		"templates/python/vuln-fix.yml":           vfRel,
	} {
		writeFile(t, root, dst, string(govRead(t, tmpl)))
	}
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("templates/python fails the Python profile:\n%s", joinMessages(rep.Violations))
	}
}

func TestDependabotDirCovered(t *testing.T) {
	cases := []struct {
		pattern, dir string
		want         bool
	}{
		{"/src/app", "/src/app", true},
		{"/src/*", "/src/app", true},
		{"/src/*", "/src/app/sub", false},
		{"/**", "/src/app/sub", true},
		{"/src/**", "/lib/x", false},
		{"/", "/", true},
		{"/src/app", "/src/application", false},
	}
	for _, tc := range cases {
		if got := dirCovered(tc.pattern, tc.dir); got != tc.want {
			t.Errorf("dirCovered(%q, %q) = %v, want %v", tc.pattern, tc.dir, got, tc.want)
		}
	}
}
