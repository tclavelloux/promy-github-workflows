package syncheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	pcRel = ".pre-commit-config.yaml"
	mkRel = "Makefile"
	gvRel = ".golangci-version"
	tcRel = ".testcoverage.yml"

	govRepoEntry = "  - repo: https://github.com/tclavelloux/promy-github-workflows.git\n    rev: hooks/v1.2.0\n    hooks:\n      - id: workflow-sync\n"
	vulnTarget   = "vuln:\n\tpre-commit run go-vuln --hook-stage manual\n"
)

func localHook(id, entry, stages string) string {
	s := "      - id: " + id + "\n        name: " + id + "\n        entry: " + entry + "\n        language: system\n        pass_filenames: false\n"
	if stages != "" {
		s += "        stages: " + stages + "\n"
	}
	return s
}

func TestSyncRulesDrift(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		edits []edit
		do    func(t *testing.T, root string)
		rules []string
		msg   string
	}{
		{name: "install types missing pre-push", edits: []edit{ed(pcRel, "[pre-commit, commit-msg, pre-push]", "[pre-commit, commit-msg]")}, rules: []string{"precommit-install-types"}, msg: "{commit-msg, pre-commit}"},
		{name: "install types absent", edits: []edit{ed(pcRel, "default_install_hook_types: [pre-commit, commit-msg, pre-push]\n", "")}, rules: []string{"precommit-install-types"}, msg: "absent"},
		{name: "governance rev hooks/v1", edits: []edit{ed(pcRel, "rev: hooks/v1.2.0", "rev: hooks/v1")}, rules: []string{"precommit-governance-repo"}, msg: "resolves a moving tag"},
		{name: "governance rev main", edits: []edit{ed(pcRel, "rev: hooks/v1.2.0", "rev: main")}, rules: []string{"precommit-governance-repo", "precommit-rev"}, msg: `"main"`},
		{name: "branch guard missing", edits: []edit{ed(pcRel, "      - id: no-direct-commit-to-main\n", "")}, rules: []string{"precommit-governance-repo"}, msg: "no-direct-commit-to-main"},
		{name: "workflow-sync missing", edits: []edit{ed(pcRel, "      - id: workflow-sync\n", "")}, rules: []string{"precommit-governance-repo"}, msg: "workflow-sync"},
		{name: "governance repo twice", do: func(t *testing.T, r string) { appendFile(t, r, pcRel, "\n"+govRepoEntry) }, rules: []string{"precommit-governance-repo"}, msg: "listed 2 times"},
		{name: "governance repo absent", edits: []edit{ed(pcRel, "https://github.com/tclavelloux/promy-github-workflows", "https://github.com/acme/other")}, rules: []string{"precommit-governance-repo"}, msg: "no `repo:"},
		{name: "third-party rev master", edits: []edit{ed(pcRel, "rev: v4.6.0", "rev: master")}, rules: []string{"precommit-rev"}, msg: "floats"},
		{name: "check-coverage without always_run", edits: []edit{ed(pcRel, "        always_run: true\n", "")}, rules: []string{"precommit-coverage-hook"}, msg: "no .go files would skip the coverage gate"},
		{name: "check-coverage stages pre-commit", edits: []edit{ed(pcRel, "        stages: [pre-push]", "        stages: [pre-commit]")}, rules: []string{"precommit-coverage-hook"}, msg: "{pre-commit}"},
		{name: "check-coverage wrong entry", edits: []edit{ed(pcRel, "entry: make check-coverage", "entry: make test")}, rules: []string{"precommit-coverage-hook"}, msg: "make check-coverage"},
		{name: "check-coverage missing", edits: []edit{ed(pcRel, "- id: check-coverage", "- id: cover")}, rules: []string{"precommit-coverage-hook"}, msg: "no `repo: local` hook"},
		{name: "extra test hook at pre-push", do: func(t *testing.T, r string) { appendFile(t, r, pcRel, localHook("test", "make test", "[pre-push]")) }, rules: []string{"precommit-no-test-hook"}, msg: "duplicates the suite"},
		{name: "pre-push hook running go test", do: func(t *testing.T, r string) { appendFile(t, r, pcRel, localHook("unit", "go test ./...", "")) }, rules: []string{"precommit-no-test-hook"}, msg: "go test ./..."},
		{name: "go test hook at pre-commit only is fine", do: func(t *testing.T, r string) {
			appendFile(t, r, pcRel, localHook("unit", "go test ./...", "[pre-commit]"))
		}},
		{name: "conventional missing revert", edits: []edit{ed(pcRel, "          - revert\n", "")}, rules: []string{"precommit-commit-msg"}, msg: "args"},
		{name: "conventional without commit-msg stage", edits: []edit{ed(pcRel, "        stages: [commit-msg]\n", "")}, rules: []string{"precommit-commit-msg"}, msg: "want {commit-msg}"},
		{name: "conventional absent", edits: []edit{ed(pcRel, "- id: conventional-pre-commit", "- id: other")}, rules: []string{"precommit-commit-msg"}, msg: "no \"conventional-pre-commit\" hook"},
		{name: "go-vuln hook without target", base: "pass-rollout", edits: []edit{ed(mkRel, vulnTarget, "")}, rules: []string{"vuln-target"}, msg: "no `vuln:` target"},
		{name: "vuln target without hook", do: func(t *testing.T, r string) { appendFile(t, r, mkRel, "\n"+vulnTarget) }, rules: []string{"vuln-target"}, msg: `no "go-vuln" hook`},
		{name: "vuln recipe wrong", base: "pass-rollout", edits: []edit{ed(mkRel, "--hook-stage manual", "--hook-stage pre-push")}, rules: []string{"vuln-target"}, msg: "exactly one line"},
		{name: "setup with git config", edits: []edit{ed(mkRel, "pre-commit install --install-hooks", "git config core.hooksPath .githooks")}, rules: []string{"make-setup", "make-hookspath"}, msg: "git config core.hooksPath .githooks"},
		{name: "setup two lines", edits: []edit{ed(mkRel, "setup:\n\tpre-commit install --install-hooks\n", "setup:\n\tpre-commit install --install-hooks\n\t@echo done\n")}, rules: []string{"make-setup"}, msg: "exactly one line"},
		{name: "setup target missing", edits: []edit{ed(mkRel, "setup:", "setup-x:")}, rules: []string{"make-setup"}, msg: "no `setup` target"},
		{name: "hooksPath elsewhere", do: func(t *testing.T, r string) {
			appendFile(t, r, mkRel, "\nhooks:\n\tgit config core.hooksPath .githooks\n")
		}, rules: []string{"make-hookspath"}, msg: "mutually exclusive"},
		{name: "go-test-coverage @latest", edits: []edit{ed(mkRel, "go-test-coverage/v2@v2.19.0", "go-test-coverage/v2@latest")}, rules: []string{"make-coverage-pin", "unpinned-tool"}, msg: "@latest"},
		{name: "go-test-coverage @v2.18.0", edits: []edit{ed(mkRel, "go-test-coverage/v2@v2.19.0", "go-test-coverage/v2@v2.18.0")}, rules: []string{"make-coverage-pin"}, msg: "go-coverage.yml runs v2.19.0"},
		{name: "install target missing", edits: []edit{ed(mkRel, "install-go-test-coverage:", "install-cov:")}, rules: []string{"make-coverage-pin"}, msg: "no `install-go-test-coverage` target"},
		{name: "check-coverage target missing", edits: []edit{ed(mkRel, "check-coverage: install-go-test-coverage", "check-cov: install-go-test-coverage")}, rules: []string{"make-coverage-pin"}, msg: "no `check-coverage` target"},
		{name: "go install @latest in another target", do: func(t *testing.T, r string) {
			appendFile(t, r, mkRel, "\ntools:\n\tgo install example.com/foo@latest\n")
		}, rules: []string{"unpinned-tool"}, msg: "floating version"},
		{name: "tracked .githooks", do: func(t *testing.T, r string) { writeFile(t, r, ".githooks/pre-commit", "#!/bin/sh\n") }, rules: []string{"no-githooks"}, msg: "mutually exclusive"},
		{name: "golangci-version 2.12.0", do: func(t *testing.T, r string) { writeFile(t, r, gvRel, "2.12.0\n") }, rules: []string{"golangci-version"}, msg: "2.13.2"},
		{name: "golangci-version v prefix ok", do: func(t *testing.T, r string) { writeFile(t, r, gvRel, "v2.13.2\n") }},
		{name: "golangci-version missing", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, gvRel)) }, rules: []string{"golangci-version"}, msg: "file missing"},
		{name: "testcoverage header lacks go-coverage.yml", edits: []edit{ed(tcRel, "go-coverage.yml", "go-cov.yml")}, rules: []string{"testcoverage"}, msg: `"go-coverage.yml"`},
		{name: "testcoverage header lacks check-coverage", do: func(t *testing.T, r string) {
			writeFile(t, r, tcRel, strings.ReplaceAll(readFile(t, r, tcRel), "check-coverage", "check-cov"))
		}, rules: []string{"testcoverage"}, msg: `"check-coverage"`},
		{name: "testcoverage missing", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, tcRel)) }, rules: []string{"testcoverage"}, msg: "file missing"},
		{name: "Makefile missing", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, mkRel)) }, rules: []string{"make-setup"}, msg: "file missing"},
		{name: "pre-commit config missing", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, pcRel)) }, rules: []string{"precommit-install-types"}, msg: "file missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base
			if base == "" {
				base = "pass-service"
			}
			root := fixture(t, base)
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

