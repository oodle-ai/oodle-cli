package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/config"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// traceQLRequest is what the test server saw.
type traceQLRequest struct {
	escapedPath string
	query       url.Values
	apiKey      string
}

// newTraceQLTestServer returns a server that records the request and replies
// with body.
func newTraceQLTestServer(t *testing.T, status int, body string) (*httptest.Server, *traceQLRequest) {
	t.Helper()
	got := &traceQLRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.escapedPath = r.URL.EscapedPath()
		got.query = r.URL.Query()
		got.apiKey = r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// runTraceQLCmd runs cmd against srv with the given args and output format.
func runTraceQLCmd(t *testing.T, srv *httptest.Server, cmd *cobra.Command, format output.Format, instance string, args ...string) (string, error) {
	t.Helper()
	c := &api.Client{Config: &config.Config{APIURL: srv.URL + "/", APIKey: "test-key"}}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	ctx := withClient(context.Background(), c)
	ctx = withOutput(ctx, format)
	ctx = withInstance(ctx, instance)
	cmd.SetContext(ctx)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}
	if err := cmd.Args(cmd, cmd.Flags().Args()); err != nil {
		return "", err
	}
	err := cmd.RunE(cmd, cmd.Flags().Args())
	return buf.String(), err
}

const traceQLSearchBody = `{"traces":[{"traceID":"abc123","rootServiceName":"api","rootTraceName":"GET /users",` +
	`"startTimeUnixNano":"1700000000000000000","durationMs":1500,"spanSets":[{"spans":[],"matched":3}]}],` +
	`"metrics":{"inspectedTraces":1}}`

func TestTraceQLSearch_RequestAndTable(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, traceQLSearchBody)
	query := `{ resource.service.name="api" && status=error }`
	out, err := runTraceQLCmd(t, srv, newTraceQLSearchCmd(), output.FormatTable, "inst/1",
		"--start", "1700000000", "--end", "1700003600000", "--limit", "5", query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := "/v1/api/instance/inst%2F1/traces/traceql/search"; got.escapedPath != want {
		t.Errorf("path = %q, want %q", got.escapedPath, want)
	}
	if got.apiKey != "test-key" {
		t.Errorf("X-API-Key = %q, want test-key", got.apiKey)
	}
	wantParams := map[string]string{
		"q":     query,
		"start": "1700000000",
		// The end was given in milliseconds; the server reads seconds.
		"end":   "1700003600",
		"limit": "5",
	}
	for k, want := range wantParams {
		if v := got.query.Get(k); v != want {
			t.Errorf("param %s = %q, want %q", k, v, want)
		}
	}

	for _, want := range []string{"TRACE ID", "ROOT SERVICE", "abc123", "api", "GET /users", "2023-11-14 22:13:20", "1.5s", "3"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output does not contain %q:\n%s", want, out)
		}
	}
}

func TestTraceQLSearch_DefaultsAndJSONPassThrough(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, traceQLSearchBody)
	before := time.Now().Unix()
	out, err := runTraceQLCmd(t, srv, newTraceQLSearchCmd(), output.FormatJSON, "inst", `{ duration > 2s }`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != traceQLSearchBody {
		t.Errorf("JSON output is not the response body:\n%s", out)
	}
	if got.query.Get("limit") != "20" {
		t.Errorf("limit = %q, want 20", got.query.Get("limit"))
	}
	start, _ := strconv.ParseInt(got.query.Get("start"), 10, 64)
	end, _ := strconv.ParseInt(got.query.Get("end"), 10, 64)
	if end-start < 3599 || end-start > 3601 {
		t.Errorf("default range = %ds, want about 3600s", end-start)
	}
	if end < before || end > time.Now().Unix()+1 {
		t.Errorf("end = %d is not now in epoch seconds", end)
	}
}

func TestTraceQLSearch_RejectsMetricsQuery(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, `{}`)
	_, err := runTraceQLCmd(t, srv, newTraceQLSearchCmd(), output.FormatTable, "inst", `{ status=error } | rate()`)
	if err == nil || !strings.Contains(err.Error(), "traceql metrics") {
		t.Fatalf("expected a hint to use metrics, got %v", err)
	}
	if got.escapedPath != "" {
		t.Errorf("request was sent for a rejected query")
	}
}

func TestTraceQLSearch_APIError(t *testing.T) {
	srv, _ := newTraceQLTestServer(t, 400, `{"message":"invalid TraceQL: unexpected token"}`)
	_, err := runTraceQLCmd(t, srv, newTraceQLSearchCmd(), output.FormatTable, "inst", `{ status= }`)
	if err == nil || !strings.Contains(err.Error(), "invalid TraceQL") {
		t.Fatalf("expected the server message in the error, got %v", err)
	}
}

