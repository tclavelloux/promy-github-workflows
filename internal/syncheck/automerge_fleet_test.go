package syncheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	goModBase = "module example.com/m\n\ngo 1.26.9\n\nrequire (\n\tgolang.org/x/net v0.30.0\n\tgithub.com/a/b v1.2.3 // indirect\n)\n\nreplace github.com/c/d => ./d\n"
	uvBase    = "version = 1\nrequires-python = \">=3.12\"\n\n[[package]]\nname = \"requests\"\nversion = \"2.32.3\"\nsource = { registry = \"https://pypi.org/simple\" }\n\n[[package]]\nname = \"typing-extensions\"\nversion = \"4.12.2\"\n"
)

// Runs fleet_gate (promy-fleet-bot vuln-fix PRs) through bash on real files.
func TestFleetGate(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(automergeGateScript)))
	if err != nil {
		t.Fatal(err)
	}
	rep := strings.ReplaceAll
	goBump := rep(goModBase, "go 1.26.9", "go 1.26.10")
	type f struct{ status, path, base, head string }
	cases := []struct {
		name    string
		head    string
		files   []f
		ecos    string
		exclude string
		accept  bool
		reason  string
	}{
		{"go directive patch", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, goBump}, {"modified", "go.sum", "a", "b"}}, "go_modules", "", true, "go 1.26.9 -> 1.26.10"},
		{"go module patch", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "v0.30.0", "v0.30.2")}}, "go_modules", "", true, "golang.org/x/net v0.30.0 -> v0.30.2"},
		{"go module minor refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "v0.30.0", "v0.31.0")}}, "go_modules", "", false, "not a patch-level bump"},
		{"go module major refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "v1.2.3", "v2.2.3")}}, "go_modules", "", false, "not a patch-level bump"},
		{"go directive minor refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "go 1.26.9", "go 1.27.0")}}, "go_modules", "", false, "not a patch-level bump"},
		{"go downgrade refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "v1.2.3", "v1.2.2")}}, "go_modules", "", false, "not a patch-level bump"},
		{"go prerelease refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "v1.2.3", "v1.2.4-rc1")}}, "go_modules", "", false, "not a patch-level bump"},
		{"go toolchain added refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "go 1.26.9", "go 1.26.10\n\ntoolchain go1.26.10")}}, "go_modules", "", false, "line changed outside"},
		{"go replace changed refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goBump, "./d", "./evil")}}, "go_modules", "", false, "line changed outside"},
		{"go require added refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "\tgithub.com/a/b", "\tgithub.com/x/y v1.0.0\n\tgithub.com/a/b")}}, "go_modules", "", false, "was added"},
		{"go require removed refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, rep(goModBase, "\tgithub.com/a/b v1.2.3 // indirect\n", "")}}, "go_modules", "", false, "was removed"},
		{"go no bump refused", "fix/vuln-abc", []f{{"modified", "go.sum", "a", "b"}}, "go_modules", "", false, "no version bump"},
		{"other file refused", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, goBump}, {"modified", "main.go", "a", "b"}}, "go_modules", "", false, "outside the files"},
		{"added file refused", "fix/vuln-abc", []f{{"added", "go.sum", "", "b"}}, "go_modules", "", false, "only modified"},
		{"traversal refused", "fix/vuln-abc", []f{{"modified", "../go.mod", "a", "b"}}, "go_modules", "", false, "unexpected file path"},
		{"wrong branch refused", "feature/x", []f{{"modified", "go.mod", goModBase, goBump}}, "go_modules", "", false, "not a vuln-fix branch"},
		{"uv file on go branch refused", "fix/vuln-abc", []f{{"modified", "uv.lock", uvBase, rep(uvBase, "2.32.3", "2.32.4")}}, "go_modules", "", false, "does not belong"},
		{"go ecosystem not allowed", "fix/vuln-abc", []f{{"modified", "go.mod", goModBase, goBump}}, "uv", "", false, "not in the allowed ecosystems"},
		{"uv patch", "fix/py-vuln-abc", []f{{"modified", "src/app/uv.lock", uvBase, rep(uvBase, "2.32.3", "2.32.5")}}, "uv", "", true, "requests 2.32.3 -> 2.32.5"},
		{"uv minor refused", "fix/py-vuln-abc", []f{{"modified", "uv.lock", uvBase, rep(uvBase, "2.32.3", "2.33.0")}}, "uv", "", false, "not a patch-level bump"},
		{"uv excluded", "fix/py-vuln-abc", []f{{"modified", "uv.lock", uvBase, rep(uvBase, "2.32.3", "2.32.5")}}, "uv", "req*", false, "excluded dependencies"},
		{"uv new package refused", "fix/py-vuln-abc", []f{{"modified", "uv.lock", uvBase, uvBase + "\n[[package]]\nname = \"evil\"\nversion = \"1.0.0\"\n"}}, "uv", "", false, "was added"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			var list []string
			for _, x := range tc.files {
				list = append(list, x.status+"\t"+x.path)
				if strings.Contains(x.path, "..") {
					continue
				}
				writeFile(t, root, "base/"+x.path, x.base)
				writeFile(t, root, "head/"+x.path, x.head)
			}
			cmd := exec.Command("bash", "-c", `set -euo pipefail; source "$SCRIPT"; if reason="$(fleet_gate)"; then echo "ACCEPT: $reason"; else echo "REFUSE: $reason"; fi`)
			cmd.Env = append(os.Environ(), "SCRIPT="+script, "HEAD_REF="+tc.head, "FLEET_FILES="+strings.Join(list, "\n"),
				"BASE_DIR="+filepath.Join(root, "base"), "HEAD_DIR="+filepath.Join(root, "head"),
				"ECOSYSTEMS="+tc.ecos, "EXCLUDE="+tc.exclude)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("bash: %v\n%s", err, out)
			}
			got := strings.TrimSpace(string(out))
			want := "REFUSE: "
			if tc.accept {
				want = "ACCEPT: "
			}
			if !strings.HasPrefix(got, want) || !strings.Contains(got, tc.reason) {
				t.Errorf("got %q, want prefix %q containing %q", got, want, tc.reason)
			}
		})
	}
}
