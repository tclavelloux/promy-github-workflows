package syncheck

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	prTitleFile   = ".github/workflows/pr-title.yml"
	releaseFile   = ".github/workflows/release-please.yml"
	vulnFixFile   = ".github/workflows/vuln-fix.yml"
	dependabotYML = ".github/dependabot.yml"

	rulePRTitle       = "pr-title"
	ruleReleasePlease = "release-please"
	ruleVulnFix       = "vuln-fix"
	ruleDependabot    = "dependabot"
)

var _ = register(rulePRTitle, ruleReleasePlease, ruleVulnFix, ruleDependabot)

// single reports whether jobs holds exactly the one job name, returning it.
func (c *checker) singleJob(rel, rule string, root *yaml.Node, name string) *yaml.Node {
	jk, jobs := get(root, "jobs")
	if names := keys(jobs); len(names) != 1 || names[0] != name {
		c.add(rel, line(jk, 1), rule, "jobs = [%s]; want exactly one job %q", strings.Join(names, ", "), name)
	}
	return val(jobs, name)
}

func (c *checker) checkPRTitle() {
	p := c.loadYAML(prTitleFile)
	if !p.exists {
		c.add(prTitleFile, 0, rulePRTitle, "file missing; add it calling %s so PR titles follow Conventional Commits", govRef("pr-title"))
		return
	}
	root := p.root
	if root == nil {
		return
	}
	pr := path(root, "on", "pull_request")
	want := setOf(PRTitleTriggerTypes...)
	if t, ok := strSet(val(pr, "types")); !ok || !sameSet(t, want) {
		c.add(prTitleFile, line(val(pr, "types"), line(pr, 1)), rulePRTitle,
			"on.pull_request.types = %s; want %s (edited is what re-checks a renamed title)", fmtSet(t), fmtSet(want))
	}
	pk, pn := get(root, "permissions")
	c.wantPerms(prTitleFile, line(pk, 1), rulePRTitle, "top-level", pn, PermsRead)

	job := c.singleJob(prTitleFile, rulePRTitle, root, "pr-title")
	if job == nil {
		return
	}
	k, n := get(job, "permissions")
	c.wantPerms(prTitleFile, line(k, line(job, 1)), rulePRTitle, `job "pr-title"`, n, PermsPRTitleJob)
	if u := val(job, "uses"); u == nil || u.Value != govRef("pr-title") {
		c.add(prTitleFile, line(u, line(job, 1)), rulePRTitle, `job "pr-title" uses %q; want %s`, scalar(u), govRef("pr-title"))
	}
}

func (c *checker) checkReleasePlease() {
	p := c.loadYAML(releaseFile)
	if !p.exists {
		c.add(releaseFile, 0, ruleReleasePlease, "file missing; add a release-please workflow running googleapis/release-please-action on push to main")
		return
	}
	root := p.root
	if root == nil {
		return
	}
	k, on := get(root, "on")
	push := val(on, "push")
	br, ok := strSet(val(push, "branches"))
	if names := onNames(on); len(names) != 1 || names[0] != "push" || len(keys(push)) != 1 || !ok || !sameSet(br, setOf("main")) {
		c.add(releaseFile, line(k, 1), ruleReleasePlease, "on = %s; want exactly `push: {branches: [main]}`", describeOn(on))
	}
	found := false
	if _, jobs := get(root, "jobs"); isMap(jobs) {
		for i := 1; i < len(jobs.Content); i += 2 {
			if steps := val(deref(jobs.Content[i]), "steps"); isSeq(steps) {
				for _, s := range steps.Content {
					if strings.HasPrefix(strings.ToLower(scalar(val(deref(s), "uses"))), "googleapis/release-please-action@") {
						found = true
					}
				}
			}
		}
	}
	if !found {
		c.add(releaseFile, line(val(root, "jobs"), 1), ruleReleasePlease, "no step uses googleapis/release-please-action")
	}
}

func describeOn(on *yaml.Node) string {
	if on == nil {
		return "(absent)"
	}
	return "[" + strings.Join(onNames(on), ", ") + "]"
}

func (c *checker) checkVulnFix() {
	p := c.loadYAML(vulnFixFile)
	if !p.exists || p.root == nil {
		return // optional file
	}
	root := p.root
	k, on := get(root, "on")
	names := setOf(onNames(on)...)
	bad := !names["schedule"]
	for n := range names {
		if n != "schedule" && n != "workflow_dispatch" {
			bad = true
		}
	}
	if bad {
		c.add(vulnFixFile, line(k, 1), ruleVulnFix, "on = %s; want schedule plus optional workflow_dispatch only", describeOn(on))
	}
	pk, pn := get(root, "permissions")
	c.wantPerms(vulnFixFile, line(pk, 1), ruleVulnFix, "top-level", pn, PermsRead)

	ck, cn := get(root, "concurrency")
	if scalar(val(cn, "group")) != "go-vuln-fix" || !isBool(val(cn, "cancel-in-progress"), false) {
		c.add(vulnFixFile, line(ck, 1), ruleVulnFix, "concurrency = {group: %q, cancel-in-progress: %q}; want {group: go-vuln-fix, cancel-in-progress: false} so a running fix is never cancelled mid-PR",
			scalar(val(cn, "group")), scalar(val(cn, "cancel-in-progress")))
	}
	job := c.singleJob(vulnFixFile, ruleVulnFix, root, "fix")
	if job == nil {
		return
	}
	jk, jn := get(job, "permissions")
	c.wantPerms(vulnFixFile, line(jk, line(job, 1)), ruleVulnFix, `job "fix"`, jn, PermsVulnFixJob)
	if u := val(job, "uses"); u == nil || u.Value != govRef("go-vuln-fix") {
		c.add(vulnFixFile, line(u, line(job, 1)), ruleVulnFix, `job "fix" uses %q; want %s`, scalar(u), govRef("go-vuln-fix"))
	}
}

func (c *checker) checkDependabot() {
	p := c.loadYAML(dependabotYML)
	if !p.exists {
		c.add(dependabotYML, 0, ruleDependabot, "file missing; add it with gomod and github-actions ecosystems (see templates/dependabot.yml)")
		return
	}
	root := p.root
	if root == nil {
		return
	}
	uk, updates := get(root, "updates")
	have := map[string]bool{}
	if isSeq(updates) {
		for _, u := range updates.Content {
			u = deref(u)
			atRoot := scalar(val(u, "directory")) == "/"
			if dirs, ok := strSet(val(u, "directories")); ok && dirs["/"] {
				atRoot = true
			}
			if atRoot {
				have[scalar(val(u, "package-ecosystem"))] = true
			}
		}
	}
	for _, eco := range []string{"gomod", "github-actions"} {
		if !have[eco] {
			c.add(dependabotYML, line(uk, 1), ruleDependabot, "no updates entry for package-ecosystem %q at directory \"/\"; add one", eco)
		}
	}
}