const traceQLMetricsBody = `{"series":[` +
	`{"labels":[{"key":"span.http.route","value":{"stringValue":"/users"}}],` +
	`"samples":[{"timestampMs":1700000060000,"value":2},{"timestampMs":1700000000000,"value":4}]},` +
	`{"labels":[{"key":"span.http.route","value":{"stringValue":"/orders"}}],` +
	`"samples":[{"timestampMs":1700000000000,"value":0.5}]}]}`

func TestTraceQLMetrics_RequestAndTable(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, traceQLMetricsBody)
	query := `{ resource.service.name="api" && status=error } | rate() by (span.http.route)`
	out, err := runTraceQLCmd(t, srv, newTraceQLMetricsCmd(), output.FormatTable, "inst",
		"--start", "1700000000000000", "--end", "1700003600", "--step", "5m", query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/v1/api/instance/inst/traces/traceql/metrics/query_range"; got.escapedPath != want {
		t.Errorf("path = %q, want %q", got.escapedPath, want)
	}
	wantParams := map[string]string{
		"q": query,
		// The start was given in microseconds.
		"start":     "1700000000",
		"end":       "1700003600",
		"step":      "300s",
		"exemplars": "0",
	}
	for k, want := range wantParams {
		if v := got.query.Get(k); v != want {
			t.Errorf("param %s = %q, want %q", k, v, want)
		}
	}
	for _, want := range []string{"SERIES", "POINTS", "LAST", `{span.http.route="/users"}`, `{span.http.route="/orders"}`, "0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output does not contain %q:\n%s", want, out)
		}
	}
	// Samples are sorted by time, so the last value of /users is 2.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "/users") {
			fields := strings.Fields(line)
			// SERIES POINTS LAST MIN MAX AVG
			if len(fields) < 6 || fields[1] != "2" || fields[2] != "2" || fields[3] != "2" || fields[4] != "4" || fields[5] != "3" {
				t.Errorf("unexpected /users row: %q", line)
			}
		}
	}
}

func TestTraceQLMetrics_DefaultStep(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, `{"series":[]}`)
	_, err := runTraceQLCmd(t, srv, newTraceQLMetricsCmd(), output.FormatTable, "inst",
		"--start", "-1h", `{ } | count_over_time()`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.query.Get("step") != "60s" {
		t.Errorf("step = %q, want 60s", got.query.Get("step"))
	}
}

func TestTraceQLMetrics_RejectsSearchQuery(t *testing.T) {
	srv, _ := newTraceQLTestServer(t, 200, `{}`)
	_, err := runTraceQLCmd(t, srv, newTraceQLMetricsCmd(), output.FormatTable, "inst", `{ status=error }`)
	if err == nil || !strings.Contains(err.Error(), "traceql search") {
		t.Fatalf("expected a hint to use search, got %v", err)
	}
}

func TestTraceQLMetrics_Stats(t *testing.T) {
	srv, _ := newTraceQLTestServer(t, 200, traceQLMetricsBody)
	out, err := runTraceQLCmd(t, srv, newTraceQLMetricsCmd(), output.FormatStats, "inst", `{ } | rate()`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "/users") {
		t.Errorf("stats output does not contain the series:\n%s", out)
	}
}

const traceQLTagsBody = `{"scopes":[{"name":"resource","tags":["service.name"]},` +
	`{"name":"span","tags":["http.route","gen_ai.tool.name"]},{"name":"intrinsic","tags":["duration","status"]}]}`

func TestTraceQLTags_Table(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, traceQLTagsBody)
	out, err := runTraceQLCmd(t, srv, newTraceQLTagsCmd(), output.FormatTable, "inst")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/v1/api/instance/inst/traces/traceql/tags"; got.escapedPath != want {
		t.Errorf("path = %q, want %q", got.escapedPath, want)
	}
	if got.query.Has("scope") {
		t.Errorf("scope is sent for all scopes")
	}
	// The server always reads the last hour, so no range is sent.
	if got.query.Has("start") || got.query.Has("end") {
		t.Errorf("start/end sent: %v", got.query)
	}
	for _, want := range []string{"resource.service.name", "span.http.route", "span.gen_ai.tool.name", "duration"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output does not contain %q:\n%s", want, out)
		}
	}
}

func TestTraceQLTags_Scope(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, traceQLTagsBody)
	out, err := runTraceQLCmd(t, srv, newTraceQLTagsCmd(), output.FormatTable, "inst", "--scope", "span")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.query.Get("scope") != "span" {
		t.Errorf("scope = %q, want span", got.query.Get("scope"))
	}
	if strings.Contains(out, "service.name") || strings.Contains(out, "duration") {
		t.Errorf("output has tags from other scopes:\n%s", out)
	}
	if !strings.Contains(out, "span.http.route") {
		t.Errorf("output does not contain span.http.route:\n%s", out)
	}
}

func TestTraceQLTags_BadScope(t *testing.T) {
	srv, _ := newTraceQLTestServer(t, 200, traceQLTagsBody)
	_, err := runTraceQLCmd(t, srv, newTraceQLTagsCmd(), output.FormatTable, "inst", "--scope", "link")
	if err == nil || !strings.Contains(err.Error(), "--scope") {
		t.Fatalf("expected a --scope error, got %v", err)
	}
}

