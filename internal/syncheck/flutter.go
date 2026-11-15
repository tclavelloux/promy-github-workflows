package syncheck

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

const ruleFlutterVersion = "flutter-version"

var _ = register(ruleFlutterVersion)

func (c *checker) checkFlutterRepoFiles() {
	c.checkPinFile(FlutterVersionFile, ruleFlutterVersion, FlutterVersion,
		"the fleet Flutter SDK pin; flutter-lint and flutter-test install the SDK from this file")
}

const rulePCDartHooks = "precommit-dart-hooks"

var _ = register(rulePCDartHooks)

type dartHookSpec struct {
	id, entry, stage, why string
	alwaysRun             bool
}

// checkDartHooks requires the local Dart hooks in their exact shape: format at
// pre-commit, analyze and test at pre-push.
func (c *checker) checkDartHooks(root *yaml.Node, repos []*pcRepo) {
	specs := []dartHookSpec{
		{HookDartFormat, DartFormatEntry, "pre-commit", "format is cheap, so it gates the commit", false},
		{HookDartAnalyze, DartAnalyzeEntry, "pre-push", "analyze runs on the whole package, so it gates the push", true},
		{HookFlutterTest, FlutterTestEntry, "pre-push", "without the env prefix a worktree push reports SDK 0.0.0-unknown (promy-frontend#46)", true},
	}
	for _, s := range specs {
		var hooks []*pcHook
		for _, r := range repos {
			if r.url != "local" {
				continue
			}
			for _, h := range r.hooks {
				if h.id == s.id {
					hooks = append(hooks, h)
				}
			}
		}
		if len(hooks) == 0 {
			c.add(PreCommitConfig, 0, rulePCDartHooks, "no `repo: local` hook %q; add it (see %s)", s.id, c.prof.precommitTpl)
			continue
		}
		for _, hook := range hooks {
			c.checkDartHook(root, hook, s)
		}
	}
}

func (c *checker) checkDartHook(root *yaml.Node, hook *pcHook, s dartHookSpec) {
	n := hook.node
	bad := func(ln int, format string, args ...any) {
		c.add(PreCommitConfig, ln, rulePCDartHooks, format, args...)
	}
	if e := val(n, "entry"); strings.TrimSpace(scalar(e)) != s.entry {
		bad(line(e, hook.line), "%s: entry = %q; want `%s` (%s)", s.id, scalar(e), s.entry, s.why)
	}
	if l := val(n, "language"); scalar(l) != "system" {
		bad(line(l, hook.line), "%s: language = %q; want system", s.id, scalar(l))
	}
	if pf := val(n, "pass_filenames"); !isBool(pf, false) {
		bad(line(pf, hook.line), "%s: pass_filenames must be false; the command takes the whole package", s.id)
	}
	if s.alwaysRun {
		if ar := val(n, "always_run"); !isBool(ar, true) {
			bad(line(ar, hook.line), "%s: always_run must be true; without it a push touching no Dart file would skip the gate", s.id)
		}
	}
	for _, k := range []string{"files", "exclude"} {
		if kn := val(n, k); kn != nil {
			bad(line(kn, hook.line), "%s: %s narrows what the hook sees; remove it", s.id, k)
		}
	}
	if tn := val(n, "types"); tn != nil {
		got, ok := strSet(tn)
		if s.id != HookDartFormat || !ok || !sameSet(got, setOf("dart")) {
			bad(line(tn, hook.line), "%s: types = %s; only dart-format may set types, and only [dart]", s.id, fmtSet(got))
		}
	}
	want := setOf(s.stage)
	got, restricted := effectiveStages(root, n)
	if !restricted || !sameSet(got, want) {
		bad(line(val(n, "stages"), hook.line), "%s: stages = %s; want %s (%s)", s.id, fmtSet(got), fmtSet(want), s.why)
	}
}
