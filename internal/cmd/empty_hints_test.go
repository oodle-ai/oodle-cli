package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/config"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// runCmdSplit runs cmd against a test server and returns stdout and stderr
// apart, so a test can check that a hint does not change -o json output.
func runCmdSplit(t *testing.T, srvURL string, cmd *cobra.Command, format output.Format, args ...string) (string, string, error) {
	t.Helper()
	c, err := api.NewClient(&config.Config{APIURL: srvURL + "/", APIKey: "test-key"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	ctx := withClient(context.Background(), c)
	ctx = withOutput(ctx, format)
	ctx = withInstance(ctx, "inst")
	cmd.SetContext(ctx)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}
	err = cmd.RunE(cmd, cmd.Flags().Args())
	return stdout.String(), stderr.String(), err
}

func TestEmptyResultHints(t *testing.T) {
	logsFile := filepath.Join(t.TempDir(), "q.ndjson")
	if err := os.WriteFile(logsFile, []byte("{\"index\":\"logs\"}\n{\"query\":{\"match_all\":{}}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		body string
		args []string
		hint string
	}{
		{"metrics names", newMetricsNamesCmd, `[]`, []string{"--start", "-7d"}, "No metric names between"},
		{"metrics label-values", newMetricsLabelValuesCmd, `[]`, []string{"up", "job"}, "No values for this label between"},
		{"metrics query-range", newMetricsQueryRangeCmd, `{"status":"success","data":{"resultType":"matrix","result":[]}}`,
			[]string{"--query", "up"}, "No series between"},
		{"metrics query", newMetricsQueryCmd, `{"status":"success","data":{"resultType":"vector","result":[]}}`,
			[]string{"--query", "up"}, "instant query reads only"},
		{"logs query", newLogsQueryCmd, `{"responses":[{"hits":{"total":{"value":0},"hits":[]}}]}`,
			[]string{"-f", logsFile}, "No logs between"},
		{"logs field-values", newLogsFieldValuesCmd, `{"responses":[{"hits":{"total":0},"aggregations":{"values":{"buckets":[]}}}]}`,
			[]string{"level", "-i", "logs"}, "No logs between"},
		{"logs aggregate", newLogsAggregateCmd, `{"responses":[{"hits":{"total":0},"aggregations":{"values":{"buckets":[]}}}]}`,
			[]string{"-i", "logs", "--count-by", "level"}, "No logs between"},
		{"traces labels default window", newTracesLabelsCmd, `{"data":[]}`, nil, "default window"},
		{"traces labels with start", newTracesLabelsCmd, `{"data":[]}`, []string{"--start", "-2d"}, "No trace labels between"},
		{"traces list", newTracesListCmd, `{"data":[],"limit":20,"offset":0,"total":0}`,
			[]string{"--start", "-1h", "--end", "now"}, "No traces between"},
		{"traceql search", newTraceQLSearchCmd, `{"traces":[]}`, []string{"{ }"}, "No matching traces between"},
		{"traceql metrics", newTraceQLMetricsCmd, `{"series":[]}`, []string{"{ } | rate()"}, "No series between"},
		{"traceql tag-values", newTraceQLTagValuesCmd, `{"tagValues":[]}`, []string{"span.x"}, "No values for span.x in the last hour"},
		{"traceql tags", newTraceQLTagsCmd, `{"scopes":[]}`, nil, "reads only the last hour"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTraceQLTestServer(t, 200, tt.body)
			stdout, stderr, err := runCmdSplit(t, srv.URL, tt.cmd(), output.FormatJSON, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stderr, tt.hint) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.hint)
			}
			if !json.Valid([]byte(stdout)) {
				t.Errorf("stdout is not clean JSON: %q", stdout)
			}
		})
	}
}

func TestEmptyResultHints_NoHintWithData(t *testing.T) {
	srv, _ := newTraceQLTestServer(t, 200, `["up"]`)
	_, stderr, err := runCmdSplit(t, srv.URL, newMetricsNamesCmd(), output.FormatTable)
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Errorf("unexpected hint: %q", stderr)
	}
}

func TestIsEmptyLogsResult(t *testing.T) {
	tests := map[string]bool{
		`{"responses":[{"hits":{"hits":[]}}]}`:                        true,
		`{"responses":[{"hits":{"hits":[{"_id":"1"}]}}]}`:             false,
		`{"responses":[{"hits":{"hits":[]},"aggregations":{"a":1}}]}`: false,
		`{"responses":[{"error":{"type":"x"}}]}`:                      false,
		`{"responses":[]}`:                                            false,
	}
	for body, want := range tests {
		if got := isEmptyLogsResult([]byte(body)); got != want {
			t.Errorf("isEmptyLogsResult(%s) = %v, want %v", body, got, want)
		}
	}
}

func TestGenAIScoresEmptyHint(t *testing.T) {
	srv, _ := newTraceQLTestServer(t, 200, `{"data":[]}`)
	stdout, stderr, err := runCmdSplit(t, srv.URL, newGenAIScoresListCmd(), output.FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "default window (the last 15 minutes)") {
		t.Errorf("stderr = %q", stderr)
	}
	if !json.Valid([]byte(stdout)) {
		t.Errorf("stdout is not clean JSON: %q", stdout)
	}
}
