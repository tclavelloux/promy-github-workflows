package syncheck

import (
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	rulePCInstallTypes = "precommit-install-types"
	rulePCGovRepo      = "precommit-governance-repo"
	rulePCRev          = "precommit-rev"
	rulePCCoverageHook = "precommit-coverage-hook"
	rulePCNoTestHook   = "precommit-no-test-hook"
	rulePCCommitMsg    = "precommit-commit-msg"
	ruleVulnTarget     = "vuln-target"
)

var _ = register(rulePCInstallTypes, rulePCGovRepo, rulePCRev, rulePCCoverageHook, rulePCNoTestHook, rulePCCommitMsg, ruleVulnTarget)

var immutableHookRev = regexp.MustCompile(`^hooks/v\d+\.\d+\.\d+$`)

// pcHook is one hook entry with the repo entry that owns it.
type pcHook struct {
	repoURL string
	node    *yaml.Node
	id      string
	line    int
}

type pcRepo struct {
	url   string
	line  int
	rev   *yaml.Node
	hooks []*pcHook
}

func normRepoURL(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), "/")
	return strings.TrimSuffix(u, ".git")
}

func readPCRepos(root *yaml.Node) []*pcRepo {
	var out []*pcRepo
	repos := val(root, "repos")
	if !isSeq(repos) {
		return nil
	}
	for _, rn := range repos.Content {
		rn = deref(rn)
		if !isMap(rn) {
			continue
		}
		r := &pcRepo{url: scalar(val(rn, "repo")), line: rn.Line, rev: val(rn, "rev")}
		if k := val(rn, "repo"); k != nil {
			r.line = k.Line
		}
		if hs := val(rn, "hooks"); isSeq(hs) {
			for _, hn := range hs.Content {
				hn = deref(hn)
				if isMap(hn) {
					r.hooks = append(r.hooks, &pcHook{repoURL: r.url, node: hn, id: scalar(val(hn, "id")), line: line(val(hn, "id"), hn.Line)})
				}
			}
		}
		out = append(out, r)
	}
	return out
}

func (r *pcRepo) hasHook(id string) bool {
	for _, h := range r.hooks {
		if h.id == id {
			return true
		}
	}
	return false
}

// normStage maps pre-commit's legacy stage names onto the current ones.
func normStage(s string) string {
	switch s {
	case "commit":
		return "pre-commit"
	case "push":
		return "pre-push"
	case "merge-commit":
		return "pre-merge-commit"
	}
	return s
}

// effectiveStages is the hook's stages, else top-level default_stages, else
// nil meaning "every stage".
func effectiveStages(root, hook *yaml.Node) (map[string]bool, bool) {
	n := val(hook, "stages")
	if n == nil {
		n = val(root, "default_stages")
	}
	if n == nil {
		return nil, false
	}
	set, _ := strSet(n)
	out := map[string]bool{}
	for s := range set {
		out[normStage(s)] = true
	}
	return out, true
}

func (c *checker) checkPreCommit() {
	p := c.loadYAML(PreCommitConfig)
	if !p.exists {
		c.add(PreCommitConfig, 0, rulePCInstallTypes, "file missing; add %s with the fleet's hooks (see templates/pre-commit-go-vuln.yaml)", PreCommitConfig)
		return
	}
	root := p.root
	if !isMap(root) {
		return
	}
	repos := readPCRepos(root)

	c.checkInstallTypes(root)
	c.checkGovernanceRepo(repos)
	c.checkRevs(repos)
	c.checkCoverageHook(repos)
	c.checkNoTestHook(root, repos)
	c.checkCommitMsgHook(repos)
	c.checkVulnTarget(repos)
}

func (c *checker) checkInstallTypes(root *yaml.Node) {
	k, n := get(root, "default_install_hook_types")
	want := setOf(InstallHookTypes...)
	if k == nil {
		c.add(PreCommitConfig, 0, rulePCInstallTypes,
			"default_install_hook_types is absent; `pre-commit install` then writes only the pre-commit hook and commit-msg/pre-push never fire; want %s", fmtSet(want))
		return
	}
	if got, ok := strSet(n); !ok || !sameSet(got, want) {
		c.add(PreCommitConfig, k.Line, rulePCInstallTypes, "default_install_hook_types = %s; want %s", fmtSet(got), fmtSet(want))
	}
}

func (c *checker) checkGovernanceRepo(repos []*pcRepo) {
	var gov []*pcRepo
	for _, r := range repos {
		if normRepoURL(r.url) == GovernanceHookRepoURL {
			gov = append(gov, r)
		}
	}
	if len(gov) == 0 {
		c.add(PreCommitConfig, 0, rulePCGovRepo, "no `repo: %s` entry; add it with rev hooks/vX.Y.Z and hooks %s, %s", GovernanceHookRepoURL, HookBranchGuard, HookWorkflowSync)
		return
	}
	for _, extra := range gov[1:] {
		c.add(PreCommitConfig, extra.line, rulePCGovRepo, "%s is listed %d times; keep one entry and put all its hooks there", GovernanceHookRepoURL, len(gov))
	}
	g := gov[0]
	rev := scalar(g.rev)
	if !immutableHookRev.MatchString(rev) {
		c.add(PreCommitConfig, line(g.rev, g.line), rulePCGovRepo,
			"rev %q is not an immutable hooks/vX.Y.Z tag; pre-commit resolves a moving tag like hooks/v1 once and caches it, so it ships nothing new and warns on every run", rev)
	}
	for _, id := range []string{HookBranchGuard, HookWorkflowSync} {
		if !g.hasHook(id) {
			c.add(PreCommitConfig, g.line, rulePCGovRepo, "governance repo entry has no hook %q", id)
		}
	}
}

