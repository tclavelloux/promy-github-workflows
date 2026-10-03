package syncheck

import (
	"fmt"
	"regexp"
	"strings"
)

// markerRe works on any text file with `#` comments (YAML, Makefile).
var markerRe = regexp.MustCompile(`#\s*sync-exempt(-file)?:(.*)$`)

type marker struct {
	line      int
	fileScope bool
	rule      string
	reason    string
	used      bool
}

// parseMarkers returns the valid markers of one file plus bad-exemption findings.
func parseMarkers(file string, lines []string) ([]*marker, []Finding) {
	var ms []*marker
	var bad []Finding
	for i, l := range lines {
		m := markerRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		kind := "sync-exempt"
		if m[1] != "" {
			kind = "sync-exempt-file"
		}
		fields := strings.Fields(m[2])
		fail := func(msg string) {
			bad = append(bad, Finding{file, i + 1, ruleBadExemption, msg})
		}
		switch {
		case len(fields) == 0:
			fail(fmt.Sprintf("%s marker has no rule id; want `# %s: <rule-id> <reason>`", kind, kind))
		case !rules[fields[0]]:
			fail(fmt.Sprintf("%s names unknown rule %q; known rules: %s", kind, fields[0], strings.Join(sortedKeys(rules), ", ")))
		case fields[0] == ruleBadExemption || fields[0] == ruleStaleExemption:
			fail(fmt.Sprintf("rule %q cannot be exempted", fields[0]))
		case len(fields) == 1:
			fail(fmt.Sprintf("%s for %q has no reason; want `# %s: %s <reason>`", kind, fields[0], kind, fields[0]))
		default:
			ms = append(ms, &marker{
				line: i + 1, fileScope: m[1] != "", rule: fields[0],
				reason: strings.Join(fields[1:], " "),
			})
		}
	}
	return ms, bad
}

// candidateLines are the lines whose line-scoped markers cover a finding at
// line: the line itself plus the contiguous comment-only block directly above.
func candidateLines(lines []string, line int) map[int]bool {
	c := map[int]bool{line: true}
	for i := line - 1; i >= 1 && i <= len(lines); i-- {
		if !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "#") {
			break
		}
		c[i] = true
	}
	return c
}

// applyExemptions removes exempted findings and appends bad/stale marker findings.
func applyExemptions(files map[string][]string, findings []Finding) Report {
	markers := map[string][]*marker{}
	var extra []Finding
	for file, lines := range files {
		ms, bad := parseMarkers(file, lines)
		markers[file] = ms
		extra = append(extra, bad...)
	}

	var rep Report
	for _, f := range findings {
		var hit *marker
		{
			cand := candidateLines(files[f.File], f.Line)
			for _, m := range markers[f.File] {
				if m.rule != f.Rule {
					continue
				}
				if m.fileScope || (f.Line > 0 && cand[m.line]) {
					m.used = true
					if hit == nil {
						hit = m
					}
				}
			}
		}
		if hit == nil {
			rep.Violations = append(rep.Violations, f)
			continue
		}
		rep.Exemptions = append(rep.Exemptions, Applied{f, hit.reason})
	}

	for file, ms := range markers {
		for _, m := range ms {
			if !m.used {
				extra = append(extra, Finding{file, m.line, ruleStaleExemption,
					fmt.Sprintf("exemption for %q matched no finding; remove it", m.rule)})
			}
		}
	}
	rep.Violations = append(rep.Violations, extra...)
	sortFindings(rep.Violations)
	sortApplied(rep.Exemptions)
	return rep
}
