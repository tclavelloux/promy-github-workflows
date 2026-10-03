package syncheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPassFixtures(t *testing.T) {
	for _, name := range []string{"pass-service", "pass-library", "pass-rollout"} {
		t.Run(name, func(t *testing.T) {
			rep := run(t, fixture(t, name))
			if !rep.OK() || len(rep.Exemptions) != 0 {
				t.Fatalf("want clean, got:\n%s", joinMessages(rep.Violations))
			}
		})
	}
}

func TestEventBusRegistry(t *testing.T) {
	rep := run(t, fixture(t, "event-bus-registry"))
	want := []string{"persist-credentials", "sha-pin", "unpinned-tool", "workflow-permissions"}
	if got := ruleIDs(rep.Violations); !equalStrings(got, want) {
		t.Fatalf("rules = %v, want %v\n%s", got, want, joinMessages(rep.Violations))
	}
	for _, f := range rep.Violations {
		if f.File != ".github/workflows/registry.yaml" {
			t.Errorf("unexpected file %s", f.File)
		}
	}
}

type edit struct{ file, old, new string }

func ed(file, old, new string) edit { return edit{file, old, new} }

const (
	lintSyncBlock = "    with:\n      workflow-sync: true\n"
	automergeNeed = "needs: [lint, vuln, docker, test, coverage, gitleaks]"
	checkoutSHA   = "3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\n"
	extraJob      = "\n  extra:\n    if: github.event.pull_request.draft == false\n    runs-on: ubuntu-latest\n    timeout-minutes: 5\n    steps:\n      - run: echo hi\n"
	minimalWF     = "name: x\non: push\npermissions:\n  contents: read\njobs:\n  j:\n    runs-on: ubuntu-latest\n    timeout-minutes: 5\n    steps:\n"
)