func (c *checker) checkRevs(repos []*pcRepo) {
	for _, r := range repos {
		if r.url == "local" || r.url == "meta" {
			continue
		}
		switch rev := scalar(r.rev); rev {
		case "", "main", "master", "HEAD":
			c.add(PreCommitConfig, line(r.rev, r.line), rulePCRev, "%s: rev %q floats; pin a release tag", r.url, rev)
		}
	}
}

func allHooks(repos []*pcRepo) []*pcHook {
	var out []*pcHook
	for _, r := range repos {
		out = append(out, r.hooks...)
	}
	return out
}

func (c *checker) checkCoverageHook(repos []*pcRepo) {
	found := false
	for _, r := range repos {
		if r.url != "local" {
			continue
		}
		for _, h := range r.hooks {
			if h.id != HookCheckCoverage {
				continue
			}
			found = true
			c.checkCoverageFields(h)
		}
	}
	if !found {
		c.add(PreCommitConfig, 0, rulePCCoverageHook, "no `repo: local` hook %q; add it running `make check-coverage` at pre-push", HookCheckCoverage)
	}
}

func (c *checker) checkCoverageFields(h *pcHook) {
	n := h.node
	bad := func(ln int, format string, args ...any) {
		c.add(PreCommitConfig, ln, rulePCCoverageHook, format, args...)
	}
	if e := val(n, "entry"); strings.TrimSpace(scalar(e)) != "make check-coverage" {
		bad(line(e, h.line), "entry = %q; want `make check-coverage`", scalar(e))
	}
	if l := val(n, "language"); scalar(l) != "system" {
		bad(line(l, h.line), "language = %q; want system", scalar(l))
	}
	if pf := val(n, "pass_filenames"); !isBool(pf, false) {
		bad(line(pf, h.line), "pass_filenames must be false; the target takes no file arguments")
	}
	if ar := val(n, "always_run"); !isBool(ar, true) {
		bad(line(ar, h.line), "always_run must be true; without it a push with no .go files would skip the coverage gate")
	}
	want := setOf("pre-push")
	sn := val(n, "stages")
	if got, ok := strSet(sn); !ok || !sameSet(got, want) {
		bad(line(sn, h.line), "stages = %s; want %s (coverage is a push gate, not a commit gate)", fmtSet(got), fmtSet(want))
	}
}

func (c *checker) checkNoTestHook(root *yaml.Node, repos []*pcRepo) {
	for _, h := range allHooks(repos) {
		if h.id == "test" {
			c.add(PreCommitConfig, h.line, rulePCNoTestHook, "hook %q duplicates the suite %s already runs; remove it", h.id, HookCheckCoverage)
			continue
		}
		if h.id == HookCheckCoverage {
			continue
		}
		entry := scalar(val(h.node, "entry"))
		if !strings.Contains(entry, "go test") && !strings.Contains(entry, "make test") {
			continue
		}
		if st, restricted := effectiveStages(root, h.node); restricted && !st["pre-push"] {
			continue
		}
		c.add(PreCommitConfig, h.line, rulePCNoTestHook, "hook %q runs the test suite at pre-push (entry %q); it duplicates the suite %s already runs", h.id, entry, HookCheckCoverage)
	}
}

func (c *checker) checkCommitMsgHook(repos []*pcRepo) {
	found := false
	wantArgs := setOf(CommitTypes...)
	for _, h := range allHooks(repos) {
		if h.id != HookConventional {
			continue
		}
		found = true
		sn := val(h.node, "stages")
		if got, ok := strSet(sn); !ok || !sameSet(got, setOf("commit-msg")) {
			c.add(PreCommitConfig, line(sn, h.line), rulePCCommitMsg, "%s stages = %s; want {commit-msg}", HookConventional, fmtSet(got))
		}
		an := val(h.node, "args")
		if got, ok := strSet(an); !ok || !sameSet(got, wantArgs) {
			c.add(PreCommitConfig, line(an, h.line), rulePCCommitMsg, "%s args = %s; want the PR-title types %s", HookConventional, fmtSet(got), fmtSet(wantArgs))
		}
	}
	if !found {
		c.add(PreCommitConfig, 0, rulePCCommitMsg, "no %q hook; add it with stages [commit-msg] and the commit types", HookConventional)
	}
}

func (c *checker) checkVulnTarget(repos []*pcRepo) {
	var hook *pcHook
	for _, h := range allHooks(repos) {
		if h.id == HookGoVuln {
			hook = h
			break
		}
	}
	var target *makeTarget
	if data, ok := c.read(MakefileRel); ok {
		target = parseMakefile(strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")).targets["vuln"]
	}
	switch {
	case hook != nil && target == nil:
		c.add(PreCommitConfig, hook.line, ruleVulnTarget, "hook %q is present but the Makefile has no `vuln:` target; add it (templates/Makefile.vuln.mk)", HookGoVuln)
	case hook == nil && target != nil:
		c.add(MakefileRel, target.line, ruleVulnTarget, "`vuln:` target exists but %s has no %q hook; add it under the governance repo (templates/pre-commit-go-vuln.yaml)", PreCommitConfig, HookGoVuln)
	case hook != nil && target != nil:
		rec := target.meaningful()
		if len(rec) != 1 || rec[0].text != VulnRecipe {
			c.add(MakefileRel, target.line, ruleVulnTarget, "`vuln:` recipe differs; want exactly one line `%s`", VulnRecipe)
		}
	}
}
