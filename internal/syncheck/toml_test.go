package syncheck

import (
	"strings"
	"testing"
)

func TestParseTOMLValues(t *testing.T) {
	const doc = `# comment
title = "a \"quoted\" \u00e9 string" # trailing comment
lit = 'C:\path'
ml = """
first \
   second"""
mll = '''
raw \n'''
n = 1_000
f = -2.5e3
yes = true
day = 1979-05-27
stamp = 1979-05-27 07:32:00Z
arr = [ 1, [2, 3], { a = "]" }, # comment
  "x,y", ]
tool.dotted."quoted.key" = 4

[tool.coverage.report]
fail_under = 94
inline = { x = 1, y = { z = "deep" } }

[[IgnoredVulns]]
id = "A"
ignoreUntil = 2027-01-13

[[IgnoredVulns]]
id = "B"
[IgnoredVulns.sub]
k = "v"

[after]
k = "table"
`
	d, err := parseTOML([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]struct {
		kind tomlKind
		str  string
	}{
		"title":                           {tomlString, `a "quoted" é string`},
		"lit":                             {tomlString, `C:\path`},
		"ml":                              {tomlString, "first second"},
		"mll":                             {tomlString, `raw \n`},
		"n":                               {tomlInt, "1_000"},
		"f":                               {tomlFloat, "-2.5e3"},
		"yes":                             {tomlBool, "true"},
		"day":                             {tomlDate, "1979-05-27"},
		"stamp":                           {tomlDate, "1979-05-27 07:32:00Z"},
		"arr":                             {tomlArray, "[ 1, [2, 3], { a = \"]\" }, # comment\n  \"x,y\", ]"},
		"tool.dotted.quoted.key":          {tomlInt, "4"},
		"tool.coverage.report.fail_under": {tomlInt, "94"},
		"tool.coverage.report.inline.y.z": {tomlString, "deep"},
		"after.k":                         {tomlString, "table"},
	} {
		got, ok := d.values[key]
		if !ok || got.kind != want.kind || got.str != want.str {
			t.Errorf("%s = %+v (present %v), want %v %q", key, got, ok, want.kind, want.str)
		}
	}
	if d.tables["tool.coverage.report"] != 18 {
		t.Errorf("[tool.coverage.report] line = %d, want 18", d.tables["tool.coverage.report"])
	}
	iv := d.arrays["IgnoredVulns"]
	if len(iv) != 2 || iv[0].line != 22 || iv[0].values["id"].str != "A" || iv[1].values["sub.k"].str != "v" {
		t.Fatalf("IgnoredVulns = %+v", iv)
	}
	if day, ok := tomlDay(iv[0].values["ignoreUntil"]); !ok || day.Format("2006-01-02") != "2027-01-13" {
		t.Errorf("tomlDay = %v %v", day, ok)
	}
	if v, ok := tomlNumber(d.values["n"]); !ok || v != 1000 {
		t.Errorf("tomlNumber(n) = %v %v", v, ok)
	}
}

func TestParseTOMLErrors(t *testing.T) {
	cases := []struct{ name, doc, msg string }{
		{"unterminated table", "[tool\n", "line 1: unterminated table header"},
		{"missing equals", "a b\n", "expected ="},
		{"two values", "a = 1 2\n", "line 1: expected end of line"},
		{"bare word", "a = maybe\n", `invalid value "maybe"`},
		{"unterminated string", "a = \"x\nb = 1\n", "unterminated string"},
		{"unterminated array", "a = [1,\n", "unterminated array"},
		{"bad escape", `a = "\q"`, `invalid escape \q`},
		{"missing value", "a =\n", "missing value"},
		{"error line", "a = 1\n\nb = [\n 1\n 2]\n", "line 5: expected , or ]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseTOML([]byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want %q", err, tc.msg)
			}
		})
	}
}
