// Package syncheck verifies that a repo's CI wiring matches the fleet policy.
package syncheck

import (
	"fmt"
	"sort"
)

// Finding is one policy violation. Line 0 means "whole file" or "missing file".
type Finding struct {
	File    string // repo-relative, forward slashes
	Line    int
	Rule    string
	Message string
}

// Applied is a finding that a sync-exempt marker suppressed.
type Applied struct {
	Finding
	Reason string
}

// Report is the outcome of one Run.
type Report struct {
	Violations []Finding
	Exemptions []Applied
}

// rules is the registry of every valid rule ID. Exemptions naming an ID that is
// not here are rejected, so each check file registers its own IDs.
var rules = map[string]bool{}

// register adds rule IDs to the registry; call it as `var _ = register(...)`.
func register(ids ...string) int {
	for _, id := range ids {
		rules[id] = true
	}
	return len(ids)
}

// Rules the engine itself emits. They can never be exempted.
const (
	ruleBadExemption   = "bad-exemption"
	ruleStaleExemption = "stale-exemption"
	ruleYAMLParse      = "yaml-parse"
)

var _ = register(ruleBadExemption, ruleStaleExemption, ruleYAMLParse)

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s", f.File, f.Line, f.Rule, f.Message)
}

func sortApplied(as []Applied) {
	sort.SliceStable(as, func(i, j int) bool {
		a, b := as[i], as[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Rule < b.Rule
	})
}
