package syncheck

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pin-manifest.json lists every manually pinned tool version so
// .github/workflows/pin-drift.yml can compare it with upstream. These tests keep
// the manifest complete and resolvable; a pin missing here is never checked.

const (
	pinManifestFile = "pin-manifest.json"
	pinPolicyFile   = "internal/syncheck/policy.go"
)

type pinLocation struct {
	File    string `json:"file"`
	Const   string `json:"const"`
	Pattern string `json:"pattern"`
}

type pinEntry struct {
	Name      string        `json:"name"`
	Locations []pinLocation `json:"locations"`
}

func loadPinManifest(t *testing.T) []pinEntry {
	t.Helper()
	var m struct {
		Pins []pinEntry `json:"pins"`
	}
	if err := json.Unmarshal(govRead(t, pinManifestFile), &m); err != nil {
		t.Fatalf("%s: %v", pinManifestFile, err)
	}
	if len(m.Pins) == 0 {
		t.Fatalf("%s has no pins", pinManifestFile)
	}
	return m.Pins
}

func TestPinManifestCoversVersionFiles(t *testing.T) {
	listed := map[string]bool{}
	for _, p := range loadPinManifest(t) {
		for _, l := range p.Locations {
			if l.Const == "" && l.Pattern == "" {
				listed[l.File] = true
			}
		}
	}
	files, err := filepath.Glob(filepath.Join("..", "..", ".*-version"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no root .*-version files found; the glob is broken")
	}
	for _, f := range files {
		name := filepath.Base(f)
		if !listed[name] {
			t.Errorf("%s has no location in %s: add it to pin-manifest.json", name, pinManifestFile)
		}
	}
}

func TestPinManifestCoversPolicyVersionConsts(t *testing.T) {
	listed := map[string]bool{}
	for _, p := range loadPinManifest(t) {
		for _, l := range p.Locations {
			if l.File == pinPolicyFile && l.Const != "" {
				listed[l.Const] = true
			}
		}
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", govRead(t, pinPolicyFile), 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			for _, n := range s.(*ast.ValueSpec).Names {
				if strings.HasSuffix(n.Name, "Version") {
					found = append(found, n.Name)
				}
			}
		}
	}
	if len(found) == 0 {
		t.Fatalf("no *Version consts found in %s; the parser walk is broken", pinPolicyFile)
	}
	sort.Strings(found)
	for _, name := range found {
		if !listed[name] {
			t.Errorf("const %s in %s has no location in %s: add it to pin-manifest.json", name, pinPolicyFile, pinManifestFile)
		}
	}
}

func TestPinManifestLocationsResolveOnce(t *testing.T) {
	for _, p := range loadPinManifest(t) {
		if len(p.Locations) == 0 {
			t.Errorf("pin %q has no locations in %s", p.Name, pinManifestFile)
		}
		for _, l := range p.Locations {
			text := string(govRead(t, l.File))
			switch {
			case l.Const != "":
				rx := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(l.Const) + `\s*=\s*"([^"]*)"`)
				if n := len(rx.FindAllStringSubmatch(text, -1)); n != 1 {
					t.Errorf("pin %q: const %s in %s matched %d times, want 1: fix pin-manifest.json", p.Name, l.Const, l.File, n)
				}
			case l.Pattern != "":
				rx, err := regexp.Compile("(?m)" + l.Pattern)
				if err != nil {
					t.Errorf("pin %q: pattern for %s does not compile in Go: %v", p.Name, l.File, err)
					continue
				}
				if rx.NumSubexp() != 1 {
					t.Errorf("pin %q: pattern for %s needs exactly one capture group, has %d", p.Name, l.File, rx.NumSubexp())
					continue
				}
				if n := len(rx.FindAllStringSubmatch(text, -1)); n != 1 {
					t.Errorf("pin %q: pattern %q in %s matched %d times, want 1: fix pin-manifest.json", p.Name, l.Pattern, l.File, n)
				}
			default:
				if strings.TrimSpace(text) == "" {
					t.Errorf("pin %q: %s is empty: fix pin-manifest.json", p.Name, l.File)
				}
			}
		}
	}
}
