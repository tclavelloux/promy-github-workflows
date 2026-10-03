package syncheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	ciRel  = ".github/workflows/ci.yml"
	prRel  = ".github/workflows/pr-title.yml"
	rpRel  = ".github/workflows/release-please.yml"
	vfRel  = ".github/workflows/vuln-fix.yml"
	depRel = ".github/dependabot.yml"
)

// fixture copies testdata/<name> into a temp dir so tests can drift it.
func fixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", name)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// replace swaps the first occurrence of old; it fails the test if old is absent,
// so a fixture edit can never silently stop drifting the file.
func replace(t *testing.T, root, rel, old, new string) {
	t.Helper()
	s := readFile(t, root, rel)
	if !strings.Contains(s, old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	writeFile(t, root, rel, strings.Replace(s, old, new, 1))
}

func run(t *testing.T, root string) Report {
	t.Helper()
	rep, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func ruleIDs(fs []Finding) []string {
	seen := map[string]bool{}
	for _, f := range fs {
		seen[f.Rule] = true
	}
	return sortedKeys(seen)
}

func joinMessages(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.String() + "\n")
	}
	return b.String()
}

func equalStrings(a, b []string) bool {
	sort.Strings(b)
	return strings.Join(a, ",") == strings.Join(b, ",")
}