func TestDrift(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		edits []edit
		do    func(t *testing.T, root string)
		rules []string
		msg   string
	}{
		{name: "push trigger", edits: []edit{ed(ciRel, "on:\n  pull_request:", "on:\n  push:\n    branches: [main]\n  pull_request:")}, rules: []string{"ci-trigger"}, msg: "want exactly `pull_request`"},
		{name: "missing ready_for_review", edits: []edit{ed(ciRel, "synchronize, ready_for_review, reopened", "synchronize, reopened")}, rules: []string{"ci-trigger"}, msg: "ready_for_review"},
		{name: "cancel-in-progress false", edits: []edit{ed(ciRel, "cancel-in-progress: true", "cancel-in-progress: false")}, rules: []string{"ci-concurrency"}, msg: "cancel-in-progress"},
		{name: "top-level pull-requests write", edits: []edit{ed(ciRel, "permissions:\n  contents: read\n\njobs:", "permissions:\n  contents: read\n  pull-requests: write\n\njobs:")}, rules: []string{"ci-permissions"}, msg: "want exactly {contents: read}"},
		{name: "missing draft skip", edits: []edit{ed(ciRel, "  vuln:\n    if: github.event.pull_request.draft == false\n", "  vuln:\n")}, rules: []string{"ci-draft-skip"}, msg: `job "vuln"`},
		{name: "coverage pull-requests read", edits: []edit{ed(ciRel, "      pull-requests: write\n    uses", "      pull-requests: read\n    uses")}, rules: []string{"ci-job-permissions"}, msg: "want exactly {contents: read, pull-requests: write}"},
		{name: "coverage needs", edits: []edit{ed(ciRel, "    needs: test\n", "")}, rules: []string{"ci-job-permissions"}, msg: "needs"},
		{name: "gitleaks no permissions", edits: []edit{ed(ciRel, "    permissions:\n      contents: read\n      pull-requests: read\n    steps:", "    steps:")}, rules: []string{"ci-job-permissions"}, msg: `job "gitleaks" permissions = (absent)`},
		{name: "gitleaks fetch-depth", edits: []edit{ed(ciRel, "          fetch-depth: 0\n", "")}, rules: []string{"ci-job-permissions"}, msg: "fetch-depth"},
		{name: "lint has permissions", edits: []edit{ed(ciRel, "  lint:\n", "  lint:\n    permissions:\n      contents: write\n")}, rules: []string{"ci-job-permissions"}, msg: `job "lint"`},
		{name: "retention 7", edits: []edit{ed(ciRel, "retention-days: 1", "retention-days: 7")}, rules: []string{"ci-test-artifact"}, msg: "retention-days"},
		{name: "if-no-files-found warn", edits: []edit{ed(ciRel, "if-no-files-found: error", "if-no-files-found: warn")}, rules: []string{"ci-test-artifact"}, msg: "if-no-files-found"},
		{name: "lint bare v1", edits: []edit{ed(ciRel, "go-lint.yml@go-lint/v1", "go-lint.yml@v1")}, rules: []string{"governance-ref"}, msg: `want "go-lint/v1"`},
		{name: "coverage v1", edits: []edit{ed(ciRel, "go-coverage/v2", "go-coverage/v1")}, rules: []string{"governance-ref"}, msg: `want "go-coverage/v2"`},
		{name: "lint main", edits: []edit{ed(ciRel, "go-lint.yml@go-lint/v1", "go-lint.yml@main")}, rules: []string{"governance-ref"}, msg: `"main"`},
		{name: "immutable governance tag", edits: []edit{ed(ciRel, "go-vuln/v1", "go-vuln/v1.2.0")}, rules: []string{"governance-ref"}, msg: "moving major"},
		{name: "unknown governance workflow", edits: []edit{ed(ciRel, "go-vuln.yml@go-vuln/v1", "go-nope.yml@go-nope/v1")}, rules: []string{"governance-ref", "ci-job-ref"}, msg: "unknown governance workflow"},
		{name: "job uses third-party workflow", edits: []edit{ed(ciRel, "tclavelloux/promy-github-workflows/.github/workflows/go-lint.yml@go-lint/v1", "acme/x/.github/workflows/l.yml@v1")}, rules: []string{"sha-pin", "ci-job-ref"}, msg: "40-char"},
		{name: "checkout tag", edits: []edit{ed(ciRel, "actions/checkout@"+checkoutSHA, "actions/checkout@v4\n")}, rules: []string{"sha-pin"}, msg: `pinned to "v4"`},
		{name: "sha without version comment", edits: []edit{ed(ciRel, checkoutSHA, "3d3c42e5aac5ba805825da76410c181273ba90b1\n")}, rules: []string{"sha-pin"}, msg: "no trailing"},
		{name: "uppercase sha", edits: []edit{ed(ciRel, "3d3c42e5aac5ba805825da76410c181273ba90b1", "3D3C42E5AAC5BA805825DA76410C181273BA90B1")}, rules: []string{"sha-pin"}, msg: "lowercase"},
		{name: "checkout persist-credentials", edits: []edit{ed(ciRel, checkoutSHA+"        with:\n          persist-credentials: false\n", checkoutSHA)}, rules: []string{"persist-credentials"}, msg: "persist-credentials: false"},
		{name: "workflow without permissions", edits: []edit{ed(rpRel, "permissions:\n  contents: write\n  pull-requests: write\n\n", "")}, rules: []string{"workflow-permissions"}, msg: "no top-level permissions"},
		{name: "pull_request_target", do: func(t *testing.T, r string) {
			writeFile(t, r, ".github/workflows/x.yml", strings.Replace(minimalWF, "on: push", "on: [push, pull_request_target]", 1)+"      - run: echo hi\n")
		}, rules: []string{"dangerous-trigger"}, msg: `"pull_request_target"`},
		{name: "workflow_run", do: func(t *testing.T, r string) {
			writeFile(t, r, ".github/workflows/x.yml", strings.Replace(minimalWF, "on: push", "on:\n  workflow_run:\n    workflows: [CI]", 1)+"      - run: echo hi\n")
		}, rules: []string{"dangerous-trigger"}, msg: `"workflow_run"`},
		{name: "wget releases/latest", do: func(t *testing.T, r string) {
			writeFile(t, r, ".github/workflows/x.yml", minimalWF+"      - run: wget https://github.com/o/r/releases/latest/download/t\n")
		}, rules: []string{"unpinned-tool"}, msg: "floating version"},
		{name: "go install @latest", do: func(t *testing.T, r string) {
			writeFile(t, r, ".github/workflows/x.yml", minimalWF+"      - run: |\n          echo a\n          go install example.com/t@latest\n")
		}, rules: []string{"unpinned-tool"}, msg: "@latest"},
		{name: "docker job without Dockerfile", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, "Dockerfile")) }, rules: []string{"ci-jobs"}, msg: "no Dockerfile"},
		{name: "Dockerfile without docker job", base: "pass-library", do: func(t *testing.T, r string) { writeFile(t, r, "Dockerfile", "") }, rules: []string{"ci-jobs"}, msg: "Dockerfile exists"},
		{name: "unknown job", do: func(t *testing.T, r string) { appendFile(t, r, ciRel, extraJob) }, rules: []string{"ci-jobs"}, msg: `unknown job "extra"`},
		{name: "required job missing", do: func(t *testing.T, r string) {
			s := readFile(t, r, ciRel)
			writeFile(t, r, ciRel, s[:strings.Index(s, "  gitleaks:")])
		}, rules: []string{"ci-jobs"}, msg: `required job "gitleaks"`},
		{name: "ci.yml missing", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, ciRel)) }, rules: []string{"ci-jobs"}, msg: "file missing"},
		{name: "test without timeout", edits: []edit{ed(ciRel, "    timeout-minutes: 15\n", "")}, rules: []string{"ci-timeout"}, msg: `job "test"`},
		{name: "automerge not last", base: "pass-rollout", do: func(t *testing.T, r string) {
			s := readFile(t, r, ciRel)
			i := strings.Index(s, "  # List EVERY")
			block, s := s[i:], s[:i]
			j := strings.Index(s, "  test:\n")
			writeFile(t, r, ciRel, s[:j]+block+"\n"+s[j:])
		}, rules: []string{"ci-automerge"}, msg: "not the last job"},
		{name: "automerge needs missing gitleaks", base: "pass-rollout", edits: []edit{ed(ciRel, automergeNeed, "needs: [lint, vuln, docker, test, coverage]")}, rules: []string{"ci-automerge"}, msg: "missing: [gitleaks]"},
		{name: "automerge wrong permissions", base: "pass-rollout", edits: []edit{ed(ciRel, "statuses: read", "statuses: write")}, rules: []string{"ci-automerge"}, msg: `job "automerge" permissions`},
		{name: "automerge wrong if", base: "pass-rollout", edits: []edit{ed(ciRel, "'dependabot[bot]'", "'renovate[bot]'")}, rules: []string{"ci-automerge"}, msg: "renovate"},
		{name: "automerge if as expression", base: "pass-rollout", edits: []edit{ed(ciRel, "if: github.event.pull_request.user.login == 'dependabot[bot]'", "if: ${{  github.event.pull_request.user.login   ==   'dependabot[bot]' }}")}},
		{name: "missing lint workflow-sync", edits: []edit{ed(ciRel, lintSyncBlock, "")}, rules: []string{"ci-lint-sync"}, msg: "(absent)"},
		{name: "lint workflow-sync string", edits: []edit{ed(ciRel, "workflow-sync: true", `workflow-sync: "true"`)}, rules: []string{"ci-lint-sync"}, msg: "boolean true"},
		{name: "pr-title missing edited", edits: []edit{ed(prRel, "opened, edited, reopened", "opened, reopened")}, rules: []string{"pr-title"}, msg: "edited"},
		{name: "pr-title wrong job permissions", edits: []edit{ed(prRel, "      pull-requests: read", "      pull-requests: write")}, rules: []string{"pr-title"}, msg: `job "pr-title" permissions`},
		{name: "pr-title.yml missing", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, prRel)) }, rules: []string{"pr-title"}, msg: "file missing"},
		{name: "release-please.yml missing", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, rpRel)) }, rules: []string{"release-please"}, msg: "file missing"},
		{name: "release-please extra trigger", edits: []edit{ed(rpRel, "on:\n  push:", "on:\n  workflow_dispatch:\n  push:")}, rules: []string{"release-please"}, msg: "want exactly"},
		{name: "dependabot missing gomod", edits: []edit{ed(depRel, "package-ecosystem: gomod", "package-ecosystem: pip")}, rules: []string{"dependabot"}, msg: `"gomod"`},
		{name: "dependabot.yml missing", do: func(t *testing.T, r string) { os.Remove(filepath.Join(r, depRel)) }, rules: []string{"dependabot"}, msg: "file missing"},
		{name: "vuln-fix wrong permissions", base: "pass-rollout", edits: []edit{ed(vfRel, "issues: write", "issues: read")}, rules: []string{"vuln-fix"}, msg: `job "fix" permissions`},
		{name: "vuln-fix cancel", base: "pass-rollout", edits: []edit{ed(vfRel, "cancel-in-progress: false", "cancel-in-progress: true")}, rules: []string{"vuln-fix"}, msg: "cancel-in-progress"},
		{name: "vuln-fix push trigger", base: "pass-rollout", edits: []edit{ed(vfRel, "  workflow_dispatch:", "  workflow_dispatch:\n  push:")}, rules: []string{"vuln-fix"}, msg: "schedule"},
		{name: "malformed yaml", do: func(t *testing.T, r string) { appendFile(t, r, ciRel, "\n  bad: [unclosed\n") }, rules: []string{"yaml-parse"}, msg: "invalid YAML"},
		// A governed file that exists but holds no mapping drops its gate silently unless flagged.
		{name: "pr-title.yml empty", do: func(t *testing.T, r string) { writeFile(t, r, prRel, "") }, rules: []string{"yaml-parse"}, msg: "empty or not a YAML mapping"},
		{name: "dependabot.yml comment only", do: func(t *testing.T, r string) { writeFile(t, r, depRel, "# nothing here\n") }, rules: []string{"yaml-parse"}, msg: "empty or not a YAML mapping"},
		{name: "ci.yml empty", do: func(t *testing.T, r string) { writeFile(t, r, ciRel, "") }, rules: []string{"yaml-parse"}, msg: "empty or not a YAML mapping"},
		{name: "release-please.yml scalar root", do: func(t *testing.T, r string) { writeFile(t, r, rpRel, "just a string\n") }, rules: []string{"yaml-parse"}, msg: "empty or not a YAML mapping"},
		{name: "pre-commit config empty", do: func(t *testing.T, r string) { writeFile(t, r, ".pre-commit-config.yaml", "") }, rules: []string{"yaml-parse"}, msg: "empty or not a YAML mapping"},
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

