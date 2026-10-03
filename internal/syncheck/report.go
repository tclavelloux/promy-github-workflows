package syncheck

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// OK reports whether the run found no violations.
func (r Report) OK() bool { return len(r.Violations) == 0 }

// ghEscape applies GitHub workflow-command escaping to a message.
var ghEscape = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")

// Render writes the human report. With GITHUB_ACTIONS=true it also emits
// ::error annotations so findings land on the PR diff.
func (r Report) Render(w io.Writer) {
	gha := os.Getenv("GITHUB_ACTIONS") == "true"
	for _, f := range r.Violations {
		fmt.Fprintf(w, "%s:%d: [%s] %s\n", f.File, f.Line, f.Rule, f.Message)
		if gha {
			loc := "file=" + f.File
			if f.Line > 0 {
				loc += fmt.Sprintf(",line=%d", f.Line)
			}
			fmt.Fprintf(w, "::error %s,title=workflow-sync [%s]::%s\n", loc, f.Rule, ghEscape.Replace(f.Message))
		}
	}
	for _, e := range r.Exemptions {
		fmt.Fprintf(w, "workflow-sync: EXEMPT %s:%d [%s] %s\n", e.File, e.Line, e.Rule, e.Reason)
	}
	status := "OK"
	if !r.OK() {
		status = "FAIL"
	}
	fmt.Fprintf(w, "workflow-sync: %s — %d violation(s), %d exemption(s)\n", status, len(r.Violations), len(r.Exemptions))
}
