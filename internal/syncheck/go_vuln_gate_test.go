package syncheck

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	goVulnGateScript = "scripts/go-vuln-gate.sh"
	goVulnYML        = ".github/workflows/go-vuln.yml"
	goVulnHook       = "hooks/go-vuln.sh"
	goVulnFixtures   = "scripts/tests/fixtures/go_vuln_gate"
)

var (
	goVulnIDRe  = regexp.MustCompile(`^GO-[0-9]{4}-[0-9]{4,}$`)
	goVulnURLRe = regexp.MustCompile(`^https://github\.com/tclavelloux/[A-Za-z0-9._-]+/issues/[0-9]+$`)
)

// A reusable workflow cannot read its own files at the caller's pinned ref, so the
// gate is inlined. This keeps the inlined copy byte-identical to the tested script.
func TestGoVulnGateInlinedBodyMatchesScript(t *testing.T) {
	const marker = "go-vuln-gate body"
	want := markedBlock(t, string(govRead(t, goVulnGateScript)), marker, goVulnGateScript)
	got := markedBlock(t, string(govRead(t, goVulnYML)), marker, goVulnYML)
	if got != want {
		t.Errorf("%s inlines a different `%s` block than %s; regenerate the workflow block from the script", goVulnYML, marker, goVulnGateScript)
	}
}

