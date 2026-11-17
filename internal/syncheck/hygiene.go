package syncheck

import (
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const rulePCHygiene = "precommit-hygiene-hooks"

var _ = register(rulePCHygiene)

// hygieneStageHooks run only at pre-commit. The three manifest-stage hooks are
// left to precommit-default-stages, which already reports them.
var hygieneStageHooks = []string{"check-merge-conflict", "check-yaml", "check-json"}

// hygieneNarrowing keys restrict the files a hook sees.
var hygieneNarrowing = []string{"files", "types", "types_or", "exclude_types"}

// hygieneOverrides keys replace what the hook runs; `true` as entry switches it off.
var hygieneOverrides = []string{"pass_filenames", "entry", "language"}

// probes are the paths no hygiene hook may hide: the base set plus the
// profile's hand-written source file.
func (c *checker) probes() []string {
	return append(slices.Clone(HygieneKeepProbes), c.prof.sourceProbes...)
}

// checkHygieneHooks requires the generic pre-commit-hooks set on every profile,
// unnarrowed, at pre-commit, with excludes that spare the config files and cover
// the profile's generated files.
func (c *checker) checkHygieneHooks(root *yaml.Node, repos []*pcRepo) {
	found := map[string]bool{}
	firstLine := 0
	for _, r := range repos {
		if normRepoURL(r.url) != preCommitHooksRepoURL {
			continue
		}
		if firstLine == 0 {
			firstLine = r.line
		}
		for _, h := range r.hooks {
			if !slices.Contains(HygieneHooks, h.id) {
				continue
			}
			found[h.id] = true
			c.checkHygieneHook(root, h)
		}
	}
	c.checkHygieneRoot(root)
	var missing []string
	for _, id := range HygieneHooks {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		c.add(PreCommitConfig, firstLine, rulePCHygiene,
			"no hook %s under `repo: %s`; add them (see %s); a `repo: local` hook of the same id does not count",
			strings.Join(missing, ", "), preCommitHooksRepoURL, c.prof.hygieneTpl)
	}
}

// checkHygieneRoot rejects a top-level `exclude` or `files` that hides a probe
// path from every hook at once.
func (c *checker) checkHygieneRoot(root *yaml.Node) {
	for _, k := range []string{"exclude", "files"} {
		kn := val(root, k)
		if kn == nil {
			continue
		}
		re, err := compilePCRegex(scalar(kn))
		if err != nil {
			c.add(PreCommitConfig, line(kn, 0), rulePCHygiene, "top-level %s %q cannot be verified (%v); use a pattern without lookarounds or backreferences, or exempt it with a reason", k, scalar(kn), err)
			continue
		}
		for _, p := range c.probes() {
			switch hit := re.MatchString(p); {
			case k == "exclude" && hit:
				c.add(PreCommitConfig, line(kn, 0), rulePCHygiene, "top-level exclude %q matches %s; that hides it from every hook; narrow the exclude or declare it per hook", scalar(kn), p)
			case k == "files" && !hit:
				c.add(PreCommitConfig, line(kn, 0), rulePCHygiene, "top-level files %q does not match %s; that hides it from every hook; remove it", scalar(kn), p)
			}
		}
	}
}

func (c *checker) checkHygieneHook(root *yaml.Node, h *pcHook) {
	n := h.node
	bad := func(ln int, format string, args ...any) {
		c.add(PreCommitConfig, ln, rulePCHygiene, "%s: "+format, append([]any{h.id}, args...)...)
	}
	for _, k := range hygieneNarrowing {
		if kn := val(n, k); kn != nil {
			bad(line(kn, h.line), "%s narrows the hook; remove it (args stay free)", k)
		}
	}
	for _, k := range hygieneOverrides {
		if kn := val(n, k); kn != nil {
			bad(line(kn, h.line), "%s overrides what the hook runs; remove it (args stay free)", k)
		}
	}
	// Without its own `stages`, the hook inherits default_stages, which
	// precommit-default-stages already requires to be {pre-commit}.
	if sn := val(n, "stages"); sn != nil && slices.Contains(hygieneStageHooks, h.id) {
		got, _ := effectiveStages(root, n)
		if !sameSet(got, setOf("pre-commit")) {
			bad(line(sn, h.line), "stages = %s; want {pre-commit}", fmtSet(got))
		}
	}

	en := val(n, "exclude")
	generated := slices.Contains(HygieneGeneratedHooks, h.id) && len(c.prof.generatedProbes) > 0
	if en == nil {
		if generated {
			bad(h.line, "no exclude; the fixer rewrites generated files (%s); add an exclude matching them", strings.Join(c.prof.generatedProbes, ", "))
		}
		return
	}
	re, err := compilePCRegex(scalar(en))
	if err != nil {
		bad(line(en, h.line), "exclude %q cannot be verified (%v); use a pattern without lookarounds or backreferences, or exempt it with a reason", scalar(en), err)
		return
	}
	for _, p := range c.probes() {
		if re.MatchString(p) {
			bad(line(en, h.line), "exclude %q matches %s; that disables the gate where it matters; narrow the exclude", scalar(en), p)
		}
	}
	if generated {
		for _, p := range c.prof.generatedProbes {
			if !re.MatchString(p) {
				bad(line(en, h.line), "exclude %q does not match generated file %s; the fixer would rewrite it", scalar(en), p)
			}
		}
	}
}

// compilePCRegex compiles a pre-commit pattern (Python re.search) with Go's
// RE2. `(?x)` is emulated by stripping unescaped whitespace and # comments
// outside character classes, as RE2 has no x flag.
func compilePCRegex(s string) (*regexp.Regexp, error) {
	if rest, ok := strings.CutPrefix(s, "(?x)"); ok {
		var b strings.Builder
		inClass := false
		classStart := 0 // index of the first rune after `[` and an optional `^`
		rs := []rune(rest)
		for i := 0; i < len(rs); i++ {
			r := rs[i]
			switch {
			case r == '\\' && i+1 < len(rs):
				b.WriteRune(r)
				i++
				b.WriteRune(rs[i])
			case inClass:
				if r == ']' && i > classStart {
					inClass = false
				}
				b.WriteRune(r)
			case r == '[':
				inClass = true
				classStart = i + 1
				if i+1 < len(rs) && rs[i+1] == '^' {
					classStart++
				}
				b.WriteRune(r)
			case r == '#':
				for i < len(rs) && rs[i] != '\n' {
					i++
				}
			case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			default:
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	return regexp.Compile(s)
}
