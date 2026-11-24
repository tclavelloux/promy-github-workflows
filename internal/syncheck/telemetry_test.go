package syncheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	prodCfgRel = "configs/config.prod.yaml"
	baseCfgRel = "configs/config.yaml"
)

func TestProdTelemetryConfig(t *testing.T) {
	cases := []struct {
		name  string
		edits []edit
		do    func(t *testing.T, root string)
		rules []string
		file  string
		msg   string
	}{
		{name: "stdout in prod", edits: []edit{ed(prodCfgRel, "type: otlp-http", "type: stdout")}, rules: []string{ruleProdTelemetry}, file: prodCfgRel, msg: "`stdout`"},
		{name: "noop in prod", edits: []edit{ed(prodCfgRel, "type: otlp-http", "type: noop")}, rules: []string{ruleProdTelemetry}, msg: "`noop`"},
		{name: "otlp-grpc in prod", edits: []edit{ed(prodCfgRel, "type: otlp-http", "type: otlp-grpc")}, rules: []string{ruleProdTelemetry}, msg: "want `otlp-http`"},
		{name: "processor only in base falls back", edits: []edit{
			ed(prodCfgRel, "  trace:\n    processor:\n      type: otlp-http\n", ""),
			ed(baseCfgRel, "type: stdout", "type: otlp-http"),
		}},
		{name: "processor absent everywhere", edits: []edit{
			ed(prodCfgRel, "  trace:\n    processor:\n      type: otlp-http\n", ""),
			ed(baseCfgRel, "  trace:\n    processor:\n      type: stdout\n", ""),
		}, rules: []string{ruleProdTelemetry}, msg: "is absent"},
		{name: "base stdout reported on base file", edits: []edit{ed(prodCfgRel, "  trace:\n    processor:\n      type: otlp-http\n", "")}, rules: []string{ruleProdTelemetry}, file: baseCfgRel, msg: "`stdout`"},
		{name: "sql arguments true in prod", edits: []edit{ed(prodCfgRel, "arguments: false", "arguments: true")}, rules: []string{ruleProdTelemetry}, msg: "personal data"},
		{name: "sql arguments inherited from base", edits: []edit{ed(prodCfgRel, "  sql:\n    log:\n      arguments: false\n", "")}, rules: []string{ruleProdTelemetry}, file: baseCfgRel, msg: "modules.sql.log.arguments is `true`"},
		{name: "no sql module skips the sql check", edits: []edit{
			ed(prodCfgRel, "  sql:\n    log:\n      arguments: false\n", ""),
			ed(baseCfgRel, "  sql:\n    driver: mysql\n    log:\n      enabled: true\n      arguments: true\n", ""),
		}},
		{name: "level_from_response false", edits: []edit{ed(prodCfgRel, "level_from_response: true", "level_from_response: false")}, rules: []string{ruleProdTelemetry}, msg: "level_from_response"},
		{name: "no prod config is a library", do: func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, prodCfgRel)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unparsable prod config", do: func(t *testing.T, r string) { writeFile(t, r, prodCfgRel, "modules: [unclosed\n") }, rules: []string{ruleYAMLParse}},
		{name: "exemption marker", edits: []edit{ed(prodCfgRel, "      type: otlp-http", "      type: stdout # sync-exempt: prod-telemetry-config tracked in #1")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, "pass-service")
			for _, e := range tc.edits {
				replace(t, root, e.file, e.old, e.new)
			}
			if tc.do != nil {
				tc.do(t, root)
			}
			rep := run(t, root)
			if got := ruleIDs(rep.Violations); !equalStrings(got, tc.rules) {
				t.Fatalf("rules = %v, want %v\n%s", got, tc.rules, joinMessages(rep.Violations))
			}
			if tc.msg != "" && !strings.Contains(joinMessages(rep.Violations), tc.msg) {
				t.Errorf("no message contains %q:\n%s", tc.msg, joinMessages(rep.Violations))
			}
			if tc.file != "" {
				for _, f := range rep.Violations {
					if f.File != tc.file {
						t.Errorf("finding on %s, want %s", f.File, tc.file)
					}
				}
			}
		})
	}
}
