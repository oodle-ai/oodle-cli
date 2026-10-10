package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// logsAggRequest is what the logs aggregation test server saw.
type logsAggRequest struct {
	method      string
	path        string
	instance    string
	contentType string
	header      map[string]any
	search      map[string]any
}

// newLogsAggTestServer returns a server that records the NDJSON request and
// replies with body.
func newLogsAggTestServer(t *testing.T, status int, body string) (*httptest.Server, *logsAggRequest) {
	t.Helper()
	got := &logsAggRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.instance = r.Header.Get("X-OODLE-INSTANCE")
		got.contentType = r.Header.Get("Content-Type")
		data, _ := io.ReadAll(r.Body)
		lines := splitNDJSON(data)
		if len(lines) != 2 {
			t.Errorf("expected 2 NDJSON lines, got %d: %s", len(lines), data)
		} else {
			if err := json.Unmarshal(lines[0], &got.header); err != nil {
				t.Errorf("header line: %v", err)
			}
			if err := json.Unmarshal(lines[1], &got.search); err != nil {
				t.Errorf("search line: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// runLogsAggCmd runs cmd against srv and returns stdout and stderr apart.
func runLogsAggCmd(t *testing.T, srv *httptest.Server, cmd *cobra.Command, format output.Format, args ...string) (string, string, error) {
	t.Helper()
	cmd.SetArgs(nil)
	if err := cmd.ParseFlags(args); err != nil {
		return "", "", err
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		return "", "", err
	}
	if err := cmd.Args(cmd, cmd.Flags().Args()); err != nil {
		return "", "", err
	}
	return runCmdSplit(t, srv.URL, cmd, format, args...)
}

// jsonPath walks nested maps by key and fails the test when a key is absent.
func jsonPath(t *testing.T, v any, keys ...string) any {
	t.Helper()
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("at %q: not an object: %v", k, v)
		}
		if v, ok = m[k]; !ok {
			t.Fatalf("missing key %q in %v", k, m)
		}
	}
	return v
}

const logsTermsBody = `{"responses":[{"error":null,"hits":{"hits":[],"total":120},` +
	`"aggregations":{"values":{"buckets":[{"key":"api","doc_count":100},{"key":"web","doc_count":15}],` +
	`"sum_other_doc_count":5}}}]}`

func TestLogsFieldValues_RequestAndTable(t *testing.T) {
	srv, got := newLogsAggTestServer(t, 200, logsTermsBody)
	stdout, stderr, err := runLogsAggCmd(t, srv, newLogsFieldValuesCmd(), output.FormatTable,
		"container_name", "--index", "app_logs", "--size", "2", "-q", "level:error",
		"--start", "1700000000", "--end", "1700003600000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.method != http.MethodPost || got.path != "/api/v1/query_logs" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
	if got.instance != "inst" || got.contentType != "application/x-ndjson" {
		t.Errorf("instance = %q, content type = %q", got.instance, got.contentType)
	}
	if got.header["index"] != "app_logs" {
		t.Errorf("header = %v", got.header)
	}
	if size, _ := got.search["size"].(float64); size != 0 {
		t.Errorf("search size = %v, want 0", got.search["size"])
	}
	terms := jsonPath(t, got.search, "aggs", "values", "terms").(map[string]any)
	if terms["field"] != "container_name" || terms["size"] != float64(2) {
		t.Errorf("terms = %v", terms)
	}
	filter := jsonPath(t, got.search, "query", "bool", "filter").([]any)
	if len(filter) != 2 {
		t.Fatalf("filter = %v", filter)
	}
	if q := jsonPath(t, filter[0], "query_string", "query"); q != "level:error" {
		t.Errorf("query_string = %v", q)
	}
	ts := jsonPath(t, filter[1], "range", "timestamp").(map[string]any)
	if ts["gte"] != float64(1700000000000) || ts["lte"] != float64(1700003600000) || ts["format"] != "epoch_millis" {
		t.Errorf("range = %v", ts)
	}

	if !strings.Contains(stdout, "CONTAINER_NAME") || !strings.Contains(stdout, "api") || !strings.Contains(stdout, "100") {
		t.Errorf("table = %q", stdout)
	}
	if !strings.Contains(stderr, "5 more logs have values that are not shown") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestLogsFieldValues_JSONRows(t *testing.T) {
	srv, _ := newLogsAggTestServer(t, 200, logsTermsBody)
	stdout, _, err := runLogsAggCmd(t, srv, newLogsFieldValuesCmd(), output.FormatJSON, "service", "-i", "app_logs")
	if err != nil {
		t.Fatal(err)
	}
	var rows []logsValueRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("stdout is not a JSON row list: %v\n%s", err, stdout)
	}
	if len(rows) != 2 || rows[0] != (logsValueRow{Value: "api", Count: 100}) {
		t.Errorf("rows = %+v", rows)
	}
}

func TestLogsFieldValues_NumericKey(t *testing.T) {
	body := `{"responses":[{"hits":{"total":{"value":3}},"aggregations":{"values":{"buckets":[` +
		`{"key":500,"doc_count":2},{"key":1.5,"key_as_string":"1.5","doc_count":1}]}}}]}`
	srv, _ := newLogsAggTestServer(t, 200, body)
	stdout, _, err := runLogsAggCmd(t, srv, newLogsFieldValuesCmd(), output.FormatJSON, "status", "-i", "app_logs")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"value": "500"`) || !strings.Contains(stdout, `"value": "1.5"`) {
		t.Errorf("stdout = %s", stdout)
	}
}

func TestLogsFieldValues_EmptyHints(t *testing.T) {
	tests := []struct {
		name string
		body string
		hint string
	}{
		{"no logs", `{"responses":[{"hits":{"total":0},"aggregations":{"values":{"buckets":[]}}}]}`, "No logs between"},
		{"field absent", `{"responses":[{"hits":{"total":42},"aggregations":{"values":{"buckets":[]}}}]}`,
			"42 logs matched, but none has a value for container_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newLogsAggTestServer(t, 200, tt.body)
			stdout, stderr, err := runLogsAggCmd(t, srv, newLogsFieldValuesCmd(), output.FormatJSON,
				"container_name", "-i", "app_logs")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stderr, tt.hint) {
				t.Errorf("stderr = %q, want %q", stderr, tt.hint)
			}
			if strings.TrimSpace(stdout) != "[]" {
				t.Errorf("stdout = %q, want []", stdout)
			}
		})
	}
}

func TestLogsFieldValues_SearchError(t *testing.T) {
	body := `{"responses":[{"error":{"message":"Internal server error","name":"SearchError"},"hits":null}]}`
	srv, _ := newLogsAggTestServer(t, 200, body)
	_, _, err := runLogsAggCmd(t, srv, newLogsFieldValuesCmd(), output.FormatTable, "Level", "-i", "app_logs")
	if err == nil {
		t.Fatal("expected an error for a search error in a success response")
	}
	for _, want := range []string{"Internal server error", "Check that Level is a field of index app_logs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestLogsFieldValues_HTTPError(t *testing.T) {
	srv, _ := newLogsAggTestServer(t, 403, `{"error":"forbidden"}`)
	_, _, err := runLogsAggCmd(t, srv, newLogsFieldValuesCmd(), output.FormatTable, "x", "-i", "app_logs")
	if err == nil {
		t.Fatal("expected an error for status 403")
	}
}

func TestLogsFieldValues_FlagValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing index", []string{"f"}, "index"},
		{"missing field", []string{"-i", "app_logs"}, "missing required argument"},
		{"size zero", []string{"f", "-i", "app_logs", "--size", "0"}, "--size must be between 1 and 1000"},
		{"size too large", []string{"f", "-i", "app_logs", "--size", "1001"}, "--size must be between 1 and 1000"},
		{"start after end", []string{"f", "-i", "app_logs", "--start", "now", "--end", "-1h"}, "--start must be before --end"},
		{"bad start", []string{"f", "-i", "app_logs", "--start", "yesterday"}, "--start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, got := newLogsAggTestServer(t, 200, logsTermsBody)
			_, _, err := runLogsAggCmd(t, srv, newLogsFieldValuesCmd(), output.FormatTable, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
			if got.path != "" {
				t.Errorf("request was sent for invalid flags")
			}
		})
	}
}

func TestLogsAggregate_CountBy(t *testing.T) {
	srv, got := newLogsAggTestServer(t, 200, logsTermsBody)
	stdout, _, err := runLogsAggCmd(t, srv, newLogsAggregateCmd(), output.FormatTable,
		"-i", "app_logs", "--count-by", "service", "--size", "5")
	if err != nil {
		t.Fatal(err)
	}
	terms := jsonPath(t, got.search, "aggs", "values", "terms").(map[string]any)
	if terms["field"] != "service" || terms["size"] != float64(5) {
		t.Errorf("terms = %v", terms)
	}
	if _, ok := got.search["aggs"].(map[string]any)["over_time"]; ok {
		t.Error("unexpected histogram without --histogram")
	}
	if !strings.Contains(stdout, "SERVICE") || !strings.Contains(stdout, "web") {
		t.Errorf("table = %q", stdout)
	}
}

func TestLogsAggregate_Histogram(t *testing.T) {
	body := `{"responses":[{"hits":{"total":7},"aggregations":{"over_time":{"buckets":[` +
		`{"key":1700000000000,"key_as_string":"2023-11-14T22:13:20.000Z","doc_count":3},` +
		`{"key":1700000300000,"doc_count":4}]}}}]}`
	srv, got := newLogsAggTestServer(t, 200, body)
	stdout, stderr, err := runLogsAggCmd(t, srv, newLogsAggregateCmd(), output.FormatJSON,
		"-i", "app_logs", "--histogram", "5m", "--start", "-6h")
	if err != nil {
		t.Fatal(err)
	}
	hist := jsonPath(t, got.search, "aggs", "over_time", "date_histogram").(map[string]any)
	if hist["field"] != "timestamp" || hist["fixed_interval"] != "5m" {
		t.Errorf("date_histogram = %v", hist)
	}
	if _, ok := got.search["aggs"].(map[string]any)["over_time"].(map[string]any)["aggs"]; ok {
		t.Error("unexpected sub-aggregation without --count-by")
	}
	var rows []logsTimeRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("stdout: %v\n%s", err, stdout)
	}
	want := []logsTimeRow{{Time: "2023-11-14T22:13:20Z", Count: 3}, {Time: "2023-11-14T22:18:20Z", Count: 4}}
	if len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %q", stderr)
	}
}

func TestLogsAggregate_HistogramCountBy(t *testing.T) {
	body := `{"responses":[{"hits":{"total":9},"aggregations":{"over_time":{"buckets":[` +
		`{"key":1700000000000,"doc_count":9,"values":{"buckets":[{"key":"error","doc_count":6},{"key":"warn","doc_count":3}]}},` +
		`{"key":1700003600000,"doc_count":0,"values":{"buckets":[]}}]}}}]}`
	srv, got := newLogsAggTestServer(t, 200, body)
	stdout, _, err := runLogsAggCmd(t, srv, newLogsAggregateCmd(), output.FormatTable,
		"-i", "app_logs", "--histogram", "1h", "--count-by", "level", "--size", "3", "--start", "-24h", "-q", "cluster:prod")
	if err != nil {
		t.Fatal(err)
	}
	terms := jsonPath(t, got.search, "aggs", "over_time", "aggs", "values", "terms").(map[string]any)
	if terms["field"] != "level" || terms["size"] != float64(3) {
		t.Errorf("nested terms = %v", terms)
	}
	for _, want := range []string{"TIME", "LEVEL", "COUNT", "2023-11-14T22:13:20Z", "error", "warn"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestLogsAggregate_EmptyHint(t *testing.T) {
	body := `{"responses":[{"hits":{"total":0},"aggregations":{"over_time":{"buckets":[]}}}]}`
	srv, _ := newLogsAggTestServer(t, 200, body)
	stdout, stderr, err := runLogsAggCmd(t, srv, newLogsAggregateCmd(), output.FormatJSON,
		"-i", "app_logs", "--histogram", "1m")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "No logs between") {
		t.Errorf("stderr = %q", stderr)
	}
	if !json.Valid([]byte(stdout)) {
		t.Errorf("stdout is not clean JSON: %q", stdout)
	}
}

func TestLogsAggregate_FlagValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no aggregation", []string{"-i", "app_logs"}, "set --count-by, --histogram, or both"},
		{"missing index", []string{"--count-by", "f"}, "index"},
		{"bad interval", []string{"-i", "app_logs", "--histogram", "1w"}, "use a number and a unit"},
		{"zero interval", []string{"-i", "app_logs", "--histogram", "0m"}, "use a number and a unit"},
		{"too many buckets", []string{"-i", "app_logs", "--histogram", "1s", "--start", "-1d"}, "the limit is 2000"},
		{"bad size", []string{"-i", "app_logs", "--count-by", "f", "--size", "-1"}, "--size must be between"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, got := newLogsAggTestServer(t, 200, logsTermsBody)
			_, _, err := runLogsAggCmd(t, srv, newLogsAggregateCmd(), output.FormatTable, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
			if got.path != "" {
				t.Errorf("request was sent for invalid flags")
			}
		})
	}
}
