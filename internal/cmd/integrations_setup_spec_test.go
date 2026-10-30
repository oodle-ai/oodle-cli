package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testSetupSpec() map[string]interface{} {
	return map[string]interface{}{
		"type": "ecs",
		"setupMethods": map[string]interface{}{
			"firelens": map[string]interface{}{
				"label":       "FireLens",
				"recommended": true,
				"whenToUse":   "ECS tasks",
				"steps": []interface{}{
					map[string]interface{}{
						"code": []interface{}{
							"Host {{ logsCollectorDomain }}",
							"X-OODLE-INSTANCE {{instanceId}}{{instanceId}}",
							"X-API-KEY {{apiKey}}",
							"token: ${{ secrets.OODLE_API_KEY }}",
							"id: ${{ instanceId }}",
							"cluster: {{ecsClusterName}}",
						},
					},
				},
			},
			"datadog": map[string]interface{}{
				"label": "Datadog",
				"steps": []interface{}{"x"},
			},
		},
	}
}

func TestFillSetupSpec(t *testing.T) {
	spec := testSetupSpec()
	filled := fillSetupSpec(spec, map[string]string{
		"instanceId":          "inst-a",
		"logsCollectorDomain": "inst-a-logs.collector.oodle.ai",
	})
	if want := []string{"instanceId", "logsCollectorDomain"}; !reflect.DeepEqual(filled, want) {
		t.Errorf("filled = %v, want %v", filled, want)
	}

	methods := spec["setupMethods"].(map[string]interface{})
	steps := methods["firelens"].(map[string]interface{})["steps"].([]interface{})
	code := steps[0].(map[string]interface{})["code"].([]interface{})
	want := []interface{}{
		"Host inst-a-logs.collector.oodle.ai",
		"X-OODLE-INSTANCE inst-ainst-a",
		// The key is a secret, and a customer value is for the agent to
		// find, so both stay as they are.
		"X-API-KEY {{apiKey}}",
		// GitHub Actions syntax is not a spec placeholder.
		"token: ${{ secrets.OODLE_API_KEY }}",
		"id: ${{ instanceId }}",
		"cluster: {{ecsClusterName}}",
	}
	if !reflect.DeepEqual(code, want) {
		t.Errorf("code = %#v, want %#v", code, want)
	}
}

func TestSummarizeSetupSpec(t *testing.T) {
	spec := testSetupSpec()
	summarizeSetupSpec(spec)
	methods := spec["setupMethods"].(map[string]interface{})
	firelens := methods["firelens"].(map[string]interface{})
	if _, ok := firelens["steps"]; ok {
		t.Error("summary kept the steps")
	}
	if firelens["recommended"] != true || firelens["whenToUse"] != "ECS tasks" {
		t.Errorf("summary lost the choice fields: %v", firelens)
	}
}

func TestSelectSetupMethod(t *testing.T) {
	spec := testSetupSpec()
	if err := selectSetupMethod(spec, "datadog"); err != nil {
		t.Fatal(err)
	}
	methods := spec["setupMethods"].(map[string]interface{})
	if len(methods) != 1 || methods["datadog"] == nil {
		t.Errorf("methods = %v, want only datadog", methods)
	}

	err := selectSetupMethod(testSetupSpec(), "nope")
	if err == nil || !strings.Contains(err.Error(), "datadog, firelens") {
		t.Errorf("err = %v, want the list of methods", err)
	}
}

// TestIntegrationsGetSetupSpec_FillsInstanceValues runs the command with a
// login and checks that it fills the instance values from the integration
// records, and leaves the API key as a placeholder.
func TestIntegrationsGetSetupSpec_FillsInstanceValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/setup-spec"):
			fmt.Fprint(w, `{"type":"otel","setupMethods":{"collector":{"label":"Collector","steps":[{"code":["endpoint: https://{{otlpCollectorDomain}}","X-OODLE-INSTANCE: {{instanceId}}","X-API-KEY: {{apiKey}}"]}]},"sdk":{"label":"SDK","steps":[]}}}`)
		case strings.HasSuffix(r.URL.Path, "/integrations"):
			fmt.Fprint(w, `[{"type":"OTEL","otlpCollectorDomain":"inst-a-otlp.collector.oodle.ai","collectorDomain":"inst-a.collector.oodle.ai"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("OODLE_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("OODLE_OAUTH_ACCESS_TOKEN", "")
	t.Setenv("OODLE_API_KEY", "test-key")
	t.Setenv("OODLE_INSTANCE", "inst-a")

	root := NewRootCmd()
	root.SetArgs([]string{"integrations", "get-setup-spec", "otel", "--method", "collector", "--api-url", srv.URL, "-o", "json"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"https://inst-a-otlp.collector.oodle.ai",
		"X-OODLE-INSTANCE: inst-a",
		"X-API-KEY: {{apiKey}}",
		`"filledValues"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"sdk"`) {
		t.Errorf("--method kept the other methods:\n%s", out)
	}
}

func TestMissingDomainValues(t *testing.T) {
	got := missingDomainValues(map[string]string{
		"collectorDomain": "c.example",
		"apiDomain":       "a.example",
	})
	want := []string{"logsCollectorDomain", "otlpCollectorDomain"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("missingDomainValues = %v, want %v", got, want)
	}
	if got := missingDomainValues(map[string]string{
		"collectorDomain":     "c",
		"logsCollectorDomain": "l",
		"otlpCollectorDomain": "o",
		"apiDomain":           "a",
	}); len(got) != 0 {
		t.Fatalf("missingDomainValues = %v, want none", got)
	}
}
