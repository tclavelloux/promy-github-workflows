package syncheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var zizmorFixtures = []string{"pass-service", pyPassFixture, flutterPassFixture, docsPassFixture}

func TestZizmorConfigPasses(t *testing.T) {
	tmpl := string(govRead(t, "templates/zizmor.yml"))
	for _, name := range []string{"pass-service", "pass-library", "pass-rollout", pyPassFixture, flutterPassFixture, docsPassFixture} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t, name)
			writeFile(t, root, zizmorFile, tmpl) // absent in some fixtures; present-path for all profiles
			if rep := run(t, root); !rep.OK() {
				t.Fatalf("templates/zizmor.yml as %s fails:\n%s", zizmorFile, joinMessages(rep.Violations))
			}
		})
	}
}

// The fixtures exercise the present path with the exact template callers copy.
func TestZizmorTemplateMatchesFixtures(t *testing.T) {
	tmpl := string(govRead(t, "templates/zizmor.yml"))
	for _, name := range zizmorFixtures {
		b, err := os.ReadFile(filepath.Join("testdata", name, filepath.FromSlash(zizmorFile)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if string(b) != tmpl {
			t.Errorf("testdata/%s/%s differs from templates/zizmor.yml; copy the template", name, zizmorFile)
		}
	}
}

func TestZizmorConfigDrift(t *testing.T) {
	const policy = "        \"tclavelloux/promy-github-workflows/*\": ref-pin\n"
	cases := []struct {
		name, old, new, msg string
	}{
		{"blanket ref-pin", policy, "        \"*\": ref-pin\n", "want exactly"},
		{"hash-pin", "ref-pin", "hash-pin", "want exactly"},
		{"extra policy", policy, policy + "        \"actions/*\": ref-pin\n", "want exactly"},
		{"wrong repo glob", "promy-github-workflows/*", "other-repo/*", "want exactly"},
		{"bare repo without glob", "workflows/*\"", "workflows\"", "want exactly"},
		{"unknown rule key", "rules:\n", "rules:\n  unpinned-uses-x:\n    disable: true\n", "rules = ["},
		{"unpinned-uses disable", "  unpinned-uses:\n", "  unpinned-uses:\n    disable: true\n", "unpinned-uses keys"},
		{"unpinned-uses ignore", "  unpinned-uses:\n", "  unpinned-uses:\n    ignore:\n      - ci.yml\n", "unpinned-uses keys"},
		{"extra rule", "rules:\n", "rules:\n  template-injection:\n    disable: true\n", "rules = ["},
		{"top-level key", "rules:\n", "foo: bar\nrules:\n", "top-level keys"},
	}
	for _, tc := range cases {
		for _, name := range zizmorFixtures {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				root := fixture(t, name)
				replace(t, root, zizmorFile, tc.old, tc.new)
				rep := run(t, root)
				if got := ruleIDs(rep.Violations); !equalStrings(got, []string{ruleZizmorConfig}) {
					t.Fatalf("rules = %v, want [%s]\n%s", got, ruleZizmorConfig, joinMessages(rep.Violations))
				}
				if !strings.Contains(joinMessages(rep.Violations), tc.msg) {
					t.Errorf("no message contains %q:\n%s", tc.msg, joinMessages(rep.Violations))
				}
			})
		}
	}
}

func TestZizmorConfigAbsentPasses(t *testing.T) {
	root := fixture(t, "pass-rollout")
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(zizmorFile))); err == nil {
		t.Fatal("pass-rollout must stay without .github/zizmor.yml: it covers the absent path")
	}
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("absent config must pass:\n%s", joinMessages(rep.Violations))
	}
}

func TestZizmorSecondConfig(t *testing.T) {
	cases := []struct{ name, base, file string }{
		{"root zizmor.yml beside .github", "pass-service", "zizmor.yml"},
		{"root zizmor.yml alone", "pass-rollout", "zizmor.yml"},
		{"root zizmor.yaml", "pass-rollout", "zizmor.yaml"},
		{".github/zizmor.yaml", "pass-rollout", ".github/zizmor.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, tc.base)
			writeFile(t, root, tc.file, string(govRead(t, "templates/zizmor.yml")))
			rep := run(t, root)
			if got := ruleIDs(rep.Violations); !equalStrings(got, []string{ruleZizmorConfig}) {
				t.Fatalf("rules = %v, want [%s]\n%s", got, ruleZizmorConfig, joinMessages(rep.Violations))
			}
			if !strings.Contains(joinMessages(rep.Violations), "zizmor also discovers "+tc.file) {
				t.Errorf("no message names %s:\n%s", tc.file, joinMessages(rep.Violations))
			}
		})
	}
}
