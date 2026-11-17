package syncheck

import (
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// hookBlock returns the line range [start, end) of the `- id: <id>` entry of the
// pre-commit-hooks repo (six-space indent) and the file's lines.
func hookBlock(t *testing.T, s, id string) (lines []string, start, end int) {
	t.Helper()
	lines = strings.SplitAfter(s, "\n")
	start = -1
	for i, l := range lines {
		if l == "      - id: "+id+"\n" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no hook %q in config", id)
	}
	end = start + 1
	for end < len(lines) && strings.HasPrefix(lines[end], "        ") {
		end++
	}
	return lines, start, end
}

// setHookKey replaces (or inserts after the id line) the block of key in hook id.
// block is the full replacement text, indented by 8 spaces; "" removes the key.
func setHookKey(t *testing.T, root, id, key, block string) {
	t.Helper()
	lines, start, end := hookBlock(t, readFile(t, root, pcRel), id)
	var out []string
	out = append(out, lines[:start+1]...)
	skipping := false
	for _, l := range lines[start+1 : end] {
		switch {
		case strings.HasPrefix(l, "        "+key+":"):
			skipping = true
			continue
		case skipping && strings.HasPrefix(l, "          "):
			continue
		}
		skipping = false
		out = append(out, l)
	}
	if block != "" {
		out = append(out[:start+1], append([]string{block}, out[start+1:]...)...)
	}
	out = append(out, lines[end:]...)
	writeFile(t, root, pcRel, strings.Join(out, ""))
}

// setRoot adds a top-level key after default_stages.
func setRoot(t *testing.T, root, kv string) {
	t.Helper()
	replace(t, root, pcRel, "default_stages: [pre-commit]\n", "default_stages: [pre-commit]\n"+kv+"\n")
}

func dropHook(t *testing.T, root, id string) {
	t.Helper()
	lines, start, end := hookBlock(t, readFile(t, root, pcRel), id)
	writeFile(t, root, pcRel, strings.Join(append(lines[:start:start], lines[end:]...), ""))
}

const localJSONHook = "\n  - repo: local\n    hooks:\n      - id: check-json\n        name: check-json\n        entry: check-json\n        language: system\n"

func TestHygieneHooksDrift(t *testing.T) {
	fixtures := []string{"pass-service", pyPassFixture, flutterPassFixture, docsPassFixture}
	cases := []struct {
		name string
		do   func(t *testing.T, root string)
		msg  string // empty: must pass
	}{
		{"check-added-large-files removed", func(t *testing.T, r string) { dropHook(t, r, "check-added-large-files") }, "check-added-large-files"},
		{"check-json removed", func(t *testing.T, r string) { dropHook(t, r, "check-json") }, "check-json"},
		{"check-merge-conflict removed", func(t *testing.T, r string) { dropHook(t, r, "check-merge-conflict") }, "check-merge-conflict"},
		{"check-yaml removed", func(t *testing.T, r string) { dropHook(t, r, "check-yaml") }, "check-yaml"},
		{"end-of-file-fixer removed", func(t *testing.T, r string) { dropHook(t, r, "end-of-file-fixer") }, "end-of-file-fixer"},
		{"trailing-whitespace removed", func(t *testing.T, r string) { dropHook(t, r, "trailing-whitespace") }, "trailing-whitespace"},
		{"check-json local only", func(t *testing.T, r string) {
			dropHook(t, r, "check-json")
			appendFile(t, r, pcRel, localJSONHook)
		}, "check-json"},
		{"check-yaml manual", func(t *testing.T, r string) { setHookKey(t, r, "check-yaml", "stages", "        stages: [manual]\n") }, "check-yaml: stages = {manual}"},
		{"check-merge-conflict pre-push", func(t *testing.T, r string) {
			setHookKey(t, r, "check-merge-conflict", "stages", "        stages: [pre-push]\n")
		}, "check-merge-conflict: stages = {pre-push}"},
		{"files on check-yaml", func(t *testing.T, r string) { setHookKey(t, r, "check-yaml", "files", "        files: '\\.ya?ml$'\n") }, "files narrows the hook"},
		{"types on trailing-whitespace", func(t *testing.T, r string) {
			setHookKey(t, r, "trailing-whitespace", "types", "        types: [text]\n")
		}, "types narrows the hook"},
		{"exclude everything on check-json", func(t *testing.T, r string) { setHookKey(t, r, "check-json", "exclude", "        exclude: '.*'\n") }, ".pre-commit-config.yaml"},
		{"exclude .github on check-yaml", func(t *testing.T, r string) {
			setHookKey(t, r, "check-yaml", "exclude", "        exclude: '^\\.github/'\n")
		}, ".github/workflows/ci.yml"},
		{"exclude README on check-json", func(t *testing.T, r string) {
			setHookKey(t, r, "check-json", "exclude", "        exclude: 'README'\n")
		}, "README.md"},
		{"lookbehind exclude", func(t *testing.T, r string) {
			setHookKey(t, r, "check-json", "exclude", "        exclude: '(?<=a)b'\n")
		}, "cannot be verified"},
		{"pass_filenames off on check-yaml", func(t *testing.T, r string) {
			setHookKey(t, r, "check-yaml", "pass_filenames", "        pass_filenames: false\n")
		}, "pass_filenames overrides"},
		{"entry true on check-json", func(t *testing.T, r string) { setHookKey(t, r, "check-json", "entry", "        entry: 'true'\n") }, "entry overrides"},
		{"language override on check-yaml", func(t *testing.T, r string) {
			setHookKey(t, r, "check-yaml", "language", "        language: system\n")
		}, "language overrides"},
		{"exclude json on check-json", func(t *testing.T, r string) {
			setHookKey(t, r, "check-json", "exclude", "        exclude: '\\.json$'\n")
		}, ".vscode/settings.json"},
		{"root exclude everything", func(t *testing.T, r string) { setRoot(t, r, "exclude: '.*'") }, "top-level exclude"},
		{"root files nothing", func(t *testing.T, r string) { setRoot(t, r, "files: '^nothing/'") }, "top-level files"},
		{"root exclude lookbehind", func(t *testing.T, r string) { setRoot(t, r, "exclude: '(?<=a)b'") }, "cannot be verified"},
		{"harmless root exclude is fine", func(t *testing.T, r string) { setRoot(t, r, "exclude: '^vendor/'") }, ""},
		{"root files covering probes is fine", func(t *testing.T, r string) { setRoot(t, r, "files: '.*'") }, ""},
		{"legacy commit stage is fine", func(t *testing.T, r string) {
			setHookKey(t, r, "check-yaml", "stages", "        stages: [commit]\n")
		}, ""},
		{"verbose multi-line exclude is fine", func(t *testing.T, r string) {
			setHookKey(t, r, "check-json", "exclude", "        exclude: |\n          (?x)\n          ^vendor/   # third party\n          | ^gen/\n")
		}, ""},
		{"args stay free", func(t *testing.T, r string) {
			setHookKey(t, r, "check-yaml", "args", "        args: [--unsafe]\n")
		}, ""},
	}
	for _, fx := range fixtures {
		for _, tc := range cases {
			t.Run(fx+"/"+tc.name, func(t *testing.T) {
				root := fixture(t, fx)
				tc.do(t, root)
				rep := run(t, root)
				if tc.msg == "" {
					if !rep.OK() {
						t.Fatalf("want pass:\n%s", joinMessages(rep.Violations))
					}
					return
				}
				if got := ruleIDs(rep.Violations); !equalStrings(got, []string{rulePCHygiene}) {
					t.Fatalf("rules = %v, want [%s]\n%s", got, rulePCHygiene, joinMessages(rep.Violations))
				}
				if !strings.Contains(joinMessages(rep.Violations), tc.msg) {
					t.Errorf("no message contains %q:\n%s", tc.msg, joinMessages(rep.Violations))
				}
			})
		}
	}
}

func TestHygieneMissingNamesProfileTemplate(t *testing.T) {
	for fx, tpl := range map[string]string{
		"pass-service": HygieneTemplate, pyPassFixture: "templates/python/pre-commit-config.yaml",
		flutterPassFixture: "templates/flutter/pre-commit-config.yaml", docsPassFixture: "templates/docs/pre-commit-config.yaml",
	} {
		root := fixture(t, fx)
		dropHook(t, root, "check-yaml")
		if msg := joinMessages(run(t, root).Violations); !strings.Contains(msg, "see "+tpl) {
			t.Errorf("%s: message does not name %s:\n%s", fx, tpl, msg)
		}
	}
}

func TestHygieneGeneratedExcludes(t *testing.T) {
	cases := []struct {
		name, fixture, exclude string // exclude "" removes it
		msg                    string // empty: must pass
	}{
		{"flutter exclude removed", flutterPassFixture, "", "lib/x.g.dart"},
		{"flutter only .g.dart", flutterPassFixture, "        exclude: '\\.g\\.dart$'\n", "lib/x.freezed.dart"},
		{"flutter alternation", flutterPassFixture, "        exclude: '\\.g\\.dart$|\\.freezed\\.dart$'\n", ""},
		{"python exclude removed", pyPassFixture, "", "src/app/.basedpyright/baseline.json"},
		{"python wrong path", pyPassFixture, "        exclude: '^other/baseline\\.json$'\n", "src/app/.basedpyright/baseline.json"},
		{"python custom regex", pyPassFixture, "        exclude: 'baseline\\.json$'\n", ""},
	}
	for _, tc := range cases {
		for _, id := range HygieneGeneratedHooks {
			t.Run(tc.name+"/"+id, func(t *testing.T) {
				root := fixture(t, tc.fixture)
				setHookKey(t, root, id, "exclude", tc.exclude)
				rep := run(t, root)
				if tc.msg == "" {
					if !rep.OK() {
						t.Fatalf("want pass:\n%s", joinMessages(rep.Violations))
					}
					return
				}
				if got := ruleIDs(rep.Violations); !equalStrings(got, []string{rulePCHygiene}) {
					t.Fatalf("rules = %v, want [%s]\n%s", got, rulePCHygiene, joinMessages(rep.Violations))
				}
				if msg := joinMessages(rep.Violations); !strings.Contains(msg, tc.msg) || !strings.Contains(msg, id) {
					t.Errorf("want message naming %q and %q:\n%s", tc.msg, id, msg)
				}
			})
		}
	}
	// Go and docs have no generated probes: no exclude is required.
	for _, fx := range []string{"pass-service", docsPassFixture} {
		root := fixture(t, fx)
		setHookKey(t, root, "trailing-whitespace", "exclude", "")
		if rep := run(t, root); !rep.OK() {
			t.Errorf("%s: no probes expected:\n%s", fx, joinMessages(rep.Violations))
		}
	}
	// Probes apply to every entry of the hook, duplicates included.
	root := fixture(t, flutterPassFixture)
	appendFile(t, root, pcRel, "\n  - repo: https://github.com/pre-commit/pre-commit-hooks\n    rev: v6.0.0\n    hooks:\n      - id: trailing-whitespace\n        stages: [pre-commit]\n")
	if msg := joinMessages(run(t, root).Violations); !strings.Contains(msg, "lib/x.g.dart") {
		t.Errorf("duplicate entry without exclude not reported:\n%s", msg)
	}
}

func TestHygieneSourceProbes(t *testing.T) {
	cases := []struct {
		fixture, id, exclude, probe string
	}{
		{flutterPassFixture, "trailing-whitespace", `\.dart$`, FlutterSourceProbe},
		{flutterPassFixture, "end-of-file-fixer", `\.dart$`, FlutterSourceProbe},
		{flutterPassFixture, "check-yaml", `\.dart$`, FlutterSourceProbe},
		{pyPassFixture, "trailing-whitespace", `^src/`, PythonSourceProbe},
		{pyPassFixture, "end-of-file-fixer", `^src/`, PythonSourceProbe},
		{"pass-service", "trailing-whitespace", `\.go$`, GoSourceProbe},
		{"pass-service", "end-of-file-fixer", `\.go$`, GoSourceProbe},
	}
	for _, tc := range cases {
		t.Run(tc.fixture+"/"+tc.id, func(t *testing.T) {
			root := fixture(t, tc.fixture)
			setHookKey(t, root, tc.id, "exclude", "        exclude: '"+tc.exclude+"'\n")
			rep := run(t, root)
			if got := ruleIDs(rep.Violations); !equalStrings(got, []string{rulePCHygiene}) {
				t.Fatalf("rules = %v\n%s", got, joinMessages(rep.Violations))
			}
			if msg := joinMessages(rep.Violations); !strings.Contains(msg, tc.probe) {
				t.Errorf("no message names %s:\n%s", tc.probe, msg)
			}
		})
	}
}

func TestHygieneSnippetPassesRule(t *testing.T) {
	root := fixture(t, "pass-service")
	cfg := readFile(t, root, pcRel)
	start := strings.Index(cfg, "  - repo: https://github.com/pre-commit/pre-commit-hooks\n")
	end := strings.Index(cfg[start+1:], "\n  - repo:") + start + 1
	snippet := string(govRead(t, HygieneTemplate))
	snippet = snippet[strings.Index(snippet, "  - repo:"):]
	writeFile(t, root, pcRel, cfg[:start]+snippet+cfg[end:])
	if rep := run(t, root); !rep.OK() {
		t.Fatalf("%s fails the Go profile:\n%s", HygieneTemplate, joinMessages(rep.Violations))
	}
}

func TestCompilePCRegex(t *testing.T) {
	re, err := compilePCRegex("(?x)\n^a  # comment\n| b[ ]c \\ d")
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]bool{"a": true, "xb c dx": true, "bc d": false, "zz": false} {
		if re.MatchString(in) != want {
			t.Errorf("match(%q) = %v, want %v", in, !want, want)
		}
	}
	// `]` first in a class is a member: the space inside it survives (x mode keeps class whitespace).
	for pat, in := range map[string]string{"(?x)[] ]b": " b", "(?x)[^] ]b": "xb"} {
		re, err := compilePCRegex(pat)
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(in) {
			t.Errorf("%s must match %q", pat, in)
		}
	}
	if _, err := compilePCRegex("(?<=a)b"); err == nil {
		t.Error("lookbehind must not compile")
	}
}

