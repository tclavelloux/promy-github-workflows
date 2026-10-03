package syncheck

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	ciFile = ".github/workflows/ci.yml"

	ruleCITrigger     = "ci-trigger"
	ruleCIConcurrency = "ci-concurrency"
	ruleCIPerms       = "ci-permissions"
	ruleCIJobs        = "ci-jobs"
	ruleCIJobRef      = "ci-job-ref"
	ruleCIDraftSkip   = "ci-draft-skip"
	ruleCITimeout     = "ci-timeout"
	ruleCIJobPerms    = "ci-job-permissions"
	ruleCIArtifact    = "ci-test-artifact"
	ruleCILintSync    = "ci-lint-sync"
	ruleCIAutomerge   = "ci-automerge"
)

var _ = register(ruleCITrigger, ruleCIConcurrency, ruleCIPerms, ruleCIJobs, ruleCIJobRef,
	ruleCIDraftSkip, ruleCITimeout, ruleCIJobPerms, ruleCIArtifact, ruleCILintSync, ruleCIAutomerge)

type ciJob struct {
	name string
	key  *yaml.Node
	node *yaml.Node
}

func (j ciJob) line() int { return j.key.Line }

// wantPerms reports a permissions block that differs from want.
func (c *checker) wantPerms(rel string, ln int, rule, what string, n *yaml.Node, want perms) {
	if p, ok := readPerms(n); ok && p.equal(want) {
		return
	}
	c.add(rel, ln, rule, "%s permissions = %s; want exactly %s", what, describePerms(n), want)
}

// onNames lists the event names of an `on:` node in any of its three forms.
func onNames(on *yaml.Node) []string {
	switch {
	case on == nil:
		return nil
	case on.Kind == yaml.ScalarNode:
		return []string{on.Value}
	case isSeq(on):
		var out []string
		for _, n := range on.Content {
			out = append(out, deref(n).Value)
		}
		return out
	}
	return keys(on)
}

func (c *checker) checkCI() {
	p := c.loadYAML(ciFile)
	if !p.exists {
		c.add(ciFile, 0, ruleCIJobs, "file missing; every repo needs %s with jobs lint, vuln, test, coverage, gitleaks (plus docker when a Dockerfile exists)", ciFile)
		return
	}
	root := p.root
	if root == nil {
		return
	}
	c.ciTrigger(root)
	c.ciConcurrency(root)
	pk, pn := get(root, "permissions")
	c.wantPerms(ciFile, line(pk, 1), ruleCIPerms, "top-level", pn, PermsRead)

	jobsKey, jobsNode := get(root, "jobs")
	if !isMap(jobsNode) {
		c.add(ciFile, line(jobsKey, 1), ruleCIJobs, "no jobs: mapping; want jobs lint, vuln, test, coverage, gitleaks")
		return
	}
	var order []ciJob
	byName := map[string]ciJob{}
	for i := 0; i+1 < len(jobsNode.Content); i += 2 {
		j := ciJob{jobsNode.Content[i].Value, jobsNode.Content[i], deref(jobsNode.Content[i+1])}
		order = append(order, j)
		byName[j.name] = j
	}

	c.ciJobSet(jobsKey, order, byName)
	for _, j := range order {
		c.ciJobRef(j)
		if j.name != "automerge" {
			c.ciDraftSkip(j)
		}
		if val(j.node, "runs-on") != nil && val(j.node, "timeout-minutes") == nil {
			c.add(ciFile, j.line(), ruleCITimeout, "job %q runs inline but has no timeout-minutes; add one (any value) so a hung job cannot burn the minutes budget", j.name)
		}
	}
	c.ciJobPermissions(byName)
	c.ciArtifact(byName)
	c.ciLintSync(byName)
	c.ciAutomerge(order, byName)
}

func (c *checker) ciTrigger(root *yaml.Node) {
	k, on := get(root, "on")
	if on == nil {
		c.add(ciFile, 1, ruleCITrigger, "no on: block; want `pull_request` with branches [main] and types %s", fmtSet(setOf(CITriggerTypes...)))
		return
	}
	names := onNames(on)
	if !isMap(on) || len(names) != 1 || names[0] != "pull_request" {
		c.add(ciFile, k.Line, ruleCITrigger, "on = [%s]; want exactly `pull_request` (a push trigger doubles every run on the branch PR)", strings.Join(names, ", "))
		return
	}
	pr := val(on, "pull_request")
	if !isMap(pr) {
		c.add(ciFile, on.Line, ruleCITrigger, "on.pull_request has no branches/types; want branches [main] and types %s", fmtSet(setOf(CITriggerTypes...)))
		return
	}
	for _, kk := range keys(pr) {
		if kk != "branches" && kk != "types" {
			c.add(ciFile, pr.Line, ruleCITrigger, "on.pull_request has key %q; want only branches and types", kk)
		}
	}
	if b, ok := strSet(val(pr, "branches")); !ok || !sameSet(b, setOf("main")) {
		c.add(ciFile, line(val(pr, "branches"), pr.Line), ruleCITrigger, "on.pull_request.branches = %s; want {main}", fmtSet(b))
	}
	want := setOf(CITriggerTypes...)
	if t, ok := strSet(val(pr, "types")); !ok || !sameSet(t, want) {
		c.add(ciFile, line(val(pr, "types"), pr.Line), ruleCITrigger, "on.pull_request.types = %s; want %s (ready_for_review is what un-skips draft PRs)", fmtSet(t), fmtSet(want))
	}
}

