package syncheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// stubGH answers the few `gh` calls the freshness, merge and rebase steps make,
// from STUB_* variables, and logs every call to STUB_LOG.
const stubGH = `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_LOG"
answer() { if [ "$1" = FAIL ]; then echo "stub: API error" >&2; exit 1; fi; printf '%b\n' "$1"; }
case "$*" in
  "pr merge"*) if [ "$STUB_MERGE" = ok ]; then echo merged; else printf '%b\n' "$STUB_MERGE"; exit 1; fi ;;
  *"/rules/branches/"*) answer "$STUB_RULES" ;;
  *"/compare/"*) answer "$STUB_BEHIND" ;;
  *"-X POST"*"/comments"*) if [ "$STUB_COMMENT" = FAIL ]; then echo '{"message":"Resource not accessible by integration"}'; exit 1; fi; printf '123\thttps://github.test/pr/1#issuecomment-123\n' ;;
  *"/reactions"*) answer "$STUB_ACKS" ;;
  *) echo "stub: unexpected gh $*" >&2; exit 99 ;;
esac
`

func automergeSteps(t *testing.T) map[string]*yaml.Node {
	t.Helper()
	steps := path(govYAML(t, automergeGateYML), "jobs", "automerge", "steps")
	if !isSeq(steps) {
		t.Fatalf("%s: jobs.automerge.steps is not a list", automergeGateYML)
	}
	byID := map[string]*yaml.Node{}
	for _, s := range steps.Content {
		if id := scalar(val(s, "id")); id != "" {
			byID[id] = s
		}
	}
	return byID
}

// runStep runs a step's `run:` the way GitHub does (bash -eo pipefail) with a
// stub gh on PATH. Returns the GITHUB_OUTPUT contents, stdout+stderr and the gh log.
func runStep(t *testing.T, step *yaml.Node, env map[string]string) (outputs, logs, calls string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stubGH), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(script, []byte(scalar(val(step, "run"))), 0o644); err != nil {
		t.Fatal(err)
	}
	out, log := filepath.Join(dir, "out"), filepath.Join(dir, "gh.log")
	for _, f := range []string{out, log} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GITHUB_OUTPUT="+out, "STUB_LOG="+log)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("step exited non-zero (the job must stay green): %v\n%s", err, b)
	}
	o, _ := os.ReadFile(out)
	l, _ := os.ReadFile(log)
	return string(o), string(b), string(l)
}

// The App is optional and only its two credentials cross into the workflow.
// The token is minted with the fleet pin and pull-requests write only.
func TestAutomergeAppToken(t *testing.T) {
	doc := govYAML(t, automergeGateYML)
	secrets := path(doc, "on", "workflow_call", "secrets")
	if got := keys(secrets); !equalStrings(got, appSecrets) {
		t.Errorf("%s declares workflow_call secrets %v, want exactly %v", automergeGateYML, got, appSecrets)
	}
	for _, name := range keys(secrets) {
		if !isBool(path(secrets, name, "required"), false) {
			t.Errorf("%s secret %s must be `required: false`: the App is optional", automergeGateYML, name)
		}
	}

	step := automergeSteps(t)["app-token"]
	if step == nil {
		t.Fatalf("%s has no step with id app-token", automergeGateYML)
	}
	var fleetPin string
	for _, s := range path(govYAML(t, ".github/workflows/go-vuln-fix.yml"), "jobs", "fix", "steps").Content {
		if scalar(val(s, "id")) == "app-token" {
			fleetPin = scalar(val(s, "uses"))
		}
	}
	if fleetPin == "" || scalar(val(step, "uses")) != fleetPin {
		t.Errorf("app-token uses %q, want the go-vuln-fix pin %q", scalar(val(step, "uses")), fleetPin)
	}
	with := val(step, "with")
	want := map[string]string{
		"client-id":                "${{ secrets.APP_CLIENT_ID }}",
		"private-key":              "${{ secrets.APP_PRIVATE_KEY }}",
		"repositories":             "${{ github.event.repository.name }}",
		"permission-pull-requests": "write",
	}
	if got := keys(with); !sameSet(setOf(got...), setOf(sortedKeys(want)...)) {
		t.Errorf("app-token with keys = %v, want exactly %v (least privilege)", got, sortedKeys(want))
	}
	for k, v := range want {
		if got := scalar(val(with, k)); got != v {
			t.Errorf("app-token with.%s = %q, want %q", k, got, v)
		}
	}
}

// Every value reaches the shell through env, never through `${{ }}` in `run:`.
func TestAutomergeRunBlocksUseEnv(t *testing.T) {
	for id, s := range automergeSteps(t) {
		if strings.Contains(scalar(val(s, "run")), "${{") {
			t.Errorf("%s step %q interpolates an expression in run:; pass it through env", automergeGateYML, id)
		}
	}
}