func TestMakefileExemptionHonouredAndPrinted(t *testing.T) {
	root := fixture(t, "pass-service")
	appendFile(t, root, mkRel, "\nhooks:\n\t# sync-exempt: make-hookspath legacy bootstrap, removed in Q4\n\tgit config core.hooksPath .githooks\n")
	// A marker directly above the offending line (comment block) is honoured.
	s := readFile(t, root, mkRel)
	s = strings.Replace(s, "\t# sync-exempt: make-hookspath legacy bootstrap, removed in Q4\n\tgit config", "# sync-exempt: make-hookspath legacy bootstrap, removed in Q4\n\tgit config", 1)
	writeFile(t, root, mkRel, s)

	rep := run(t, root)
	if !rep.OK() || len(rep.Exemptions) != 1 {
		t.Fatalf("want 0 violations and 1 exemption, got:\n%s\nexemptions=%v", joinMessages(rep.Violations), rep.Exemptions)
	}
	var buf bytes.Buffer
	rep.Render(&buf)
	want := "workflow-sync: EXEMPT Makefile:"
	if !strings.Contains(buf.String(), want) || !strings.Contains(buf.String(), "[make-hookspath] legacy bootstrap, removed in Q4") {
		t.Fatalf("exemption not printed:\n%s", buf.String())
	}
}

func TestParseMakefile(t *testing.T) {
	mk := parseMakefile(strings.Split(".PHONY: a\nVAR := x\nVAR2 ?= y\na b: dep\n\t@echo \\\n  cont\n\n# note\n\techo two\nc:\n\tx\n", "\n"))
	if mk.targets["VAR"] != nil || mk.targets[".PHONY"] != nil || mk.targets["VAR2"] != nil {
		t.Fatalf("assignments/.PHONY parsed as targets: %v", mk.targets)
	}
	a, b := mk.targets["a"], mk.targets["b"]
	if a == nil || b == nil || a.line != 4 || len(a.recipe) != 3 {
		t.Fatalf("a = %+v", a)
	}
	if a.recipe[1].line != 6 || a.recipe[2].line != 9 || mk.targets["c"].recipe[0].line != 11 {
		t.Fatalf("recipe lines: %+v / %+v", a.recipe, mk.targets["c"].recipe)
	}
}