func TestTraceQLTagValues_EscapesTag(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, `{"tagValues":[{"type":"string","value":"/users"},{"type":"string","value":"/orders"}]}`)
	out, err := runTraceQLCmd(t, srv, newTraceQLTagValuesCmd(), output.FormatTable, "a b",
		"--query", `{ resource.service.name="api" }`, "span.weird/tag name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/v1/api/instance/a%20b/traces/traceql/tags/span.weird%2Ftag%20name/values"; got.escapedPath != want {
		t.Errorf("path = %q, want %q", got.escapedPath, want)
	}
	if got.query.Get("q") != `{ resource.service.name="api" }` {
		t.Errorf("q = %q", got.query.Get("q"))
	}
	if !strings.Contains(out, "VALUE") || strings.Index(out, "/orders") > strings.Index(out, "/users") {
		t.Errorf("expected sorted values in a table:\n%s", out)
	}
}

func TestIsTraceQLMetricsQuery(t *testing.T) {
	tests := []struct {
		q    string
		want bool
	}{
		{`{ status=error }`, false},
		{`{ status=error } | rate()`, true},
		{`{ } |rate() by (span.http.route)`, true},
		{`{ duration > 1s } | quantile_over_time(duration, .95)`, true},
		{`{ name="| rate(" }`, false},
		{`{ status=error } | select(span.http.route)`, false},
	}
	for _, tt := range tests {
		if got := isTraceQLMetricsQuery(tt.q); got != tt.want {
			t.Errorf("isTraceQLMetricsQuery(%q) = %v, want %v", tt.q, got, tt.want)
		}
	}
}

func TestParseTraceQLStep(t *testing.T) {
	tests := map[string]int64{"30s": 30, "5m": 300, "1h": 3600, "1d": 86400, "90": 90}
	for in, want := range tests {
		got, err := parseTraceQLStep(in)
		if err != nil || got != want {
			t.Errorf("parseTraceQLStep(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"0", "-5", "abc", "100ms"} {
		if _, err := parseTraceQLStep(bad); err == nil {
			t.Errorf("parseTraceQLStep(%q): expected an error", bad)
		}
	}
}

func TestDefaultTraceQLStep(t *testing.T) {
	tests := map[int64]int64{3600: 60, 6 * 3600: 120, 24 * 3600: 300, 7 * 86400: 2040}
	for rangeSec, want := range tests {
		if got := defaultTraceQLStep(rangeSec); got != want {
			t.Errorf("defaultTraceQLStep(%d) = %d, want %d", rangeSec, got, want)
		}
	}
}

func TestTraceQLTimeRange_StartAfterEnd(t *testing.T) {
	srv, _ := newTraceQLTestServer(t, 200, `{}`)
	_, err := runTraceQLCmd(t, srv, newTraceQLSearchCmd(), output.FormatTable, "inst",
		"--start", "now", "--end", "-1h", `{ }`)
	if err == nil || !strings.Contains(err.Error(), "before --end") {
		t.Fatalf("expected a range error, got %v", err)
	}
}

// TestTraceQLTags_NoTimeFlags checks that tags and tag-values have no
// --start or --end. The server ignores them, so the flags would make a user
// think that an empty result covers a longer range.
func TestTraceQLTags_NoTimeFlags(t *testing.T) {
	for _, cmd := range []*cobra.Command{newTraceQLTagsCmd(), newTraceQLTagValuesCmd()} {
		for _, name := range []string{"start", "end"} {
			if cmd.Flags().Lookup(name) != nil {
				t.Errorf("%s has --%s", cmd.Name(), name)
			}
		}
	}
}

func TestTraceQLSearch_LimitRange(t *testing.T) {
	for _, bad := range []string{"0", "-1", "1001"} {
		srv, got := newTraceQLTestServer(t, 200, traceQLSearchBody)
		_, err := runTraceQLCmd(t, srv, newTraceQLSearchCmd(), output.FormatJSON, "inst", "--limit", bad, "{ }")
		if err == nil || !strings.Contains(err.Error(), "between 1 and 1000") {
			t.Errorf("--limit %s: expected a range error, got %v", bad, err)
		}
		if got.escapedPath != "" {
			t.Errorf("--limit %s: request was sent", bad)
		}
	}
	srv, got := newTraceQLTestServer(t, 200, traceQLSearchBody)
	if _, err := runTraceQLCmd(t, srv, newTraceQLSearchCmd(), output.FormatJSON, "inst", "--limit", "1000", "{ }"); err != nil {
		t.Fatalf("--limit 1000: %v", err)
	}
	if got.query.Get("limit") != "1000" {
		t.Errorf("limit = %q, want 1000", got.query.Get("limit"))
	}
}