func (c *checker) ciConcurrency(root *yaml.Node) {
	k, n := get(root, "concurrency")
	if !isMap(n) {
		c.add(ciFile, line(k, 1), ruleCIConcurrency, "no concurrency mapping; want group `%s` and cancel-in-progress: true", ConcurrencyGroup)
		return
	}
	if g := normExpr(scalar(val(n, "group"))); g != normExpr(ConcurrencyGroup) {
		c.add(ciFile, line(val(n, "group"), k.Line), ruleCIConcurrency, "concurrency.group = %q; want %q", scalar(val(n, "group")), ConcurrencyGroup)
	}
	if !isBool(val(n, "cancel-in-progress"), true) {
		c.add(ciFile, line(val(n, "cancel-in-progress"), k.Line), ruleCIConcurrency, "concurrency.cancel-in-progress = %q; want true so a new push cancels the stale run", scalar(val(n, "cancel-in-progress")))
	}
}

func (c *checker) ciJobSet(jobsKey *yaml.Node, order []ciJob, byName map[string]ciJob) {
	allowed := setOf(CIRequiredJobs...)
	for _, n := range CIOptionalJobs {
		allowed[n] = true
	}
	allowed[CIDockerJob] = true

	for _, n := range CIRequiredJobs {
		if _, ok := byName[n]; !ok {
			c.add(ciFile, jobsKey.Line, ruleCIJobs, "required job %q is missing", n)
		}
	}
	hasDockerfile := c.isRegularFile("Dockerfile")
	_, hasDocker := byName[CIDockerJob]
	switch {
	case hasDockerfile && !hasDocker:
		c.add(ciFile, jobsKey.Line, ruleCIJobs, "a Dockerfile exists at the repo root but job %q is missing; add it so the image build is verified on every PR", CIDockerJob)
	case !hasDockerfile && hasDocker:
		c.add(ciFile, byName[CIDockerJob].line(), ruleCIJobs, "job %q is present but there is no Dockerfile at the repo root; remove the job (libraries have no image to build)", CIDockerJob)
	}
	for _, j := range order {
		if !allowed[j.name] {
			c.add(ciFile, j.line(), ruleCIJobs, "unknown job %q; allowed: %s", j.name, strings.Join(append(append(append([]string{}, CIRequiredJobs...), CIDockerJob), CIOptionalJobs...), ", "))
		}
	}
}

func (c *checker) ciJobRef(j ciJob) {
	name, ok := CIReusableJobs[j.name]
	if !ok {
		return
	}
	u := val(j.node, "uses")
	if u == nil {
		c.add(ciFile, j.line(), ruleCIJobRef, "job %q has no uses:; want %s", j.name, govRef(name))
		return
	}
	m := govWFRe.FindStringSubmatch(u.Value)
	switch {
	case m == nil && strings.HasPrefix(u.Value, govPrefix):
		return // malformed governance ref: governance-ref reports it
	case m == nil || m[1] != name:
		c.add(ciFile, u.Line, ruleCIJobRef, "job %q uses %q; want %s", j.name, u.Value, govRef(name))
	}
	// A right workflow at a wrong ref is governance-ref's finding, not a duplicate here.
}

func (c *checker) ciDraftSkip(j ciJob) {
	n := val(j.node, "if")
	if n == nil || normExpr(scalar(n)) != DraftSkipIf {
		c.add(ciFile, line(n, j.line()), ruleCIDraftSkip, "job %q if = %q; want %q so draft PRs spend no minutes", j.name, scalar(n), DraftSkipIf)
	}
}

func (c *checker) ciJobPermissions(byName map[string]ciJob) {
	for _, name := range CINoPermissionJobs {
		j, ok := byName[name]
		if !ok {
			continue
		}
		if k, n := get(j.node, "permissions"); k != nil {
			c.add(ciFile, k.Line, ruleCIJobPerms, "job %q permissions = %s; want no permissions: key (inherits top-level contents: read)", name, describePerms(n))
		}
	}
	if j, ok := byName["coverage"]; ok {
		k, n := get(j.node, "permissions")
		c.wantPerms(ciFile, line(k, j.line()), ruleCIJobPerms, `job "coverage"`, n, PermsCoverage)
		if need, ok := strSet(val(j.node, "needs")); !ok || !sameSet(need, setOf("test")) {
			c.add(ciFile, line(val(j.node, "needs"), j.line()), ruleCIJobPerms, `job "coverage" needs = %s; want {test} (it downloads the test job's artifact)`, fmtSet(need))
		}
		if scalar(val(j.node, "secrets")) != "inherit" {
			c.add(ciFile, line(val(j.node, "secrets"), j.line()), ruleCIJobPerms, `job "coverage" secrets = %q; want inherit (fleet convention for the coverage caller)`, scalar(val(j.node, "secrets")))
		}
	}
	if j, ok := byName["gitleaks"]; ok {
		k, n := get(j.node, "permissions")
		c.wantPerms(ciFile, line(k, j.line()), ruleCIJobPerms, `job "gitleaks"`, n, PermsGitleaks)
		c.gitleaksSteps(j)
	}
}

