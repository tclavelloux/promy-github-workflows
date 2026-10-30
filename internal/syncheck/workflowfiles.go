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
	if !p.exists && c.prof == docsProfile {
		return // a docs repo publishes no releases; the file is optional there
	}
	if !p.exists {
		c.add(releaseFile, 0, ruleReleasePlease, "file missing; copy templates/release-please.yml, which calls %s", govRef("release-please"))
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
	pk, pn := get(root, "permissions")
	c.wantPerms(releaseFile, line(pk, 1), ruleReleasePlease, "top-level", pn, PermsRead)

	job := c.singleJob(releaseFile, ruleReleasePlease, root, "release-please")
	if job == nil {
		return
	}
	jk, jn := get(job, "permissions")
	c.wantPerms(releaseFile, line(jk, line(job, 1)), ruleReleasePlease, `job "release-please"`, jn, PermsReleasePleaseJob)
	if u := val(job, "uses"); u == nil || u.Value != govRef("release-please") {
		why := ""
		if runsReleasePleaseLocally(job) {
			why = " (it runs release-please locally; call the governance workflow instead)"
		}
		c.add(releaseFile, line(u, line(job, 1)), ruleReleasePlease, `job "release-please" uses %q; want %s%s`, scalar(u), govRef("release-please"), why)
	}
	c.checkAppSecrets(releaseFile, ruleReleasePlease, "release-please", job)
}

// runsReleasePleaseLocally reports whether a job has a step running
// googleapis/release-please-action, the pre-reusable-workflow shape.
func runsReleasePleaseLocally(job *yaml.Node) bool {
	steps := val(job, "steps")
	if !isSeq(steps) {
		return false
	}
	for _, s := range steps.Content {
		if strings.HasPrefix(strings.ToLower(scalar(val(deref(s), "uses"))), "googleapis/release-please-action@") {
			return true
		}
	}
	return false
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
	wf := c.prof.vulnFixWorkflow
	if scalar(val(cn, "group")) != wf || !isBool(val(cn, "cancel-in-progress"), false) {
		c.add(vulnFixFile, line(ck, 1), ruleVulnFix, "concurrency = {group: %q, cancel-in-progress: %q}; want {group: %s, cancel-in-progress: false} so a running fix is never cancelled mid-PR",
			scalar(val(cn, "group")), scalar(val(cn, "cancel-in-progress")), wf)
	}
	job := c.singleJob(vulnFixFile, ruleVulnFix, root, "fix")
	if job == nil {
		return
	}
	jk, jn := get(job, "permissions")
	c.wantPerms(vulnFixFile, line(jk, line(job, 1)), ruleVulnFix, `job "fix"`, jn, PermsVulnFixJob)
	if u := val(job, "uses"); u == nil || u.Value != govRef(wf) {
		c.add(vulnFixFile, line(u, line(job, 1)), ruleVulnFix, `job "fix" uses %q; want %s`, scalar(u), govRef(wf))
	}
	if c.prof == pythonProfile {
		c.checkVulnFixProjects(job)
	}
	c.checkAppSecrets(vulnFixFile, ruleVulnFix, "fix", job)
}

// checkVulnFixProjects requires the Python caller's with.projects to list every
// uv project: a project left out is never patched.
func (c *checker) checkVulnFixProjects(job *yaml.Node) {
	want := c.uvProjects()
	n := path(job, "with", "projects")
	got, err := parseProjects(n)
	if err != nil {
		c.add(vulnFixFile, line(n, line(job, 1)), ruleVulnFix, `job "fix" with.projects is %s; want '%s' (every uv project (a directory with a tracked uv.lock))`, err, jsonList(want))
		return
	}
	gotSet, wantSet := setOf(got...), setOf(want...)
	var missing, unknown []string
	for _, d := range want {
		if !gotSet[d] {
			missing = append(missing, d)
		}
	}
	for _, d := range sortedKeys(gotSet) {
		if !wantSet[d] {
			unknown = append(unknown, d)
		}
	}
	if len(missing)+len(unknown) > 0 {
		c.add(vulnFixFile, n.Line, ruleVulnFix, `job "fix" with.projects = %s; want '%s', every uv project (a directory with a tracked uv.lock) (missing: [%s], unknown: [%s]); a project left out is never patched`,
			n.Value, jsonList(want), strings.Join(missing, ", "), strings.Join(unknown, ", "))
	}
}

// appSecrets are the only secrets a bot-PR caller job may pass: the GitHub App
// credentials that let its PR trigger CI. The block is optional.
var appSecrets = []string{"APP_CLIENT_ID", "APP_PRIVATE_KEY"}

func (c *checker) checkAppSecrets(file, rule, jobName string, job *yaml.Node) {
	sk, sn := get(job, "secrets")
	if sk == nil {
		return
	}
	want := `{APP_CLIENT_ID: ${{ secrets.APP_CLIENT_ID }}, APP_PRIVATE_KEY: ${{ secrets.APP_PRIVATE_KEY }}}`
	if !isMap(sn) {
		c.add(file, line(sk, 1), rule, `job %q secrets = %q; want an explicit mapping %s (least privilege: never `+"`secrets: inherit`"+`)`, jobName, scalar(sn), want)
		return
	}
	got := map[string]string{}
	for _, k := range keys(sn) {
		got[k] = normExpr(scalar(val(sn, k)))
	}
	ok := len(got) == len(appSecrets)
	for _, name := range appSecrets {
		if got[name] != "secrets."+name {
			ok = false
		}
	}
	if !ok {
		c.add(file, line(sk, 1), rule, `job %q secrets = %v; want exactly %s (least privilege)`, jobName, got, want)
	}
}

func (c *checker) checkDependabot() {
	p := c.loadYAML(dependabotYML)
	if !p.exists {
		c.add(dependabotYML, 0, ruleDependabot, "file missing; %s", c.prof.dependabotTp)
		return
	}
	root := p.root
	if root == nil {
		return
	}
	if c.prof == pythonProfile {
		c.checkPythonDependabot(root)
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
	ecosystems := []string{"gomod", "github-actions"}
	if c.prof == docsProfile {
		ecosystems = []string{"github-actions"}
	}
	for _, eco := range ecosystems {
		if !have[eco] {
			c.add(dependabotYML, line(uk, 1), ruleDependabot, "no updates entry for package-ecosystem %q at directory \"/\"; add one", eco)
		}
	}
}
