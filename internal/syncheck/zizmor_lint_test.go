package syncheck

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// zizmorLintWorkflows are the reusable lint workflows that offer the opt-in zizmor audit.
var zizmorLintWorkflows = []string{"go-lint", "python-lint", "python-ci", "flutter-lint", "docs-lint"}

const (
	zizmorMarker    = "zizmor install"
	zizmorAuditName = "Audit workflows (zizmor)"
	zizmorAuditRun  = "zizmor --persona=regular .github/workflows/"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func zizmorWorkflowRel(name string) string { return ".github/workflows/" + name + ".yml" }

// namedStep returns the step called name in the only job of the workflow, or nil.
func namedStep(doc *yaml.Node, name string) *yaml.Node {
	jobs := val(doc, "jobs")
	for _, jid := range keys(jobs) {
		for _, step := range val(val(jobs, jid), "steps").Content {
			step = deref(step)
			if scalar(val(step, "name")) == name {
				return step
			}
		}
	}
	return nil
}

// zizmorInstallDrift names the first way block differs from want, or "" when equal.
func zizmorInstallDrift(rel, block, want string) string {
	if block == want {
		return ""
	}
	g, w := strings.Split(block, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) && i < len(w); i++ {
		if g[i] != w[i] {
			return rel + ": line " + strconv.Itoa(i+1) + " of the `" + zizmorMarker + "` block is `" + g[i] + "`, want `" + w[i] + "`"
		}
	}
	return rel + ": `" + zizmorMarker + "` block has " + strconv.Itoa(len(g)) + " lines, want " + strconv.Itoa(len(w))
}

// The lint workflows default to the pin in .zizmor-version / .zizmor-sha256: a
// reusable workflow cannot read those files, so the defaults carry the copy.
func TestZizmorLintInputsMatchPin(t *testing.T) {
	pinVersion := strings.TrimSpace(string(govRead(t, ".zizmor-version")))
	pinSum := strings.TrimSpace(string(govRead(t, ".zizmor-sha256")))
	if !sha256Hex.MatchString(pinSum) {
		t.Fatalf(".zizmor-sha256 = %q, want 64 lowercase hex characters", pinSum)
	}
	for _, name := range zizmorLintWorkflows {
		rel := zizmorWorkflowRel(name)
		doc := govYAML(t, rel)
		in := func(input string) *yaml.Node { return path(doc, "on", "workflow_call", "inputs", input) }

		if n := in("zizmor"); n == nil || scalar(val(n, "type")) != "boolean" || !isBool(val(n, "default"), false) {
			t.Errorf("%s needs a boolean input zizmor defaulting to false", rel)
		}
		if n := in("zizmor-version"); n == nil {
			t.Errorf("%s has no input zizmor-version", rel)
		} else if got := strings.TrimPrefix(scalar(val(n, "default")), "v"); got != pinVersion {
			t.Errorf("%s zizmor-version default = %q, want %q (.zizmor-version); bump both in the same PR", rel, got, pinVersion)
		}
		if n := in("zizmor-sha256"); n == nil {
			t.Errorf("%s has no input zizmor-sha256", rel)
		} else {
			got := scalar(val(n, "default"))
			if got != pinSum {
				t.Errorf("%s zizmor-sha256 default = %q, want %q (.zizmor-sha256); bump both in the same PR", rel, got, pinSum)
			}
			if !sha256Hex.MatchString(got) {
				t.Errorf("%s zizmor-sha256 default = %q, want 64 lowercase hex characters", rel, got)
			}
		}
	}
}

// Governance CI executes the same install block callers get from the lint
// workflows, so each PR here proves what callers run.
func TestZizmorInstallBlockIdentical(t *testing.T) {
	rels := []string{ciRel}
	for _, name := range zizmorLintWorkflows {
		rels = append(rels, zizmorWorkflowRel(name))
	}
	want := markedBlock(t, string(govRead(t, rels[0])), zizmorMarker, rels[0])
	for _, rel := range rels[1:] {
		got := markedBlock(t, string(govRead(t, rel)), zizmorMarker, rel)
		if msg := zizmorInstallDrift(rel, got, want); msg != "" {
			t.Errorf("%s differs from the block in %s; %s", rel, rels[0], msg)
		}
	}
	for _, line := range []string{`version="${RAW_VERSION#v}"`, `"$actual" != "$EXPECTED_SHA256"`} {
		if !strings.Contains(want, line) {
			t.Errorf("%s install block lacks %s", rels[0], line)
		}
	}
}

func TestZizmorInstallBlockDriftDetected(t *testing.T) {
	block := markedBlock(t, string(govRead(t, ciRel)), zizmorMarker, ciRel)
	if msg := zizmorInstallDrift("x.yml", strings.Replace(block, "install -m 0755", "install -m 0777", 1), block); msg == "" {
		t.Error("a changed line in the install block must be reported")
	}
	if msg := zizmorInstallDrift("x.yml", block+"\nextra", block); msg == "" {
		t.Error("an extra line in the install block must be reported")
	}
}

func TestZizmorLintSteps(t *testing.T) {
	for _, name := range zizmorLintWorkflows {
		rel := zizmorWorkflowRel(name)
		doc := govYAML(t, rel)

		install := namedStep(doc, "Install zizmor")
		if install == nil {
			t.Errorf("%s has no `Install zizmor` step", rel)
		} else {
			if !strings.Contains(scalar(val(install, "if")), "inputs.zizmor") {
				t.Errorf("%s `Install zizmor` if = %q, want it gated on inputs.zizmor", rel, scalar(val(install, "if")))
			}
			for env, input := range map[string]string{"RAW_VERSION": "inputs.zizmor-version", "EXPECTED_SHA256": "inputs.zizmor-sha256"} {
				if got := scalar(path(install, "env", env)); !strings.Contains(got, input) {
					t.Errorf("%s `Install zizmor` env %s = %q, want it to read %s", rel, env, got, input)
				}
			}
		}

		audit := namedStep(doc, zizmorAuditName)
		if audit == nil {
			t.Errorf("%s has no `%s` step", rel, zizmorAuditName)
			continue
		}
		if !strings.Contains(scalar(val(audit, "if")), "inputs.zizmor") {
			t.Errorf("%s `%s` if = %q, want it gated on inputs.zizmor", rel, zizmorAuditName, scalar(val(audit, "if")))
		}
		if got := strings.TrimSpace(scalar(val(audit, "run"))); got != zizmorAuditRun {
			t.Errorf("%s `%s` run = %q, want %q", rel, zizmorAuditName, got, zizmorAuditRun)
		}
		if install != nil && install.Line >= audit.Line {
			t.Errorf("%s installs zizmor after the audit step", rel)
		}
	}
}