func TestAutomergeFreshness(t *testing.T) {
	step := automergeSteps(t)["fresh"]
	base := map[string]string{"REPO": "o/r", "BASE_REF": "main", "HEAD_SHA": "abc"}
	cases := []struct {
		name, rules, behind string
		stale               bool
		log                 string
	}{
		{"strict and behind", "true", "2", true, "branch behind base by 2 commit(s)"},
		{"strict and current", "true", "0", false, "strict up-to-date rule active: 1"},
		{"not strict and behind", "false", "3", false, "strict up-to-date rule active: 0"},
		{"no ruleset and behind", "", "3", false, "strict up-to-date rule active: 0"},
		{"rules lookup fails counts as strict", "FAIL", "1", true, "branch behind base by 1 commit(s)"},
		{"compare fails merges anyway", "true", "FAIL", false, "Freshness unknown"},
		{"compare garbage merges anyway", "true", "null", false, "Freshness unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"STUB_RULES": tc.rules, "STUB_BEHIND": tc.behind}
			for k, v := range base {
				env[k] = v
			}
			out, logs, _ := runStep(t, step, env)
			if got := strings.Contains(out, "stale=true"); got != tc.stale {
				t.Errorf("stale = %v, want %v\noutputs:\n%s", got, tc.stale, out)
			}
			if !strings.Contains(logs+out, tc.log) {
				t.Errorf("want %q in the log or outputs\nlog:\n%s\noutputs:\n%s", tc.log, logs, out)
			}
		})
	}
}

func TestAutomergeMergeRefusal(t *testing.T) {
	step := automergeSteps(t)["merge"]
	base := map[string]string{"REPO": "o/r", "BASE_REF": "main", "HEAD_SHA": "abc", "PR_NUMBER": "7"}
	cases := []struct {
		name, merge, behind string
		want                []string
	}{
		{"merged", "ok", "0", []string{"merged=true"}},
		{"sibling merged first", "GraphQL: Repository rule violations found\n6 of 6 required status checks are expected.", "1",
			[]string{"merged=false", "stale=true", "reason=merge refused and branch now behind base by 1 commit(s) (GraphQL: Repository rule violations found 6 of 6"}},
		{"other refusal stays green", "head commit moved 100%", "0", []string{"merged=false", "stale=false", "reason=merge refused (head commit moved 100)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"STUB_MERGE": tc.merge, "STUB_BEHIND": tc.behind}
			for k, v := range base {
				env[k] = v
			}
			out, _, _ := runStep(t, step, env)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("want %q in outputs:\n%s", w, out)
				}
			}
			if n := strings.Count(out, "\n"); tc.merge != "ok" && n != 3 {
				t.Errorf("outputs have %d lines, want 3 (a multi-line reason would corrupt GITHUB_OUTPUT):\n%s", n, out)
			}
		})
	}
}

func TestAutomergeRebaseRequest(t *testing.T) {
	step := automergeSteps(t)["rebase"]
	cases := []struct {
		name, actor, hasApp, comment, acks, result, log string
		commented                                       bool // a comment was attempted
	}{
		{"acknowledged", "dependabot[bot]", "true", "", "1", "rebase=acknowledged", "Dependabot acknowledged", true},
		{"not acknowledged", "dependabot[bot]", "true", "", "0", "rebase=unacknowledged", "Rebase not acknowledged", true},
		{"reactions lookup fails", "dependabot[bot]", "true", "", "FAIL", "rebase=unacknowledged", "Rebase not acknowledged", true},
		{"comment refused stays green", "dependabot[bot]", "true", "FAIL", "0", "rebase=comment-failed", "Rebase not requested", true},
		{"no app secrets", "dependabot[bot]", "false", "", "0", "rebase=no-app", "comment `@dependabot rebase` by hand", false},
		{"fleet-bot PR", "promy-fleet-bot[bot]", "true", "", "0", "rebase=not-applicable", "merge by hand", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, logs, calls := runStep(t, step, map[string]string{
				"ACTOR": tc.actor, "HAS_APP": tc.hasApp, "STUB_ACKS": tc.acks, "REPO": "o/r", "PR_NUMBER": "7",
				"STUB_COMMENT": tc.comment,
				"GH_TOKEN":     "app-token", "APP_SLUG": "promy-fleet-bot", "ACK_ATTEMPTS": "2", "ACK_SECONDS": "0",
			})
			if !strings.Contains(out, tc.result) {
				t.Errorf("want %q in outputs:\n%s", tc.result, out)
			}
			if !strings.Contains(logs, tc.log) {
				t.Errorf("want %q in the log:\n%s", tc.log, logs)
			}
			if got := strings.Contains(calls, "-X POST repos/o/r/issues/7/comments -f body=@dependabot rebase"); got != tc.commented {
				t.Errorf("commented = %v, want %v; gh calls:\n%s", got, tc.commented, calls)
			}
		})
	}
}
