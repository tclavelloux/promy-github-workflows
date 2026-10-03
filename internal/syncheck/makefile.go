package syncheck

import (
	"regexp"
	"strings"
)

const (
	ruleMakeSetup       = "make-setup"
	ruleMakeHooksPath   = "make-hookspath"
	ruleMakeCoveragePin = "make-coverage-pin"
)

var _ = register(ruleMakeSetup, ruleMakeHooksPath, ruleMakeCoveragePin)

type recipeLine struct {
	text string // without the leading tab
	line int
}

type makeTarget struct {
	name   string
	line   int
	recipe []recipeLine
}

type makefile struct {
	targets map[string]*makeTarget
	lines   []string
}

var targetRe = regexp.MustCompile(`^([^\s:=#$.][^\s:=#$]*(?:\s+[^\s:=#$]+)*)\s*:`)

// parseMakefile is deliberately minimal: targets, their tab-indented recipe
// lines (plus backslash continuations) and 1-based line numbers.
func parseMakefile(lines []string) *makefile {
	m := &makefile{targets: map[string]*makeTarget{}, lines: lines}
	var cur []*makeTarget
	continued := false
	for i, l := range lines {
		ln := i + 1
		switch {
		case continued && cur != nil:
			m.addRecipe(cur, strings.TrimLeft(l, " \t"), ln)
			continued = strings.HasSuffix(l, "\\")
		case strings.HasPrefix(l, "\t"):
			if cur != nil {
				m.addRecipe(cur, l[1:], ln)
				continued = strings.HasSuffix(l, "\\")
			}
		case strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#"):
			// blank and comment lines never end a recipe
		default:
			cur = nil
			mm := targetRe.FindStringSubmatch(l)
			if mm == nil || strings.HasPrefix(l[len(mm[0]):], "=") {
				continue // variable assignment (`:=`, `::=`) or directive
			}
			for _, name := range strings.Fields(mm[1]) {
				t := &makeTarget{name: name, line: ln}
				if _, dup := m.targets[name]; !dup {
					m.targets[name] = t
				}
				cur = append(cur, t)
			}
		}
	}
	return m
}

func (m *makefile) addRecipe(ts []*makeTarget, text string, ln int) {
	for _, t := range ts {
		t.recipe = append(t.recipe, recipeLine{text, ln})
	}
}

// meaningful returns recipe lines minus blanks and `#` comments, trimmed.
func (t *makeTarget) meaningful() []recipeLine {
	var out []recipeLine
	for _, r := range t.recipe {
		s := strings.TrimSpace(r.text)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		out = append(out, recipeLine{s, r.line})
	}
	return out
}

func (c *checker) loadMakefile() (*makefile, bool) {
	_, ok := c.read(MakefileRel)
	if !ok {
		return nil, false
	}
	return parseMakefile(c.files[MakefileRel]), true
}

var coverageInstallRe = regexp.MustCompile(`go install github\.com/vladopajic/go-test-coverage/v2@(\S+)`)

func (c *checker) checkMakefile() {
	mk, ok := c.loadMakefile()
	if !ok {
		c.add(MakefileRel, 0, ruleMakeSetup, "file missing; add a `setup` target running `%s`", MakeSetupLine)
		return
	}

	c.checkMakeSetup(mk)

	for i, l := range mk.lines {
		if strings.Contains(l, "core.hooksPath") {
			c.add(MakefileRel, i+1, ruleMakeHooksPath,
				"`core.hooksPath` found; it and pre-commit are mutually exclusive, so pre-commit would refuse to install")
		}
	}

	c.checkMakeCoverage(mk)

	for _, name := range sortedKeys(mk.targets) {
		for _, r := range mk.targets[name].recipe {
			if strings.Contains(r.text, "@latest") || strings.Contains(r.text, "releases/latest") {
				c.add(MakefileRel, r.line, ruleUnpinnedTool,
					"target %q: recipe uses a floating version (%q); pin an exact version", name, strings.TrimSpace(r.text))
			}
		}
	}
}

func (c *checker) checkMakeSetup(mk *makefile) {
	t := mk.targets["setup"]
	if t == nil {
		c.add(MakefileRel, 0, ruleMakeSetup, "no `setup` target; add one running `%s`", MakeSetupLine)
		return
	}
	rec := t.meaningful()
	if len(rec) == 1 && rec[0].text == MakeSetupLine {
		return
	}
	got := make([]string, len(rec))
	for i, r := range rec {
		got[i] = r.text
	}
	c.add(MakefileRel, t.line, ruleMakeSetup, "`setup` recipe is [%s]; want exactly one line `%s`", strings.Join(got, " ; "), MakeSetupLine)
}

func (c *checker) checkMakeCoverage(mk *makefile) {
	want := "v" + GoTestCoverageVersion
	t := mk.targets["install-go-test-coverage"]
	switch {
	case t == nil:
		c.add(MakefileRel, 0, ruleMakeCoveragePin,
			"no `install-go-test-coverage` target; add one running `go install github.com/vladopajic/go-test-coverage/v2@%s`", want)
	default:
		found := ""
		ln := t.line
		for _, r := range t.recipe {
			if m := coverageInstallRe.FindStringSubmatch(r.text); m != nil {
				found, ln = m[1], r.line
				break
			}
		}
		if found != want {
			if found == "" {
				c.add(MakefileRel, ln, ruleMakeCoveragePin,
					"`install-go-test-coverage` has no `go install github.com/vladopajic/go-test-coverage/v2@%s` line", want)
			} else {
				c.add(MakefileRel, ln, ruleMakeCoveragePin,
					"go-test-coverage installed at @%s; want @%s (go-coverage.yml runs v%s, so local and CI would measure differently)",
					found, want, GoTestCoverageVersion)
			}
		}
	}
	if mk.targets["check-coverage"] == nil {
		c.add(MakefileRel, 0, ruleMakeCoveragePin, "no `check-coverage` target; the pre-push coverage hook runs `make check-coverage`")
	}
}