func TestHygieneTemplatesCarrySet(t *testing.T) {
	for _, rel := range []string{
		"templates/flutter/pre-commit-config.yaml", "templates/python/pre-commit-config.yaml",
		"templates/docs/pre-commit-config.yaml", HygieneTemplate,
	} {
		doc := deref(govYAML(t, rel))
		repos := doc
		if isMap(doc) {
			repos = val(doc, "repos")
		}
		wrap := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "repos"}, repos}}
		var ids []string
		for _, r := range readPCRepos(wrap) {
			if normRepoURL(r.url) != preCommitHooksRepoURL {
				continue
			}
			for _, h := range r.hooks {
				ids = append(ids, h.id)
			}
		}
		sort.Strings(ids)
		if !equalStrings(ids, append([]string(nil), HygieneHooks...)) {
			t.Errorf("%s: pre-commit-hooks ids = %v, want %v", rel, ids, HygieneHooks)
		}
	}
}

func TestHygieneExemption(t *testing.T) {
	root := fixture(t, "pass-service")
	dropHook(t, root, "trailing-whitespace")
	replace(t, root, pcRel, "  - repo: https://github.com/pre-commit/pre-commit-hooks\n",
		"  # sync-exempt: precommit-hygiene-hooks trailing-whitespace conflicts with markdown line breaks\n  - repo: https://github.com/pre-commit/pre-commit-hooks\n")
	rep := run(t, root)
	if !rep.OK() || len(rep.Exemptions) != 1 {
		t.Fatalf("want 0 violations and 1 exemption:\n%s\nexemptions=%v", joinMessages(rep.Violations), rep.Exemptions)
	}
}
