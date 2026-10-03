package syncheck

import (
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	ruleSHAPin       = "sha-pin"
	ruleGovRef       = "governance-ref"
	rulePersistCreds = "persist-credentials"
	ruleWFPerms      = "workflow-permissions"
	ruleDangerous    = "dangerous-trigger"
	ruleUnpinnedTool = "unpinned-tool"
)

var _ = register(ruleSHAPin, ruleGovRef, rulePersistCreds, ruleWFPerms, ruleDangerous, ruleUnpinnedTool)

var (
	sha40      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionCmt = regexp.MustCompile(`^#\s*v\d+(\.\d+){0,2}\b`)
	govPrefix  = GovernanceRepo + "/"
	govWFRe    = regexp.MustCompile(`^` + regexp.QuoteMeta(GovernanceRepo) + `/\.github/workflows/([^@]+)\.yml@(.+)$`)
)

// checkWorkflows applies the generic rules to every workflow file.
func (c *checker) checkWorkflows() {
	for _, rel := range c.workflowFiles() {
		if p := c.loadYAML(rel); p.root != nil {
			c.genericRules(rel, p.root)
		}
	}
}

func (c *checker) genericRules(rel string, root *yaml.Node) {
	jobsKey, jobs := get(root, "jobs")

	if val(root, "permissions") == nil {
		c.add(rel, line(jobsKey, 1), ruleWFPerms,
			"no top-level permissions: block; add `permissions: {contents: read}` so the token defaults to least privilege")
	}

	c.dangerousTriggers(rel, root)

	if !isMap(jobs) {
		return
	}
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		name, job := jobs.Content[i].Value, deref(jobs.Content[i+1])
		if uk, u := get(job, "uses"); u != nil {
			c.checkUses(rel, fmt.Sprintf("job %q", name), uk, u, true)
		}
		steps := val(job, "steps")
		if !isSeq(steps) {
			continue
		}
		for _, s := range steps.Content {
			c.checkStep(rel, name, deref(s))
		}
	}
}

func (c *checker) dangerousTriggers(rel string, root *yaml.Node) {
	// `on` is a plain string key in yaml.v3 Nodes (no YAML 1.1 bool coercion).
	_, on := get(root, "on")
	if on == nil {
		return
	}
	check := func(n *yaml.Node) {
		if n.Value == "pull_request_target" || n.Value == "workflow_run" {
			c.add(rel, n.Line, ruleDangerous,
				"trigger %q runs with the base repo's secrets and write token on untrusted input; use `pull_request`", n.Value)
		}
	}
	switch {
	case on.Kind == yaml.ScalarNode:
		check(on)
	case isSeq(on):
		for _, n := range on.Content {
			check(deref(n))
		}
	case isMap(on):
		for i := 0; i < len(on.Content); i += 2 {
			check(on.Content[i])
		}
	}
}

func (c *checker) checkStep(rel, jobName string, step *yaml.Node) {
	if !isMap(step) {
		return
	}
	if uk, u := get(step, "uses"); u != nil {
		c.checkUses(rel, fmt.Sprintf("job %q step", jobName), uk, u, false)
		if strings.HasPrefix(strings.ToLower(scalar(u)), "actions/checkout@") {
			if !isBool(path(step, "with", "persist-credentials"), false) {
				c.add(rel, u.Line, rulePersistCreds,
					"job %q: actions/checkout without `with: persist-credentials: false`; the token would stay in .git/config for later steps", jobName)
			}
		}
	}
	if r := val(step, "run"); r != nil && r.Kind == yaml.ScalarNode {
		c.checkRun(rel, jobName, r)
	}
}

// checkRun flags tool downloads that float. For literal blocks the finding lands
// on the offending line so a trailing exemption marker can target it.
func (c *checker) checkRun(rel, jobName string, r *yaml.Node) {
	for i, l := range strings.Split(r.Value, "\n") {
		if !strings.Contains(l, "releases/latest") && !strings.Contains(l, "@latest") {
			continue
		}
		ln := r.Line
		if r.Style&yaml.LiteralStyle != 0 {
			ln = r.Line + 1 + i
		}
		c.add(rel, ln, ruleUnpinnedTool,
			"job %q: run: uses a floating version (%q); pin an exact version and verify its checksum", jobName, strings.TrimSpace(l))
	}
}

// checkUses applies governance-ref or sha-pin to one uses: value.
func (c *checker) checkUses(rel, where string, key, u *yaml.Node, isJob bool) {
	v := u.Value
	switch {
	case strings.HasPrefix(v, govPrefix):
		c.checkGovRef(rel, where, u)
		return
	case strings.HasPrefix(v, "./") || strings.HasPrefix(v, "docker://"):
		return
	}
	at := strings.LastIndex(v, "@")
	if at < 0 {
		c.add(rel, u.Line, ruleSHAPin, "%s: uses %q has no @ref; pin to `@<40-hex sha> # vX.Y.Z`", where, v)
		return
	}
	ref := v[at+1:]
	if !sha40.MatchString(ref) {
		c.add(rel, u.Line, ruleSHAPin, "%s: uses %q is pinned to %q; want a 40-char lowercase commit SHA followed by `# vX.Y.Z`", where, v, ref)
		return
	}
	cmt := u.LineComment
	if cmt == "" && key != nil {
		cmt = key.LineComment
	}
	if !versionCmt.MatchString(strings.TrimSpace(cmt)) {
		c.add(rel, u.Line, ruleSHAPin, "%s: uses %q is SHA-pinned but has no trailing `# vX.Y.Z` comment (got %q); Dependabot and reviewers need the version", where, v, cmt)
	}
}

func (c *checker) checkGovRef(rel, where string, u *yaml.Node) {
	m := govWFRe.FindStringSubmatch(u.Value)
	if m == nil {
		c.add(rel, u.Line, ruleGovRef, "%s: uses %q is not of the form %s/.github/workflows/<name>.yml@<name>/v<major>", where, u.Value, GovernanceRepo)
		return
	}
	name, ref := m[1], m[2]
	major, ok := WorkflowMajors[name]
	if !ok {
		c.add(rel, u.Line, ruleGovRef, "%s: unknown governance workflow %q; known: %s", where, name, strings.Join(sortedKeys(WorkflowMajors), ", "))
		return
	}
	if want := fmt.Sprintf("%s/v%d", name, major); ref != want {
		c.add(rel, u.Line, ruleGovRef, "%s: uses ref %q; want %q (the moving major tag)", where, ref, want)
	}
}