func (c *checker) gitleaksSteps(j ciJob) {
	steps := val(j.node, "steps")
	var hasAction, hasCheckout bool
	if isSeq(steps) {
		for _, s := range steps.Content {
			s = deref(s)
			u := strings.ToLower(scalar(val(s, "uses")))
			switch {
			case strings.HasPrefix(u, "gitleaks/gitleaks-action@"):
				hasAction = true
			case strings.HasPrefix(u, "actions/checkout@"):
				hasCheckout = true
				if f := path(s, "with", "fetch-depth"); scalar(f) != "0" {
					c.add(ciFile, line(f, val(s, "uses").Line), ruleCIJobPerms, `job "gitleaks" checkout fetch-depth = %q; want 0 (the scan needs full history)`, scalar(f))
				}
			}
		}
	}
	if !hasAction {
		c.add(ciFile, j.line(), ruleCIJobPerms, `job "gitleaks" has no step using gitleaks/gitleaks-action`)
	}
	if !hasCheckout {
		c.add(ciFile, j.line(), ruleCIJobPerms, `job "gitleaks" has no actions/checkout step with fetch-depth: 0`)
	}
}

func (c *checker) ciArtifact(byName map[string]ciJob) {
	j, ok := byName["test"]
	if !ok {
		return
	}
	var up *yaml.Node
	if steps := val(j.node, "steps"); isSeq(steps) {
		for _, s := range steps.Content {
			if strings.HasPrefix(strings.ToLower(scalar(val(deref(s), "uses"))), "actions/upload-artifact@") {
				up = deref(s)
			}
		}
	}
	if up == nil {
		c.add(ciFile, j.line(), ruleCIArtifact, `job "test" has no actions/upload-artifact step; the coverage job needs artifact coverage-profile`)
		return
	}
	for _, w := range [][2]string{
		{"name", "coverage-profile"}, {"path", "coverage.raw.out"},
		{"retention-days", "1"}, {"if-no-files-found", "error"},
	} {
		n := path(up, "with", w[0])
		if scalar(n) != w[1] {
			c.add(ciFile, line(n, val(up, "uses").Line), ruleCIArtifact, "test artifact with.%s = %q; want %q", w[0], scalar(n), w[1])
		}
	}
}

func (c *checker) ciLintSync(byName map[string]ciJob) {
	j, ok := byName["lint"]
	if !ok {
		return
	}
	n := path(j.node, "with", "workflow-sync")
	if !isBool(n, true) {
		c.add(ciFile, line(n, j.line()), ruleCILintSync, `job "lint" with.workflow-sync = %s; want boolean true (add `+"`with: {workflow-sync: true}`"+` to run this check in CI)`, orAbsent(n))
	}
}

func orAbsent(n *yaml.Node) string {
	if n == nil {
		return "(absent)"
	}
	return fmt.Sprintf("%q", n.Value)
}

func (c *checker) ciAutomerge(order []ciJob, byName map[string]ciJob) {
	j, ok := byName["automerge"]
	if !ok {
		return
	}
	if last := order[len(order)-1]; last.name != "automerge" {
		c.add(ciFile, j.line(), ruleCIAutomerge, `job "automerge" is not the last job (%q is); move it last so the "needs lists every other job" invariant is easy to review`, last.name)
	}
	others := map[string]bool{}
	for _, o := range order {
		if o.name != "automerge" {
			others[o.name] = true
		}
	}
	need, ok := strSet(val(j.node, "needs"))
	if !ok || !sameSet(need, others) {
		var missing, extra []string
		for _, n := range sortedKeys(others) {
			if !need[n] {
				missing = append(missing, n)
			}
		}
		for _, n := range sortedKeys(need) {
			if !others[n] {
				extra = append(extra, n)
			}
		}
		c.add(ciFile, line(val(j.node, "needs"), j.line()), ruleCIAutomerge,
			`job "automerge" needs = %s; want every other job %s (missing: [%s], unknown: [%s]); a job left out cannot block the merge`,
			fmtSet(need), fmtSet(others), strings.Join(missing, ", "), strings.Join(extra, ", "))
	}
	if n := val(j.node, "if"); n == nil || normExpr(scalar(n)) != AutomergeIf {
		c.add(ciFile, line(n, j.line()), ruleCIAutomerge, `job "automerge" if = %q; want %q`, scalar(n), AutomergeIf)
	}
	k, n := get(j.node, "permissions")
	c.wantPerms(ciFile, line(k, j.line()), ruleCIAutomerge, `job "automerge"`, n, PermsAutomergeJob)
}
