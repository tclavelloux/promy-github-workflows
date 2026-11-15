package syncheck

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// flutterWorkflows are the reusable workflows added with the Flutter profile.
var flutterWorkflows = []string{"flutter-lint", "flutter-test"}

// resolveStep returns the `Resolve Flutter version` step of a workflow's only job.
func resolveStep(t *testing.T, rel string, doc *yaml.Node, job string) *yaml.Node {
	t.Helper()
	for _, step := range val(val(val(doc, "jobs"), job), "steps").Content {
		step = deref(step)
		if scalar(val(step, "name")) == "Resolve Flutter version" {
			return step
		}
	}
	t.Fatalf("%s has no `Resolve Flutter version` step", rel)
	return nil
}

// The hard rules for every Flutter workflow: SHA-pinned actions, no persisted
// credentials, read-only token, inputs only through env, timeouts, no secrets.
func TestFlutterWorkflowsHardRules(t *testing.T) {
	var resolveRuns []string
	for _, name := range flutterWorkflows {
		rel := ".github/workflows/" + name + ".yml"
		doc := govYAML(t, rel)

		if p, ok := readPerms(val(doc, "permissions")); !ok || !p.equal(PermsRead) {
			t.Errorf("%s top-level permissions = %s, want %s", rel, describePerms(val(doc, "permissions")), PermsRead)
		}
		if secrets := path(doc, "on", "workflow_call", "secrets"); secrets != nil {
			t.Errorf("%s declares workflow_call secrets; Flutter workflows take none", rel)
		}
		jobs := val(doc, "jobs")
		wantJob := strings.TrimPrefix(name, "flutter-")
		if len(keys(jobs)) != 1 || keys(jobs)[0] != wantJob {
			t.Errorf("%s jobs = %v, want the single job %q (the caller's check name depends on it)", rel, keys(jobs), wantJob)
		}
		resolveRuns = append(resolveRuns, scalar(val(resolveStep(t, rel, doc, wantJob), "run")))
		for _, jid := range keys(jobs) {
			job := val(jobs, jid)
			if val(job, "permissions") != nil {
				t.Errorf("%s job %s sets permissions; the top-level read applies", rel, jid)
			}
			if scalar(val(job, "timeout-minutes")) == "" {
				t.Errorf("%s job %s has no timeout-minutes", rel, jid)
			}
			checkouts, flutterActions := 0, 0
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
				switch {
				case strings.HasPrefix(uses, "actions/checkout@"):
					checkouts++
					if !isBool(path(step, "with", "persist-credentials"), false) {
						t.Errorf("%s checkout without `persist-credentials: false`", rel)
					}
				case strings.HasPrefix(uses, "subosito/flutter-action@"):
					flutterActions++
					if got := scalar(path(step, "with", "flutter-version")); got != "${{ steps.flutter.outputs.version }}" {
						t.Errorf("%s flutter-action flutter-version = %q, want the Resolve step output", rel, got)
					}
				}
			}
			if checkouts != 1 {
				t.Errorf("%s job %s has %d checkouts, want 1", rel, jid, checkouts)
			}
			if flutterActions != 1 {
				t.Errorf("%s job %s has %d flutter-action steps, want 1", rel, jid, flutterActions)
			}
		}
	}
	if len(resolveRuns) == 2 && resolveRuns[0] != resolveRuns[1] {
		t.Errorf("the `Resolve Flutter version` run: bodies of flutter-lint and flutter-test differ; keep them byte-identical")
	}
}

func TestFlutterTestUploadsLcov(t *testing.T) {
	const rel = ".github/workflows/flutter-test.yml"
	doc := govYAML(t, rel)
	if !isBool(path(doc, "on", "workflow_call", "inputs", "timeout-minutes", "required"), true) {
		t.Errorf("%s input timeout-minutes must be required", rel)
	}
	found := false
	for _, step := range val(val(val(doc, "jobs"), "test"), "steps").Content {
		step = deref(step)
		if !strings.HasPrefix(scalar(val(step, "uses")), "actions/upload-artifact@") {
			continue
		}
		found = true
		if scalar(path(step, "with", "name")) != "coverage-lcov" || scalar(path(step, "with", "path")) != "coverage/lcov.info" {
			t.Errorf("%s upload must be artifact coverage-lcov from coverage/lcov.info", rel)
		}
		if scalar(path(step, "with", "if-no-files-found")) != "error" {
			t.Errorf("%s upload must set if-no-files-found: error", rel)
		}
	}
	if !found {
		t.Errorf("%s has no upload-artifact step", rel)
	}
	if !strings.Contains(string(govRead(t, rel)), "flutter test --coverage") {
		t.Errorf("%s must run `flutter test --coverage`", rel)
	}
}

func TestFlutterLintRunsWorkflowSync(t *testing.T) {
	const rel = ".github/workflows/flutter-lint.yml"
	doc := govYAML(t, rel)
	if in := path(doc, "on", "workflow_call", "inputs", "workflow-sync"); in == nil || scalar(val(in, "type")) != "boolean" || !isBool(val(in, "default"), false) {
		t.Errorf("%s needs a boolean input workflow-sync defaulting to false", rel)
	}
	if !strings.Contains(string(govRead(t, rel)), "pre-commit run workflow-sync") {
		t.Errorf("%s must run `pre-commit run workflow-sync`", rel)
	}
}
