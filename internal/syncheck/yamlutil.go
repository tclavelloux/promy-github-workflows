package syncheck

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// parseYAML returns the document's root node (nil for an empty document).
func parseYAML(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil
	}
	return deref(doc.Content[0]), nil
}

var yamlErrLine = regexp.MustCompile(`line (\d+)`)

func yamlErrorLine(err error) int {
	if m := yamlErrLine.FindStringSubmatch(err.Error()); m != nil {
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		return n
	}
	return 0
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func isMap(n *yaml.Node) bool { return n != nil && n.Kind == yaml.MappingNode }
func isSeq(n *yaml.Node) bool { return n != nil && n.Kind == yaml.SequenceNode }

// get returns the key and value nodes for key in mapping n (nil, nil if absent).
func get(n *yaml.Node, key string) (k, v *yaml.Node) {
	if !isMap(n) {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], deref(n.Content[i+1])
		}
	}
	return nil, nil
}

func val(n *yaml.Node, key string) *yaml.Node { _, v := get(n, key); return v }

// path walks nested mapping keys.
func path(n *yaml.Node, keys ...string) *yaml.Node {
	for _, k := range keys {
		n = val(n, k)
	}
	return n
}

func keys(n *yaml.Node) []string {
	var out []string
	if isMap(n) {
		for i := 0; i < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	}
	return out
}

// line returns n's line, or fallback when n is nil.
func line(n *yaml.Node, fallback int) int {
	if n == nil {
		return fallback
	}
	return n.Line
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// isBool reports whether n is the YAML boolean want (not the string "true").
func isBool(n *yaml.Node, want bool) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Tag == "!!bool" && (n.Value == fmt.Sprint(want))
}

// strSet reads a scalar or a sequence of scalars as a set.
func strSet(n *yaml.Node) (map[string]bool, bool) {
	set := map[string]bool{}
	switch {
	case n == nil:
		return set, false
	case n.Kind == yaml.ScalarNode:
		set[n.Value] = true
	case isSeq(n):
		for _, c := range n.Content {
			c = deref(c)
			if c.Kind != yaml.ScalarNode {
				return set, false
			}
			set[c.Value] = true
		}
	default:
		return set, false
	}
	return set, true
}

func setOf(items ...string) map[string]bool {
	s := map[string]bool{}
	for _, i := range items {
		s[i] = true
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fmtSet(s map[string]bool) string { return "{" + strings.Join(sortedKeys(s), ", ") + "}" }

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

var wsRun = regexp.MustCompile(`\s+`)

// normExpr collapses whitespace and strips a surrounding ${{ }}.
func normExpr(s string) string {
	s = strings.TrimSpace(wsRun.ReplaceAllString(s, " "))
	if strings.HasPrefix(s, "${{") && strings.HasSuffix(s, "}}") {
		s = strings.TrimSpace(s[3 : len(s)-2])
	}
	return s
}

// readPerms reads a permissions mapping. ok is false when n is absent or not a
// mapping of scalars (e.g. `read-all`).
func readPerms(n *yaml.Node) (perms, bool) {
	if !isMap(n) {
		return nil, false
	}
	p := perms{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		p[n.Content[i].Value] = scalar(deref(n.Content[i+1]))
	}
	return p, true
}

func (p perms) String() string {
	parts := make([]string, 0, len(p))
	for _, k := range sortedKeys(p) {
		parts = append(parts, k+": "+p[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func (p perms) equal(q perms) bool {
	if len(p) != len(q) {
		return false
	}
	for k, v := range p {
		if q[k] != v {
			return false
		}
	}
	return true
}

// describePerms renders the node for a message.
func describePerms(n *yaml.Node) string {
	if p, ok := readPerms(n); ok {
		return p.String()
	}
	if n == nil {
		return "(absent)"
	}
	if s := scalar(n); s != "" {
		return s
	}
	return "(unreadable)"
}
