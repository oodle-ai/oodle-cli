package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// inlineFilterRule is a rule whose top-level filter is one inline condition.
// The generated LogFilter type has no fields for this form.
const inlineFilterRule = `{"name":"errors","filter":{"field":"message","operator":"matches regex","value":"fail(ed)? for (\\S+)"},` +
	`"labels":[{"name":"user","valueExtractor":{"field":"message","regex":"for (\\S+)"}}],` +
	`"metricDefinitions":[{"name":"errors","type":"log_count"}]}`

// logMetricsServer records the request body and replies with respBody.
func logMetricsServer(t *testing.T, respBody string) (*httptest.Server, *[]byte, *string) {
	t.Helper()
	var gotBody []byte
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, &gotBody, &gotPath
}

func runLogMetricsCmd(t *testing.T, srvURL string, cmd *cobra.Command, format output.Format, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := withClient(context.Background(), newTestClient(t, srvURL))
	ctx = withOutput(ctx, format)
	ctx = withInstance(ctx, "inst")
	cmd.SetContext(ctx)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}
	err := cmd.RunE(cmd, cmd.Flags().Args())
	return buf.String(), err
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("invalid JSON %q: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

func TestLogMetricsCreate_SendsInlineFilterUnchanged(t *testing.T) {
	srv, gotBody, gotPath := logMetricsServer(t, `{"id":"r1","name":"errors"}`)
	path := writeTempFile(t, "rule.json", inlineFilterRule)

	out, err := runLogMetricsCmd(t, srv.URL, newLogMetricsCreateCmd(), output.FormatTable, "-f", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *gotPath != "POST /v1/api/instance/inst/logmetrics" {
		t.Errorf("request = %q", *gotPath)
	}
	if string(*gotBody) != inlineFilterRule {
		t.Errorf("request body changed:\n got: %s\nwant: %s", *gotBody, inlineFilterRule)
	}
	if !strings.Contains(out, "errors") || !strings.Contains(out, "r1") {
		t.Errorf("table output does not show the rule:\n%s", out)
	}
}

func TestLogMetricsUpdate_YAMLConvertsToJSON(t *testing.T) {
	srv, gotBody, gotPath := logMetricsServer(t, `{"id":"r1","name":"errors"}`)
	yamlRule := `name: errors
filter:
  field: message
  operator: matches regex
  value: 'fail(ed)? for (\S+)'
labels:
  - name: user
    valueExtractor:
      field: message
      regex: 'for (\S+)'
metricDefinitions:
  - name: errors
    type: log_count
`
	path := writeTempFile(t, "rule.yaml", yamlRule)

	if _, err := runLogMetricsCmd(t, srv.URL, newLogMetricsUpdateCmd(), output.FormatJSON, "-f", path, "r1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *gotPath != "PUT /v1/api/instance/inst/logmetrics/r1" {
		t.Errorf("request = %q", *gotPath)
	}
	if !jsonEqual(t, *gotBody, []byte(inlineFilterRule)) {
		t.Errorf("YAML was not converted to the same JSON:\n got: %s\nwant: %s", *gotBody, inlineFilterRule)
	}
}

func TestLogMetricsCreate_RejectsNonObject(t *testing.T) {
	srv, _, gotPath := logMetricsServer(t, `{}`)
	path := writeTempFile(t, "rule.json", `[1, 2]`)
	_, err := runLogMetricsCmd(t, srv.URL, newLogMetricsCreateCmd(), output.FormatJSON, "-f", path)
	if err == nil || !strings.Contains(err.Error(), "object") {
		t.Fatalf("expected an error for a non-object file, got %v", err)
	}
	if *gotPath != "" {
		t.Errorf("request was sent for an invalid file")
	}
}

func TestLogMetricsGet_PrintsInlineFilterUnchanged(t *testing.T) {
	resp := `{"id":"r1",` + inlineFilterRule[1:]
	srv, _, gotPath := logMetricsServer(t, resp)

	out, err := runLogMetricsCmd(t, srv.URL, newLogMetricsGetCmd(), output.FormatJSON, "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *gotPath != "GET /v1/api/instance/inst/logmetrics/r1" {
		t.Errorf("request = %q", *gotPath)
	}
	if !jsonEqual(t, []byte(out), []byte(resp)) {
		t.Errorf("JSON output is not the response:\n got: %s\nwant: %s", out, resp)
	}

	out, err = runLogMetricsCmd(t, srv.URL, newLogMetricsGetCmd(), output.FormatYAML, "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"operator: matches regex", "field: message"} {
		if !strings.Contains(out, want) {
			t.Errorf("YAML output does not contain %q:\n%s", want, out)
		}
	}
}

func TestLogMetricsList_RawJSONAndTable(t *testing.T) {
	resp := `[{"id":"r1",` + inlineFilterRule[1:] + `,{"id":"r2","name":"other","filter":{"all":[]}}]`
	srv, _, _ := logMetricsServer(t, resp)

	out, err := runLogMetricsCmd(t, srv.URL, newLogMetricsListCmd(), output.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !jsonEqual(t, []byte(out), []byte(resp)) {
		t.Errorf("JSON output is not the response:\n%s", out)
	}

	out, err = runLogMetricsCmd(t, srv.URL, newLogMetricsListCmd(), output.FormatTable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"NAME", "ID", "errors", "r1", "other", "r2"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output does not contain %q:\n%s", want, out)
		}
	}
}

func TestReadInputFileJSON_UnknownExtension(t *testing.T) {
	jsonPath := writeTempFile(t, "rule.txt", inlineFilterRule)
	got, err := readInputFileJSON(jsonPath)
	if err != nil || string(got) != inlineFilterRule {
		t.Errorf("JSON in a .txt file: got %s, %v", got, err)
	}

	yamlPath := writeTempFile(t, "rule", "name: a\nfilter:\n  field: level\n  operator: exists\n")
	got, err = readInputFileJSON(yamlPath)
	if err != nil {
		t.Fatalf("YAML without an extension: %v", err)
	}
	if !jsonEqual(t, got, []byte(`{"name":"a","filter":{"field":"level","operator":"exists"}}`)) {
		t.Errorf("YAML without an extension converted to %s", got)
	}
}