func appendFile(t *testing.T, root, rel, s string) {
	t.Helper()
	writeFile(t, root, rel, readFile(t, root, rel)+s)
}

func TestMissingRoot(t *testing.T) {
	if _, err := Run(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("want error for missing root")
	}
}

// yaml.v3 Nodes keep `on` as the plain string key (no YAML 1.1 bool coercion).
func TestOnKeyIsString(t *testing.T) {
	root, err := parseYAML([]byte("on:\n  push:\n    branches: [main]\nyes: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	k, v := get(root, "on")
	if k == nil || k.Tag != "!!str" || !isMap(v) {
		t.Fatalf("on key = %+v", k)
	}
	if got := onNames(v); len(got) != 1 || got[0] != "push" {
		t.Fatalf("onNames = %v", got)
	}
}

func TestRulesRegistered(t *testing.T) {
	for _, id := range []string{"sha-pin", "bad-exemption", "stale-exemption", "yaml-parse", "ci-automerge"} {
		if !rules[id] {
			t.Errorf("rule %q not registered", id)
		}
	}
}

func TestDuplicateKey(t *testing.T) {
	root := fixture(t, "pass-service")
	appendFile(t, root, ciRel, "\npermissions:\n  contents: read\n")
	rep := run(t, root)
	if got := ruleIDs(rep.Violations); !equalStrings(got, []string{"yaml-parse"}) || !strings.Contains(joinMessages(rep.Violations), "duplicate key") {
		t.Fatalf("got:\n%s", joinMessages(rep.Violations))
	}
}
