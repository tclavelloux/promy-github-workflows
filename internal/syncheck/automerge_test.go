package syncheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	automergeGateScript = "scripts/automerge-gate.sh"
	automergeGateYML    = ".github/workflows/dependabot-automerge.yml"
	patchMinor          = "version-update:semver-patch version-update:semver-minor"
)

// A reusable workflow cannot read its own files at the caller's pinned ref, so the
// gate is inlined. This keeps the inlined copy byte-identical to the tested script.
func TestAutomergeGateInlinedBodyMatchesScript(t *testing.T) {
	const marker = "automerge-gate body"
	want := markedBlock(t, string(govRead(t, automergeGateScript)), marker, automergeGateScript)
	got := markedBlock(t, string(govRead(t, automergeGateYML)), marker, automergeGateYML)
	if got != want {
		t.Errorf("%s inlines a different `%s` block than %s; regenerate the workflow block from the script", automergeGateYML, marker, automergeGateScript)
	}
}

// The reusable workflow keeps Go callers unchanged: new inputs default to the
// old behaviour, and reach the shell only through env.
func TestAutomergeInputsDefaults(t *testing.T) {
	in := path(govYAML(t, automergeGateYML), "on", "workflow_call", "inputs")
	if got := scalar(path(in, "ecosystems", "default")); got != "go_modules" {
		t.Errorf("%s ecosystems default = %q, want go_modules (Go callers must not change)", automergeGateYML, got)
	}
	if n := path(in, "exclude-dependencies", "default"); n == nil || scalar(n) != "" {
		t.Errorf("%s exclude-dependencies default = %q, want empty", automergeGateYML, scalar(n))
	}
}

