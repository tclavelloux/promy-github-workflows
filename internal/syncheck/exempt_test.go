package syncheck

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

const badLint = "go-lint.yml@main"

func TestExemptions(t *testing.T) {
	t.Run("trailing marker honoured", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "go-lint.yml@go-lint/v1", badLint+" # sync-exempt: governance-ref pinned during incident 12")
		rep := run(t, root)
		if !rep.OK() || len(rep.Exemptions) != 1 {
			t.Fatalf("violations:\n%s\nexemptions: %v", joinMessages(rep.Violations), rep.Exemptions)
		}
		e := rep.Exemptions[0]
		if e.Rule != "governance-ref" || e.Reason != "pinned during incident 12" {
			t.Fatalf("exemption = %+v", e)
		}
		var out bytes.Buffer
		rep.Render(&out)
		want := "workflow-sync: EXEMPT .github/workflows/ci.yml:" + strconv.Itoa(e.Line) + " [governance-ref] pinned during incident 12\n"
		if !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "workflow-sync: OK — 0 violation(s), 1 exemption(s)") {
			t.Fatalf("render:\n%s", out.String())
		}
	})

	t.Run("comment block above honoured", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "    uses: tclavelloux/promy-github-workflows/.github/workflows/go-lint.yml@go-lint/v1",
			"    # why: incident 12\n    # sync-exempt: governance-ref pinned to main\n    # more context\n    uses: tclavelloux/promy-github-workflows/.github/workflows/"+badLint)
		rep := run(t, root)
		if !rep.OK() || len(rep.Exemptions) != 1 {
			t.Fatalf("violations:\n%s", joinMessages(rep.Violations))
		}
	})

	t.Run("blank line breaks the block", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "    uses: tclavelloux/promy-github-workflows/.github/workflows/go-lint.yml@go-lint/v1",
			"    # sync-exempt: governance-ref too far away\n\n    uses: tclavelloux/promy-github-workflows/.github/workflows/"+badLint)
		got := ruleIDs(run(t, root).Violations)
		if !equalStrings(got, []string{"governance-ref", "stale-exemption"}) {
			t.Fatalf("rules = %v", got)
		}
	})

	t.Run("file marker honoured", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "go-lint.yml@go-lint/v1", badLint)
		replace(t, root, ciRel, "go-vuln.yml@go-vuln/v1", "go-vuln.yml@main")
		replace(t, root, ciRel, "name: CI\n", "# sync-exempt-file: governance-ref bootstrapping on main\nname: CI\n")
		rep := run(t, root)
		if !rep.OK() || len(rep.Exemptions) != 2 {
			t.Fatalf("violations:\n%s\nexemptions: %d", joinMessages(rep.Violations), len(rep.Exemptions))
		}
	})

	t.Run("marker only covers its own rule", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "go-lint.yml@go-lint/v1", badLint+" # sync-exempt: sha-pin wrong rule")
		got := ruleIDs(run(t, root).Violations)
		if !equalStrings(got, []string{"governance-ref", "stale-exemption"}) {
			t.Fatalf("rules = %v", got)
		}
	})

	t.Run("missing reason", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "go-lint.yml@go-lint/v1", badLint+" # sync-exempt: governance-ref")
		rep := run(t, root)
		if got := ruleIDs(rep.Violations); !equalStrings(got, []string{"governance-ref", "bad-exemption"}) {
			t.Fatalf("rules = %v", got)
		}
		if !strings.Contains(joinMessages(rep.Violations), "has no reason") {
			t.Fatalf("messages:\n%s", joinMessages(rep.Violations))
		}
	})

	t.Run("unknown rule", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "name: CI\n", "name: CI # sync-exempt: no-such-rule because\n")
		rep := run(t, root)
		if got := ruleIDs(rep.Violations); !equalStrings(got, []string{"bad-exemption"}) {
			t.Fatalf("rules = %v", got)
		}
		if !strings.Contains(joinMessages(rep.Violations), `unknown rule "no-such-rule"`) {
			t.Fatalf("messages:\n%s", joinMessages(rep.Violations))
		}
	})

	t.Run("stale marker", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "name: CI\n", "name: CI # sync-exempt: sha-pin nothing to exempt\n")
		rep := run(t, root)
		if got := ruleIDs(rep.Violations); !equalStrings(got, []string{"stale-exemption"}) {
			t.Fatalf("rules = %v", got)
		}
	})

	t.Run("engine rules cannot be exempted", func(t *testing.T) {
		root := fixture(t, "pass-service")
		replace(t, root, ciRel, "name: CI\n", "# sync-exempt-file: stale-exemption hide it\nname: CI\n")
		got := ruleIDs(run(t, root).Violations)
		if !equalStrings(got, []string{"bad-exemption"}) {
			t.Fatalf("rules = %v", got)
		}
	})

	t.Run("works in files with line findings from other rules", func(t *testing.T) {
		root := fixture(t, "event-bus-registry")
		appendFile(t, root, ".github/workflows/registry.yaml", "# sync-exempt-file: workflow-permissions read-only repo\n")
		rep := run(t, root)
		if got := ruleIDs(rep.Violations); !equalStrings(got, []string{"persist-credentials", "sha-pin", "unpinned-tool"}) {
			t.Fatalf("rules = %v", got)
		}
		if len(rep.Exemptions) != 1 {
			t.Fatalf("exemptions = %d", len(rep.Exemptions))
		}
	})
}

func TestRender(t *testing.T) {
	root := fixture(t, "pass-service")
	replace(t, root, ciRel, "cancel-in-progress: true", "cancel-in-progress: false")
	rep := run(t, root)

	t.Run("plain", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "")
		var out bytes.Buffer
		rep.Render(&out)
		s := out.String()
		if strings.Contains(s, "::error") || !strings.Contains(s, ".github/workflows/ci.yml:10: [ci-concurrency] ") ||
			!strings.HasSuffix(s, "workflow-sync: FAIL — 1 violation(s), 0 exemption(s)\n") {
			t.Fatalf("render:\n%s", s)
		}
	})

	t.Run("github actions", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "true")
		var out bytes.Buffer
		rep.Render(&out)
		if !strings.Contains(out.String(), "::error file=.github/workflows/ci.yml,line=10,title=workflow-sync [ci-concurrency]::") {
			t.Fatalf("render:\n%s", out.String())
		}
	})

	t.Run("github actions whole-file finding omits line", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "true")
		rep := Report{Violations: []Finding{{File: "a.yml", Rule: "pr-title", Message: "50% gone\nline2"}}}
		var out bytes.Buffer
		rep.Render(&out)
		if !strings.Contains(out.String(), "::error file=a.yml,title=workflow-sync [pr-title]::50%25 gone%0Aline2\n") {
			t.Fatalf("render:\n%s", out.String())
		}
	})
}
