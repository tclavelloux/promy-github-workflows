package syncheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// checker holds the state of one Run. Check files (workflows.go, ci.go, ...)
// read files through it so every file they touch is scanned for exemptions.
type checker struct {
	root     string
	files    map[string][]string // rel path -> raw lines, for exemption scanning
	docs     map[string]*parsed  // rel path -> parse result cache
	findings []Finding
	ioErr    error
}

type parsed struct {
	exists bool
	root   *yaml.Node // nil on parse error or empty document
}

// Run checks whatever policy files exist under root.
func Run(root string) (Report, error) {
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return Report{}, fmt.Errorf("root %q is not a readable directory", root)
	}
	c := &checker{root: root, files: map[string][]string{}, docs: map[string]*parsed{}}

	c.checkWorkflows()
	c.checkCI()
	c.checkPRTitle()
	c.checkReleasePlease()
	c.checkVulnFix()
	c.checkDependabot()
	c.checkPreCommit()
	c.checkMakefile()
	c.checkRepoFiles()

	if c.ioErr != nil {
		return Report{}, c.ioErr
	}
	return applyExemptions(c.files, c.findings), nil
}

func (c *checker) add(file string, ln int, rule, format string, args ...any) {
	c.findings = append(c.findings, Finding{file, ln, rule, fmt.Sprintf(format, args...)})
}

// read returns the file's lines, recording it for exemption scanning.
func (c *checker) read(rel string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) && c.ioErr == nil {
			c.ioErr = err
		}
		return nil, false
	}
	c.files[rel] = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	return data, true
}

// loadYAML parses rel once. A parse error becomes a yaml-parse finding.
func (c *checker) loadYAML(rel string) *parsed {
	if p, ok := c.docs[rel]; ok {
		return p
	}
	p := &parsed{}
	c.docs[rel] = p
	data, ok := c.read(rel)
	if !ok {
		return p
	}
	p.exists = true
	root, err := parseYAML(data)
	if err != nil {
		msg := strings.TrimPrefix(err.Error(), "yaml: ")
		c.add(rel, yamlErrorLine(err), ruleYAMLParse, "invalid YAML: %s; fix the syntax so the other rules can run", msg)
		return p
	}
	// Every governed file is a YAML mapping. A file that exists but is empty,
	// comment-only or a bare scalar/list would otherwise make each rule's
	// `root == nil` early return pass silently and drop that file's gate.
	if root == nil || root.Kind != yaml.MappingNode {
		line := 1
		if root != nil {
			line = root.Line
		}
		c.add(rel, line, ruleYAMLParse, "file is empty or not a YAML mapping; a governed file with no content silently drops its gate, restore it or delete it")
		return p
	}
	p.root = root
	c.duplicateKeys(rel, root)
	return p
}

// duplicateKeys reports repeated mapping keys, which yaml.v3 Nodes accept
// silently but GitHub treats as an error (or last-wins).
func (c *checker) duplicateKeys(rel string, n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if seen[k.Value] {
				c.add(rel, k.Line, ruleYAMLParse, "duplicate key %q in mapping; remove or merge the repeated entry", k.Value)
			}
			seen[k.Value] = true
		}
	}
	if n.Kind != yaml.AliasNode {
		for _, ch := range n.Content {
			c.duplicateKeys(rel, ch)
		}
	}
}

func (c *checker) isRegularFile(rel string) bool {
	st, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(rel)))
	return err == nil && st.Mode().IsRegular()
}

// workflowFiles lists .github/workflows/*.yml|*.yaml, sorted.
func (c *checker) workflowFiles() []string {
	dir := filepath.Join(c.root, ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		ext := filepath.Ext(e.Name())
		if !e.IsDir() && (ext == ".yml" || ext == ".yaml") {
			out = append(out, ".github/workflows/"+e.Name())
		}
	}
	sort.Strings(out)
	return out
}
