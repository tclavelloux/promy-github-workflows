package syncheck

import "go.yaml.in/yaml/v3"

const (
	ruleProdTelemetry = "prod-telemetry-config"

	baseConfigFile = "configs/config.yaml"
	prodConfigFile = "configs/config.prod.yaml"
)

var _ = register(ruleProdTelemetry)

// checkProdTelemetry enforces the mechanically checkable part of the prod
// observability bar on the config Yokai runs with in prod: APP_ENV=prod merges
// configs/config.prod.yaml over configs/config.yaml, so a key absent from the
// overlay falls back to the base file. A repo without config.prod.yaml (a
// library) is out of scope.
func (c *checker) checkProdTelemetry() {
	prod := c.loadYAML(prodConfigFile)
	if !prod.exists || prod.root == nil {
		return // absent: library; unparsable: yaml-parse already reported
	}
	base := c.loadYAML(baseConfigFile)

	effective := func(keys ...string) (*yaml.Node, string) {
		if n := path(prod.root, keys...); n != nil {
			return n, prodConfigFile
		}
		if n := path(base.root, keys...); n != nil {
			return n, baseConfigFile
		}
		return nil, prodConfigFile
	}
	shown := func(n *yaml.Node) string {
		if n == nil {
			return "absent"
		}
		return "`" + n.Value + "`"
	}

	if n, f := effective("modules", "trace", "processor", "type"); n == nil || n.Value != ProdTraceProcessor {
		c.add(f, line(n, 1), ruleProdTelemetry,
			"modules.trace.processor.type is %s in prod; want `%s` (endpoint from the platform-injected OTEL_EXPORTER_OTLP_*): `noop` drops spans, `stdout` only prints them",
			shown(n), ProdTraceProcessor)
	}

	if sql, _ := effective("modules", "sql"); sql != nil {
		if n, f := effective("modules", "sql", "log", "arguments"); n == nil || n.Value != "false" {
			c.add(f, line(n, 1), ruleProdTelemetry,
				"modules.sql.log.arguments is %s in prod; want `false`: SQL arguments carry personal data", shown(n))
		}
	}

	if n, f := effective("modules", "http", "server", "log", "level_from_response"); n == nil || n.Value != "true" {
		c.add(f, line(n, 1), ruleProdTelemetry,
			"modules.http.server.log.level_from_response is %s in prod; want `true` so 5xx requests log at error level", shown(n))
	}
}
