package syncheck

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestGoTestWorkflowInputs(t *testing.T) {
	in := path(govYAML(t, prScopeGoTest), "on", "workflow_call", "inputs")
	got := keys(in)
	sort.Strings(got)
	want := []string{"go-version-file", "redis", "timeout-minutes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: inputs = %v, want exactly %v", prScopeGoTest, got, want)
	}
	r := val(in, "redis")
	if got := scalar(val(r, "type")); got != "boolean" {
		t.Errorf("%s: inputs.redis.type = %q, want %q", prScopeGoTest, got, "boolean")
	}
	if d := val(r, "default"); !isBool(d, false) {
		t.Errorf("%s: inputs.redis.default = %q, want boolean false", prScopeGoTest, scalar(d))
	}
	if req := val(r, "required"); req != nil && !isBool(req, false) {
		t.Errorf("%s: inputs.redis.required = %q, want false or absent", prScopeGoTest, scalar(req))
	}
}

func TestGoTestWorkflowRedisIsOptInStep(t *testing.T) {
	doc := govYAML(t, prScopeGoTest)
	job := path(doc, "jobs", "test")
	for _, k := range []string{"services", "env"} {
		if kn, _ := get(job, k); kn != nil {
			t.Errorf("%s: job test has a %q key, want none (Redis is a step so the default-off path is unchanged)", prScopeGoTest, k)
		}
	}

	steps := val(job, "steps").Content
	idx := func(pred func(n string, s string) bool) int {
		for i, s := range steps {
			s = deref(s)
			if pred(scalar(val(s, "name")), scalar(val(s, "uses"))) {
				return i
			}
		}
		return -1
	}
	setup := idx(func(_, u string) bool { return strings.HasPrefix(u, "actions/setup-go@") })
	start := idx(func(n, _ string) bool { return n == "Start Redis" })
	run := idx(func(n, _ string) bool { return n == "Run tests" })
	if setup < 0 || start < 0 || run < 0 {
		t.Fatalf("%s: step indexes setup-go=%d, Start Redis=%d, Run tests=%d, want all present", prScopeGoTest, setup, start, run)
	}
	if setup >= start || start != run-1 {
		t.Errorf("%s: step order setup-go=%d, Start Redis=%d, Run tests=%d, want setup-go before Start Redis and Start Redis immediately before Run tests", prScopeGoTest, setup, start, run)
	}

	if kn, _ := get(deref(steps[run]), "env"); kn != nil {
		t.Errorf("%s: step Run tests has an env key, want none (REDIS_TEST_DSN arrives through GITHUB_ENV)", prScopeGoTest)
	}

	s := deref(steps[start])
	cond := scalar(val(s, "if"))
	for _, w := range []string{"inputs.redis", "steps.scope.outputs.skip"} {
		if !strings.Contains(cond, w) {
			t.Errorf("%s: step Start Redis if = %q, want it to contain %q", prScopeGoTest, cond, w)
		}
	}
	if got := scalar(path(s, "env", "REDIS_IMAGE")); got != "redis:7" {
		t.Errorf("%s: step Start Redis env.REDIS_IMAGE = %q, want %q", prScopeGoTest, got, "redis:7")
	}
	body := scalar(val(s, "run"))
	for _, w := range []string{"REDIS_TEST_DSN=redis://localhost:6379/15", `>> "$GITHUB_ENV"`, `--health-cmd "redis-cli ping"`} {
		if !strings.Contains(body, w) {
			t.Errorf("%s: step Start Redis run lacks %q", prScopeGoTest, w)
		}
	}
	if strings.Contains(body, "${{") {
		t.Errorf("%s: step Start Redis run contains \"${{\", want no expression interpolation in run", prScopeGoTest)
	}
}