// Runs the gate through bash for each row. Real outputs, no mocks.
func TestAutomergeGate(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(automergeGateScript)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatal(err)
	}
	type in struct{ update, eco, ecos, deps, exclude, head string }
	cases := []struct {
		name   string
		in     in
		accept bool
		reason string // substring of the refusal reason
	}{
		{"go default patch", in{update: "version-update:semver-patch", eco: "go_modules", ecos: "go_modules", deps: "golang.org/x/net"}, true, ""},
		{"go default minor grouped", in{update: "version-update:semver-minor", eco: "go_modules", ecos: "go_modules", deps: "a/b, c/d"}, true, ""},
		{"go major refused", in{update: "version-update:semver-major", eco: "go_modules", ecos: "go_modules", deps: "a/b"}, false, "major updates need a human review"},
		{"no update-type refused", in{update: "", eco: "go_modules", ecos: "go_modules", deps: "a/b"}, false, "no update-type"},
		{"release-please branch refused", in{update: "version-update:semver-patch", eco: "go_modules", ecos: "go_modules", head: "release-please--branches--main"}, false, "release-please branches"},
		{"uv refused when only default allowed", in{update: "version-update:semver-patch", eco: "uv", ecos: "go_modules", deps: "requests"}, false, "package-ecosystem 'uv' is not in the allowed ecosystems 'go_modules'"},
		{"uv patch accepted with ecosystems uv", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "requests"}, true, ""},
		{"uv minor accepted with ecosystems uv", in{update: "version-update:semver-minor", eco: "uv", ecos: "uv", deps: "requests, pytest"}, true, ""},
		{"uv major refused", in{update: "version-update:semver-major", eco: "uv", ecos: "uv", deps: "requests"}, false, "major updates need a human review"},
		{"pip refused when only uv allowed", in{update: "version-update:semver-patch", eco: "pip", ecos: "uv", deps: "requests"}, false, "package-ecosystem 'pip' is not in the allowed ecosystems 'uv'"},
		{"github_actions refused when only uv allowed", in{update: "version-update:semver-patch", eco: "github_actions", ecos: "uv", deps: "actions/checkout"}, false, "github-actions updates need a human merge"},
		{"github_actions refused even if listed", in{update: "version-update:semver-patch", eco: "github_actions", ecos: "uv,github_actions", deps: "actions/checkout"}, false, "github-actions updates need a human merge"},
		{"list with spaces accepted", in{update: "version-update:semver-patch", eco: "uv", ecos: "go_modules, uv", deps: "requests"}, true, ""},
		{"invalid ecosystems refused", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv; rm -rf /", deps: "requests"}, false, "invalid 'ecosystems' input"},
		{"empty ecosystems refused", in{update: "version-update:semver-patch", eco: "uv", ecos: "", deps: "requests"}, false, "invalid 'ecosystems' input"},
		{"grouped torch excluded", in{update: "version-update:semver-minor", eco: "uv", ecos: "uv", deps: "requests, torch, pytest", exclude: "torch*,numpy"}, false, "'torch' (pattern 'torch*')"},
		{"torchvision excluded by torch*", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "torchvision", exclude: "torch*,numpy"}, false, "'torchvision' (pattern 'torch*')"},
		{"numpy exact excluded", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "numpy", exclude: "torch*,numpy"}, false, "'numpy' (pattern 'numpy')"},
		{"numpy-financial not matched by numpy", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "numpy-financial", exclude: "torch*,numpy"}, true, ""},
		{"grouped non-excluded accepted", in{update: "version-update:semver-minor", eco: "uv", ecos: "uv", deps: "requests, pytest, ruff", exclude: "torch*,numpy"}, true, ""},
		{"all excluded hits named", in{update: "version-update:semver-minor", eco: "uv", ecos: "uv", deps: "torch, numpy, requests", exclude: "torch*,numpy"}, false, "'torch' (pattern 'torch*'), 'numpy' (pattern 'numpy')"},
		{"newline separated globs", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "opencv-python", exclude: "torch*\nopencv*\nnumpy"}, false, "'opencv-python' (pattern 'opencv*')"},
		{"case and separators normalised", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "Typing_Extensions", exclude: "typing-extensions"}, false, "'Typing_Extensions'"},
		{"exact pattern does not match a longer name", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "openai-agents", exclude: "openai"}, true, ""},
		{"excluded wins over ecosystem pass", in{update: "version-update:semver-patch", eco: "go_modules", ecos: "go_modules", deps: "github.com/x/torch", exclude: "github.com/x/*"}, false, "excluded dependencies"},
		{"invalid exclude refused", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "requests", exclude: "$(id)"}, false, "invalid 'exclude-dependencies' entry"},
		{"exclude with empty dependency-names refused", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "", exclude: "torch*"}, false, "no dependency-names"},
		{"empty exclude with empty dependency-names accepted", in{update: "version-update:semver-patch", eco: "go_modules", ecos: "go_modules", deps: ""}, true, ""},
		{"blank exclude lines ignored", in{update: "version-update:semver-patch", eco: "uv", ecos: "uv", deps: "requests", exclude: "\n , \n"}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head := tc.in.head
			if head == "" {
				head = "dependabot/" + tc.in.eco + "/x"
			}
			cmd := exec.Command("bash", "-c", `set -euo pipefail; source "$SCRIPT"; if reason="$(gate)"; then echo "ACCEPT"; else echo "REFUSE: $reason"; fi`)
			cmd.Env = append(os.Environ(),
				"SCRIPT="+script, "HEAD_REF="+head, "UPDATE_TYPE="+tc.in.update, "ALLOWED="+patchMinor,
				"ECOSYSTEM="+tc.in.eco, "ECOSYSTEMS="+tc.in.ecos, "DEPENDENCIES="+tc.in.deps, "EXCLUDE="+tc.in.exclude)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("bash: %v\n%s", err, out)
			}
			got := strings.TrimSpace(string(out))
			if tc.accept {
				if got != "ACCEPT" {
					t.Errorf("got %q, want ACCEPT", got)
				}
				return
			}
			if !strings.HasPrefix(got, "REFUSE: ") || !strings.Contains(got, tc.reason) {
				t.Errorf("got %q, want a refusal containing %q", got, tc.reason)
			}
		})
	}
}
