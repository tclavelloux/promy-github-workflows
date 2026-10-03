// Command workflow-sync checks a repo's CI wiring against the fleet policy.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tclavelloux/promy-github-workflows/internal/syncheck"
)

func main() {
	root := flag.String("root", ".", "repository root to check")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "workflow-sync: unexpected argument %q\nusage: workflow-sync [--root DIR]\n", flag.Arg(0))
		os.Exit(2)
	}

	rep, err := syncheck.Run(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow-sync: %v\n", err)
		os.Exit(2)
	}
	rep.Render(os.Stdout)
	if !rep.OK() {
		os.Exit(1)
	}
}