// goVulnAcceptList returns the literal GO_VULN_ACCEPT env of the scan step.
func goVulnAcceptList(t *testing.T) string {
	t.Helper()
	steps := path(govYAML(t, goVulnYML), "jobs", "vuln", "steps")
	if !isSeq(steps) {
		t.Fatalf("%s: jobs.vuln.steps missing", goVulnYML)
	}
	found := ""
	n := 0
	for _, s := range steps.Content {
		if v := path(s, "env", "GO_VULN_ACCEPT"); v != nil {
			found = scalar(v)
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%s: want exactly one step with env.GO_VULN_ACCEPT, found %d", goVulnYML, n)
	}
	return found
}

func TestGoVulnAcceptList(t *testing.T) {
	list := goVulnAcceptList(t)
	limit := time.Now().UTC().AddDate(0, 0, OSVIgnoreMaxDays).Format("2006-01-02") // real clock: TestMain pins `now`
	seen := map[string]bool{}
	entries := 0
	for i, l := range strings.Split(list, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		entries++
		f := strings.Fields(l)
		if len(f) < 4 {
			t.Errorf("GO_VULN_ACCEPT line %d: want `<GO-ID> <expires> <issue URL> <reason>`, got %q", i+1, l)
			continue
		}
		if !goVulnIDRe.MatchString(f[0]) {
			t.Errorf("line %d: invalid ID %q", i+1, f[0])
		}
		if seen[f[0]] {
			t.Errorf("line %d: duplicate ID %s", i+1, f[0])
		}
		seen[f[0]] = true
		if _, err := time.Parse("2006-01-02", f[1]); err != nil {
			t.Errorf("line %d (%s): expiry %q is not a real YYYY-MM-DD date", i+1, f[0], f[1])
		} else if f[1] > limit {
			t.Errorf("line %d (%s): expiry %s is more than %d days ahead (limit %s)", i+1, f[0], f[1], OSVIgnoreMaxDays, limit)
		}
		if !goVulnURLRe.MatchString(f[2]) {
			t.Errorf("line %d (%s): tracking link %q is not an issue URL", i+1, f[0], f[2])
		}
	}
	if entries == 0 {
		t.Error("GO_VULN_ACCEPT has no entries; remove the env and the gate input rather than leave it empty")
	}
	// An already-expired entry is not flagged here: the runtime gate fails every Go PR instead.

	// No caller-controlled list (D2).
	in := path(govYAML(t, goVulnYML), "on", "workflow_call", "inputs")
	for _, k := range keys(in) {
		if strings.Contains(strings.ToLower(k), "accept") {
			t.Errorf("%s declares input %q; the accepted list must not be caller-controlled", goVulnYML, k)
		}
	}
	if scalar(path(in, "args", "default")) != "./..." {
		t.Errorf("%s args default changed; callers rely on ./...", goVulnYML)
	}
}

// The awk extractor the hook uses must yield exactly what the YAML parser does.
func TestGoVulnAcceptExtractorMatchesYAML(t *testing.T) {
	script := goVulnAbs(t, goVulnGateScript)
	out, err := exec.Command("bash", script, "accept-from-workflow", goVulnAbs(t, goVulnYML)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := goVulnAcceptList(t); string(out) != want {
		t.Errorf("awk extraction differs from YAML value\n got: %q\nwant: %q", out, want)
	}
}

func goVulnAbs(t *testing.T, rel string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// goVulnFake writes a fake govulncheck that insists on `-format json`, prints the
// fixture and exits $FAKE_RC.
func goVulnFake(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "fake-govulncheck")
	body := `#!/usr/bin/env bash
case " $* " in *" -format json "*) ;; *) echo "fake: -format json missing in: $*" >&2; exit 64 ;; esac
cat "$FIXTURE"
exit "${FAKE_RC:-0}"
`
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// echoOnly drops the stdlib findings so only the two echo advisories remain.
func goVulnEchoOnly(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("jq", "-c", `select((.finding.osv // "") != "GO-2026-6859")`, goVulnAbs(t, goVulnFixtures+"/product-real.json")).Output()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "echo-only.json")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGoVulnGate(t *testing.T) {
	script := goVulnAbs(t, goVulnGateScript)
	real := goVulnAbs(t, goVulnFixtures+"/product-real.json")
	echoOnly := goVulnEchoOnly(t)
	list := goVulnAcceptList(t)
	const issue = "https://github.com/tclavelloux/promy-github-workflows/issues/78"
	valid := func(id, exp string) string { return fmt.Sprintf("%s %s %s reason text", id, exp, issue) }

	cases := []struct {
		name     string
		fixture  string
		list     string
		today    string
		fakeRC   string
		args     []string
		wantRC   int
		contains []string
		absent   []string
		summary  []string
	}{
		{name: "real capture fails on stdlib only", fixture: real, list: list, today: "2026-11-11", wantRC: 1,
			contains: []string{"::error title=vulnerable dependency::GO-2026-6859", "::notice title=accepted advisory::GO-2026-6875 accepted until 2026-12-11", "::notice title=accepted advisory::GO-2026-6872 accepted until 2026-12-11", "https://pkg.go.dev/vuln/GO-2026-6859"},
			absent:   []string{"::error title=vulnerable dependency::GO-2026-6875", "::error title=vulnerable dependency::GO-2026-6872"}},
		{name: "echo_only", fixture: echoOnly, list: list, today: "2026-11-11", wantRC: 0,
			summary: []string{"GO-2026-6875", "GO-2026-6872", "2026-12-11", "accepted"}},
		{name: "echo only expired", fixture: echoOnly, list: list, today: "2026-12-12", wantRC: 1,
			contains: []string{"::error title=accepted advisory expired::GO-2026-6875", "::error title=accepted advisory expired::GO-2026-6872", "expired"}},
		{name: "expiry day itself", fixture: echoOnly, list: list, today: "2026-12-11", wantRC: 0},
		{name: "package-level finding only", fixture: goVulnAbs(t, goVulnFixtures+"/imported-only.json"), list: "", today: "2026-11-11", wantRC: 0},
		{name: "clean", fixture: goVulnAbs(t, goVulnFixtures+"/clean.json"), list: list, today: "2026-11-11", wantRC: 0,
			summary: []string{"not reached"}},
		{name: "scanner error", fixture: echoOnly, list: list, today: "2026-11-11", fakeRC: "1", wantRC: 2, contains: []string{"govulncheck failed"}},
		{name: "empty list", fixture: echoOnly, list: "", today: "2026-11-11", wantRC: 1,
			contains: []string{"GO-2026-6875 is called and not accepted", "GO-2026-6872 is called and not accepted"}},
		{name: "comments and blanks ignored", fixture: echoOnly, today: "2026-11-11", wantRC: 0,
			list: "# note\n\n" + valid("GO-2026-6875", "2026-12-11") + "\n  \n" + valid("GO-2026-6872", "2026-12-11") + "\n"},
		{name: "bad ID", fixture: echoOnly, list: valid("GO-26-1", "2026-12-11"), today: "2026-11-11", wantRC: 2, contains: []string{"line 1", "invalid advisory ID"}},
		{name: "bad date", fixture: echoOnly, list: "\n" + valid("GO-2026-6875", "2026-02-30"), today: "2026-11-11", wantRC: 2, contains: []string{"line 2", "not a real date"}},
		{name: "date format", fixture: echoOnly, list: valid("GO-2026-6875", "11/12/2026"), today: "2026-11-11", wantRC: 2, contains: []string{"line 1", "not YYYY-MM-DD"}},
		{name: "missing reason", fixture: echoOnly, list: "GO-2026-6875 2026-12-11 " + issue, today: "2026-11-11", wantRC: 2, contains: []string{"line 1", "reason is empty"}},
		{name: "non-issue URL", fixture: echoOnly, list: "GO-2026-6875 2026-12-11 https://example.com/x why", today: "2026-11-11", wantRC: 2, contains: []string{"line 1", "tracking link"}},
		{name: "duplicate", fixture: echoOnly, list: valid("GO-2026-6875", "2026-12-11") + "\n" + valid("GO-2026-6875", "2026-12-11"), today: "2026-11-11", wantRC: 2, contains: []string{"line 2", "duplicate"}},
		{name: "format arg refused", fixture: echoOnly, list: list, today: "2026-11-11", args: []string{"-format", "text"}, wantRC: 2, contains: []string{"owns the output format"}},
		{name: "json arg refused", fixture: echoOnly, list: list, today: "2026-11-11", args: []string{"-json"}, wantRC: 2, contains: []string{"owns the output format"}},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.name, " ", "_"), func(t *testing.T) {
			dir := t.TempDir()
			fake := goVulnFake(t, dir)
			sum := filepath.Join(dir, "summary.md")
			args := append([]string{script, fake}, tc.args...)
			if len(tc.args) == 0 {
				args = append(args, "./...")
			}
			cmd := exec.Command("bash", args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "FIXTURE="+tc.fixture, "GO_VULN_ACCEPT="+tc.list, "GO_VULN_TODAY="+tc.today,
				"GITHUB_STEP_SUMMARY="+sum, "FAKE_RC="+tc.fakeRC)
			out, err := cmd.CombinedOutput()
			rc := 0
			if ee, ok := err.(*exec.ExitError); ok {
				rc = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if rc != tc.wantRC {
				t.Fatalf("rc = %d, want %d\n%s", rc, tc.wantRC, trimTail(out))
			}
			for _, s := range tc.contains {
				if !strings.Contains(string(out), s) {
					t.Errorf("output lacks %q\n%s", s, trimTail(out))
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(string(out), s) {
					t.Errorf("output has %q\n%s", s, trimTail(out))
				}
			}
			if len(tc.summary) > 0 {
				b, _ := os.ReadFile(sum)
				for _, s := range tc.summary {
					if !strings.Contains(string(b), s) {
						t.Errorf("summary lacks %q:\n%s", s, b)
					}
				}
			}
		})
	}
}

func trimTail(b []byte) string {
	s := string(b)
	if len(s) > 3000 {
		return "..." + s[len(s)-3000:]
	}
	return s
}

// Runs the pre-push hook with a fake govulncheck pre-placed in the hook's cache.
func TestGoVulnHook(t *testing.T) {
	hook := goVulnAbs(t, goVulnHook)
	real := goVulnAbs(t, goVulnFixtures+"/product-real.json")
	echoOnly := goVulnEchoOnly(t)
	cases := []struct {
		name    string
		fixture string
		today   string
		wantRC  int
		want    string
	}{
		{"echo only passes", echoOnly, "2026-11-11", 0, ""},
		{"stdlib finding blocks", real, "2026-11-11", 1, "blocking: govulncheck reports called vulnerabilities"},
		{"expired acceptance blocks", echoOnly, "2026-12-12", 1, "blocking"},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.name, " ", "_"), func(t *testing.T) {
			repo, cache := t.TempDir(), t.TempDir()
			for _, c := range [][]string{{"init", "-q"}} {
				if out, err := exec.Command("git", append([]string{"-C", repo}, c...)...).CombinedOutput(); err != nil {
					t.Fatalf("git: %v\n%s", err, out)
				}
			}
			bin := filepath.Join(cache, "promy-go-vuln", "v9.9.9")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			src, err := os.ReadFile(goVulnFake(t, t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "govulncheck"), src, 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", hook)
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache, "GOVULNCHECK_VERSION=9.9.9", "GO_VULN_MODE=block",
				"GO_VULN_TODAY="+tc.today, "FIXTURE="+tc.fixture, "GITHUB_STEP_SUMMARY=")
			out, err := cmd.CombinedOutput()
			rc := 0
			if ee, ok := err.(*exec.ExitError); ok {
				rc = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if rc != tc.wantRC {
				t.Fatalf("rc = %d, want %d\n%s", rc, tc.wantRC, trimTail(out))
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("output lacks %q\n%s", tc.want, trimTail(out))
			}
		})
	}
}
