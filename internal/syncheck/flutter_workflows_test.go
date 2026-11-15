package syncheck

import (
	"regexp"
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

// Analyze still reports after a format failure, but never after a failed setup:
// without the SDK or packages it only buries the root cause.
func TestFlutterLintAnalyzeNeedsDeps(t *testing.T) {
	const rel = ".github/workflows/flutter-lint.yml"
	var deps, analyze *yaml.Node
	for _, step := range path(govYAML(t, rel), "jobs", "lint", "steps").Content {
		step = deref(step)
		switch {
		case strings.Contains(scalar(val(step, "run")), "pub get"):
			deps = step
		case strings.Contains(scalar(val(step, "run")), "dart analyze"):
			analyze = step
		}
	}
	if deps == nil || analyze == nil {
		t.Fatalf("%s: want a `pub get` step and a `dart analyze` step", rel)
	}
	id := scalar(val(deps, "id"))
	if id == "" {
		t.Fatalf("%s: the `pub get` step needs an id so Analyze can gate on it", rel)
	}
	cond := normExpr(scalar(val(analyze, "if")))
	if !strings.Contains(cond, "steps."+id+".outcome == 'success'") || !strings.Contains(cond, "!cancelled()") {
		t.Errorf("%s Analyze if = %q; want !cancelled() && steps.%s.outcome == 'success'", rel, cond, id)
	}
}

// flutter-vuln.yml is checked apart from flutter-lint and flutter-test: it needs
// no SDK, so it has no Resolve step and no flutter-action.
func TestFlutterVulnHardRules(t *testing.T) {
	const rel = ".github/workflows/flutter-vuln.yml"
	doc := govYAML(t, rel)
	if p, ok := readPerms(val(doc, "permissions")); !ok || !p.equal(PermsRead) {
		t.Errorf("%s top-level permissions = %s, want %s", rel, describePerms(val(doc, "permissions")), PermsRead)
	}
	if secrets := path(doc, "on", "workflow_call", "secrets"); secrets != nil {
		t.Errorf("%s declares workflow_call secrets; it takes none", rel)
	}
	if path(doc, "on", "workflow_call", "inputs", "projects") != nil {
		t.Errorf("%s has a projects input; it scans the root pubspec.lock only", rel)
	}
	if val(doc, "concurrency") != nil {
		t.Errorf("%s sets concurrency; the caller owns it", rel)
	}
	jobs := val(doc, "jobs")
	if len(keys(jobs)) != 1 || keys(jobs)[0] != "vuln" {
		t.Fatalf("%s jobs = %v, want the single job \"vuln\" (the caller's check name depends on it)", rel, keys(jobs))
	}
	job := val(jobs, "vuln")
	if val(job, "permissions") != nil {
		t.Errorf("%s job vuln sets permissions; the top-level read applies", rel)
	}
	if val(job, "strategy") != nil {
		t.Errorf("%s job vuln has a matrix; it scans one lock", rel)
	}
	if scalar(val(job, "timeout-minutes")) == "" {
		t.Errorf("%s job vuln has no timeout-minutes", rel)
	}
	checkouts := 0
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
}

// flutter-vuln.yml installs the same osv-scanner as python-vuln.yml, policy.go and
// the hook (.osv-scanner-version): a different scanner would disagree with the
// local hook on what is vulnerable.
func TestFlutterVulnOSVDefaults(t *testing.T) {
	const rel = ".github/workflows/flutter-vuln.yml"
	def := func(rel, input string) string {
		return scalar(path(govYAML(t, rel), "on", "workflow_call", "inputs", input, "default"))
	}
	for _, input := range []string{"osv-scanner-version", "osv-scanner-sha256"} {
		got, want := def(rel, input), def(govPyVulnYML, input)
		if got == "" || got != want {
			t.Errorf("flutter-vuln.yml %s default = %q but python-vuln.yml has %q; change both together", input, got, want)
		}
	}
	if got := strings.TrimPrefix(def(rel, "osv-scanner-version"), "v"); got != OSVScannerVersion {
		t.Errorf("flutter-vuln.yml osv-scanner-version default = %s but policy.go OSVScannerVersion = %s", got, OSVScannerVersion)
	}
	pin := strings.TrimPrefix(strings.TrimSpace(string(govRead(t, ".osv-scanner-version"))), "v")
	if got := strings.TrimPrefix(def(rel, "osv-scanner-version"), "v"); got != pin {
		t.Errorf("flutter-vuln.yml osv-scanner-version default = %s but .osv-scanner-version = %s", got, pin)
	}
	if sum := def(rel, "osv-scanner-sha256"); !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(sum) {
		t.Errorf("flutter-vuln.yml osv-scanner-sha256 default = %q, want 64 lowercase hex characters", sum)
	}
}

// The run step scans the root pubspec.lock and fails closed when it is missing.
func TestFlutterVulnScansRootLock(t *testing.T) {
	const rel = ".github/workflows/flutter-vuln.yml"
	var run string
	for _, step := range path(govYAML(t, rel), "jobs", "vuln", "steps").Content {
		step = deref(step)
		if scalar(val(step, "name")) == "Run osv-scanner" {
			run = scalar(val(step, "run"))
		}
	}
	if run == "" {
		t.Fatalf("%s has no `Run osv-scanner` step", rel)
	}
	if !regexp.MustCompile(`if \[ ! -f "\$lock" \]; then\n\s*echo "::error::[^\n]*"\n\s*exit 1\n\s*fi`).MatchString(run) {
		t.Errorf("%s: the missing-lock branch must print ::error:: and exit 1", rel)
	}
	for _, want := range []string{`lock="pubspec.lock"`, "--lockfile", "--config osv-scanner.toml"} {
		if !strings.Contains(run, want) {
			t.Errorf("%s run step lacks %q", rel, want)
		}
	}
}
