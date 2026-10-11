package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// genaiRequest is what the test server saw.
type genaiRequest struct {
	method      string
	escapedPath string
	query       url.Values
	body        []byte
	apiKey      string
}

// newGenAITestServer replies to each request with the body that
// reply returns for its path, and records the last request.
func newGenAITestServer(t *testing.T, reply func(path string) (int, string)) (*httptest.Server, *genaiRequest) {
	t.Helper()
	got := &genaiRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.escapedPath = r.URL.EscapedPath()
		got.query = r.URL.Query()
		got.body, _ = io.ReadAll(r.Body)
		got.apiKey = r.Header.Get("X-API-Key")
		status, body := reply(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func fixedReply(body string) func(string) (int, string) {
	return func(string) (int, string) { return 200, body }
}

// testGenAITracesBody has one agent trace. The agent span repeats the
// tokens of its model calls, so a sum over all spans counts them
// twice. One model call reports a cost and one does not. An
// evaluation span gives a score. An HTTP span failed.
const testGenAITracesBody = `{"data":[{"traceID":"t1","processes":{"p1":{"serviceName":"support"}},"spans":[
 {"spanID":"a","operationName":"invoke_agent planner","startTime":1700000000000000,"duration":3000000,"processID":"p1",
  "tags":[{"key":"gen_ai.operation.name","value":"invoke_agent"},{"key":"gen_ai.agent.name","value":"planner"},
          {"key":"gen_ai.usage.input_tokens_int","value":"300"},{"key":"gen_ai.usage.output_tokens_int","value":"30"},
          {"key":"gen_ai.conversation.id","value":"s1"},{"key":"user.id","value":"u1"}]},
 {"spanID":"b","operationName":"chat gpt-4o","startTime":1700000000100000,"duration":1000000,"processID":"p1",
  "tags":[{"key":"gen_ai.operation.name","value":"chat"},{"key":"gen_ai.request.model","value":"gpt-4o"},
          {"key":"gen_ai.usage.input_tokens_int","value":"100"},{"key":"gen_ai.usage.cache_read.input_tokens_int","value":"40"},
          {"key":"gen_ai.usage.output_tokens_int","value":"10"},{"key":"gen_ai.usage.cost_float","value":0.5}]},
 {"spanID":"c","operationName":"chat claude","startTime":1700000001200000,"duration":1000000,"processID":"p1",
  "tags":[{"key":"gen_ai.operation.name","value":"chat"},{"key":"gen_ai.request.model","value":"claude-sonnet-4"},
          {"key":"gen_ai.usage.input_tokens","value":20},{"key":"gen_ai.usage.cache_read.input_tokens","value":100},
          {"key":"gen_ai.usage.output_tokens","value":20}]},
 {"spanID":"d","operationName":"execute_tool search","startTime":1700000002300000,"duration":500000,"processID":"p1",
  "tags":[{"key":"gen_ai.operation.name","value":"execute_tool"},{"key":"gen_ai.tool.name","value":"search"},
          {"key":"oodle.signal.execution","value":"failure.tool_error"},{"key":"otel.status_code","value":"Error"}]},
 {"spanID":"e","operationName":"GET /db","startTime":1700000002400000,"duration":100000,"processID":"p1",
  "tags":[{"key":"error","value":true},{"key":"error.message","value":"timeout"}]},
 {"spanID":"f","operationName":"eval_score","startTime":1700000009000000,"duration":1,"processID":"p1",
  "tags":[{"key":"gen_ai.operation.name","value":"eval_score"},{"key":"score.name","value":"helpful"},{"key":"score.value","value":"0.8"}]}
]}],"rowsRead":6,"rowLimit":100000}`

func TestSummarizeGenAITrace(t *testing.T) {
	var resp genaiTracesResponse
	if err := json.Unmarshal([]byte(testGenAITracesBody), &resp); err != nil {
		t.Fatal(err)
	}
	r := summarizeGenAITrace(resp.Data[0])
	// gpt-4o: input 100 includes the 40 cached, so 60 uncached.
	// claude: input 20 excludes the 100 cached. The agent span is
	// not counted.
	if r.InputTokens != 80 || r.CacheReadTokens != 140 || r.OutputTokens != 30 || r.TotalTokens != 250 {
		t.Errorf("tokens = %d/%d/%d total %d, want 80/140/30 total 250",
			r.InputTokens, r.CacheReadTokens, r.OutputTokens, r.TotalTokens)
	}
	if r.Generations != 2 || r.ToolCalls != 1 || r.Errors != 2 {
		t.Errorf("generations/tools/errors = %d/%d/%d, want 2/1/2", r.Generations, r.ToolCalls, r.Errors)
	}
	if r.CostDollars != 0.5 || r.GenerationsWithoutCost != 1 {
		t.Errorf("cost = %v without %d, want 0.5 without 1", r.CostDollars, r.GenerationsWithoutCost)
	}
	// The evaluation span is not part of the agent run.
	if r.DurationMs != 3000 {
		t.Errorf("duration = %v ms, want 3000", r.DurationMs)
	}
	if len(r.Scores) != 1 || r.Scores[0].Name != "helpful" || *r.Scores[0].Value != 0.8 {
		t.Errorf("scores = %+v", r.Scores)
	}
	if r.SessionID != "s1" || r.UserID != "u1" || strings.Join(r.Signals, ",") != "failure.tool_error" ||
		strings.Join(r.Models, ",") != "claude-sonnet-4,gpt-4o" || strings.Join(r.Services, ",") != "support" {
		t.Errorf("unexpected row: %+v", r)
	}
}

func TestGenAITraces_Request(t *testing.T) {
	srv, got := newGenAITestServer(t, fixedReply(testGenAITracesBody))
	stdout, stderr, err := runCmdSplit(t, srv.URL, newGenAITracesCmd(), output.FormatJSON,
		"--agent", "planner", "--model", "gpt-4.1,o3", "--attr", "tenant=acme", "--attr", "resource::region=eu",
		"--errors", "--min-tokens", "1000", "--limit", "10", "--start", "-1h")
	if err != nil {
		t.Fatal(err)
	}
	if got.escapedPath != "/v1/api/instance/inst/traces/traces" || got.apiKey != "test-key" {
		t.Errorf("path %q key %q", got.escapedPath, got.apiKey)
	}
	if got.query.Get("limit") != "11" || got.query.Get("minTokens") != "1000" {
		t.Errorf("limit %q minTokens %q", got.query.Get("limit"), got.query.Get("minTokens"))
	}
	if !strings.Contains(got.query.Get("extraFields"), "gen_ai.usage.input_tokens_int") {
		t.Errorf("extraFields = %q", got.query.Get("extraFields"))
	}
	var filters []traceMatcher
	if err := json.Unmarshal([]byte(got.query.Get("filters")), &filters); err != nil {
		t.Fatal(err)
	}
	want := []traceMatcher{
		{Name: genaiOperationLabel, Type: 2, Value: ".+"},
		{Name: "span::gen_ai.agent.name", Type: 0, Value: "planner"},
		{Name: "span::gen_ai.request.model", Type: 2, Value: `(?:gpt-4\.1|o3)`},
		{Name: "span::tenant", Type: 0, Value: "acme"},
		{Name: "resource::region", Type: 0, Value: "eu"},
		{Name: "span_status", Type: 0, Value: "Error"},
	}
	if fmt.Sprint(filters) != fmt.Sprint(want) {
		t.Errorf("filters = %v\nwant      %v", filters, want)
	}
	var out struct {
		GroupBy string          `json:"group_by"`
		Traces  []genaiTraceRow `json:"traces"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if out.GroupBy != "trace" || len(out.Traces) != 1 || out.Traces[0].TotalTokens != 250 {
		t.Errorf("unexpected output: %s", stdout)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %q", stderr)
	}
}

func TestGenAITraces_OperationReplacesBaseFilter(t *testing.T) {
	srv, got := newGenAITestServer(t, fixedReply(`{"data":[]}`))
	if _, _, err := runCmdSplit(t, srv.URL, newGenAITracesCmd(), output.FormatJSON, "--operation", "execute_tool"); err != nil {
		t.Fatal(err)
	}
	if f := got.query.Get("filters"); f != `[{"name":"span::gen_ai.operation.name","type":0,"value":"execute_tool"}]` {
		t.Errorf("filters = %s", f)
	}
}

func TestGenAITraces_TableAndHints(t *testing.T) {
	srv, _ := newGenAITestServer(t, fixedReply(testGenAITracesBody))
	stdout, stderr, err := runCmdSplit(t, srv.URL, newGenAITracesCmd(), output.FormatTable, "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TRACE ID", "LLM CALLS", "t1", "2023-11-14 22:13:20", "3s", "planner", "250", "$0.50"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table does not contain %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stderr, "more traces match") {
		t.Errorf("unexpected hint: %q", stderr)
	}

	// Two traces for --limit 1: the extra trace shows that more match.
	two := strings.Replace(testGenAITracesBody, `{"data":[`, `{"data":[{"traceID":"t0","spans":[]},`, 1)
	srv, _ = newGenAITestServer(t, fixedReply(two))
	_, stderr, err = runCmdSplit(t, srv.URL, newGenAITracesCmd(), output.FormatTable, "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "more traces match") {
		t.Errorf("stderr = %q", stderr)
	}

	cut := strings.Replace(testGenAITracesBody, `"rowsRead":6`, `"rowsRead":100000`, 1)
	srv, _ = newGenAITestServer(t, fixedReply(cut))
	_, stderr, err = runCmdSplit(t, srv.URL, newGenAITracesCmd(), output.FormatTable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "stopped at 100000 span rows") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestGenAITraces_GroupBySession(t *testing.T) {
	srv, _ := newGenAITestServer(t, fixedReply(testGenAITracesBody))
	stdout, _, err := runCmdSplit(t, srv.URL, newGenAITracesCmd(), output.FormatTable, "--group-by", "session")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SESSION ID", "USER", "s1", "u1", "250"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestGenAITraces_BadFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--limit", "0"}, {"--limit", "501"}, {"--group-by", "model"}, {"--attr", "novalue"},
	} {
		srv, _ := newGenAITestServer(t, fixedReply(`{"data":[]}`))
		if _, _, err := runCmdSplit(t, srv.URL, newGenAITracesCmd(), output.FormatJSON, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

func TestGenAITrace_Steps(t *testing.T) {
	srv, got := newGenAITestServer(t, fixedReply(testGenAITracesBody))
	stdout, _, err := runCmdSplit(t, srv.URL, newGenAITraceCmd(), output.FormatTable, "abc:12/3")
	if err != nil {
		t.Fatal(err)
	}
	if got.escapedPath != "/v1/api/instance/inst/traces/traces/abc:12%2F3" {
		t.Errorf("path = %q", got.escapedPath)
	}
	if got.query.Get("omitLarge") != "1" {
		t.Errorf("omitLarge = %q, want 1 without --messages", got.query.Get("omitLarge"))
	}
	// The evaluation span is not a step; the failed HTTP span is.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 6 {
		t.Fatalf("want a header and 5 steps:\n%s", stdout)
	}
	for i, want := range []string{"AGENT", "GENERATION", "GENERATION", "TOOL", "SPAN"} {
		if !strings.Contains(lines[i+1], want) {
			t.Errorf("step %d = %q, want type %s", i+1, lines[i+1], want)
		}
	}
	if !strings.Contains(lines[2], "100/10") || !strings.Contains(lines[4], "failure.tool_error") {
		t.Errorf("unexpected steps:\n%s", stdout)
	}
}

func TestGenAITrace_Messages(t *testing.T) {
	body := strings.Replace(testGenAITracesBody,
		`{"key":"gen_ai.request.model","value":"gpt-4o"},`,
		`{"key":"gen_ai.request.model","value":"gpt-4o"},{"key":"gen_ai.input.messages","value":"0123456789"},{"key":"gen_ai.output.messages","value":"abcdefghij"},`, 1)
	srv, got := newGenAITestServer(t, fixedReply(body))
	stdout, _, err := runCmdSplit(t, srv.URL, newGenAITraceCmd(), output.FormatJSON, "t1", "--messages", "--preview-chars", "4")
	if err != nil {
		t.Fatal(err)
	}
	if got.query.Has("omitLarge") {
		t.Error("omitLarge is set with --messages")
	}
	var out struct {
		TraceID string        `json:"trace_id"`
		Summary genaiTraceRow `json:"summary"`
		Steps   []genaiStep   `json:"steps"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.TraceID != "t1" || out.Summary.TotalTokens != 250 || len(out.Steps) != 5 {
		t.Fatalf("unexpected output: %s", stdout)
	}
	if out.Steps[1].Input != "(10 chars) ...6789" || out.Steps[1].Output != "abcd... (10 chars)" {
		t.Errorf("previews = %q / %q", out.Steps[1].Input, out.Steps[1].Output)
	}
}

func TestGenAITrace_NotFound(t *testing.T) {
	srv, _ := newGenAITestServer(t, func(string) (int, string) { return 404, "" })
	_, _, err := runCmdSplit(t, srv.URL, newGenAITraceCmd(), output.FormatJSON, "t9")
	if err == nil || !strings.Contains(err.Error(), "widen it") {
		t.Errorf("err = %v", err)
	}
}

func TestGenAIValues(t *testing.T) {
	srv, got := newGenAITestServer(t, fixedReply(
		`{"data":["resource::service.name","resource::host.arch","span::gen_ai.agent.name","span::tenant"]}`))
	stdout, _, err := runCmdSplit(t, srv.URL, newGenAIValuesCmd(), output.FormatTable)
	if err != nil {
		t.Fatal(err)
	}
	if got.escapedPath != "/v1/api/instance/inst/traces/labels" || !strings.Contains(got.query.Get("filters"), genaiOperationLabel) {
		t.Errorf("path %q filters %q", got.escapedPath, got.query.Get("filters"))
	}
	for _, want := range []string{"agent", "built-in", "service", "tenant", "span attribute"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output does not contain %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "host.arch") {
		t.Errorf("output has a resource label without a short name:\n%s", stdout)
	}

	srv, got = newGenAITestServer(t, fixedReply(`{"data":["planner","writer"]}`))
	stdout, _, err = runCmdSplit(t, srv.URL, newGenAIValuesCmd(), output.FormatJSON, "agent", "--service", "support", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if got.escapedPath != "/v1/api/instance/inst/traces/labels/span::gen_ai.agent.name/values" {
		t.Errorf("path = %q", got.escapedPath)
	}
	if !strings.Contains(got.query.Get("filters"), `"resource::service.name","type":0,"value":"support"`) {
		t.Errorf("filters = %q", got.query.Get("filters"))
	}
	var out map[string][]string
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || len(out["agent"]) != 1 {
		t.Errorf("output = %s (%v)", stdout, err)
	}
}

const testGenAIGraphBody = `{"nodes":[
 {"operation":"search","type":"TOOL","stats":{"requestCount":{"current":10,"previous":0},"errorCount":{"current":5},"avgDurationNs":{"current":2000000},"traceCount":{"current":2}}},
 {"operation":"planner","type":"AGENT","stats":{"requestCount":{"current":20},"totalTokens":{"current":900},"totalCostDollars":{"current":1.25},"traceCount":{"current":20}}}],
 "edges":[{"source":{"operation":"planner","type":"AGENT"},"target":{"operation":"search","type":"TOOL"},"stats":{"requestCount":{"current":10}}}]}`

func TestGenAIAgentGraph(t *testing.T) {
	srv, got := newGenAITestServer(t, fixedReply(testGenAIGraphBody))
	stdout, _, err := runCmdSplit(t, srv.URL, newGenAIAgentGraphCmd(), output.FormatTable, "--service", "support", "--start", "1700000000000")
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.escapedPath != "/v1/api/instance/inst/graph/generate-genai-graph" {
		t.Errorf("%s %s", got.method, got.escapedPath)
	}
	var req map[string]any
	if err := json.Unmarshal(got.body, &req); err != nil {
		t.Fatal(err)
	}
	if req["serviceName"] != "support" || req["startTimeEpochMs"] != float64(1700000000000) {
		t.Errorf("request = %s", got.body)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// The node with the most requests comes first.
	if len(lines) != 3 || !strings.Contains(lines[1], "planner") || !strings.Contains(lines[2], "50.0%") ||
		!strings.Contains(lines[2], "5.0") || !strings.Contains(lines[1], "$1.25") {
		t.Errorf("unexpected table:\n%s", stdout)
	}

	srv, _ = newGenAITestServer(t, fixedReply(testGenAIGraphBody))
	stdout, _, err = runCmdSplit(t, srv.URL, newGenAIAgentGraphCmd(), output.FormatTable, "--edges")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "planner -> search") || !strings.Contains(stdout, "AGENT -> TOOL") {
		t.Errorf("unexpected edges table:\n%s", stdout)
	}

	srv, _ = newGenAITestServer(t, fixedReply(testGenAIGraphBody))
	stdout, _, err = runCmdSplit(t, srv.URL, newGenAIAgentGraphCmd(), output.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"total_nodes: 2", "from: planner", "calls_per_trace: 5"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("YAML does not contain %q:\n%s", want, stdout)
		}
	}
}

const testRecommendationsBody = `{"generated_at_epoch_ms":1700000000000,"traces_analyzed":34,
 "recommendations":[
  {"id":"r1","severity":"high","status":"new","category":"tool_error","category_group":"Invalid tool calls","title":"Tool fails","impact":"x"},
  {"id":"r2","severity":"medium","status":"stale","category":"slow_tool_calls","category_group":"Latency","title":"Slow tool"},
  {"id":"r3","severity":"high","status":"recurring","category":"loop","category_group":"Invalid tool calls","title":"Loop"}]}`

func TestGenAIRecommendations(t *testing.T) {
	srv, got := newGenAITestServer(t, fixedReply(testRecommendationsBody))
	stdout, _, err := runCmdSplit(t, srv.URL, newGenAIRecommendationsCmd(), output.FormatJSON, "--severity", "HIGH", "--category", "invalid")
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodGet || got.escapedPath != "/v1/api/instance/inst/genai/recommendations" {
		t.Errorf("%s %s", got.method, got.escapedPath)
	}
	var out struct {
		GeneratedAt     string           `json:"generated_at"`
		Total           int              `json:"total_recommendations"`
		Returned        int              `json:"returned"`
		Recommendations []map[string]any `json:"recommendations"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 3 || out.Returned != 2 || out.Recommendations[0]["impact"] != "x" || out.GeneratedAt != "2023-11-14T22:13:20Z" {
		t.Errorf("unexpected output: %s", stdout)
	}

	srv, _ = newGenAITestServer(t, fixedReply(testRecommendationsBody))
	stdout, stderr, err := runCmdSplit(t, srv.URL, newGenAIRecommendationsCmd(), output.FormatTable, "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Tool fails") || strings.Contains(stdout, "Slow tool") || !strings.Contains(stderr, "Showing 1 of 3") {
		t.Errorf("stdout:\n%s\nstderr: %q", stdout, stderr)
	}

	srv, _ = newGenAITestServer(t, fixedReply(testRecommendationsBody))
	if _, _, err := runCmdSplit(t, srv.URL, newGenAIRecommendationsCmd(), output.FormatTable, "--status", "open"); err == nil {
		t.Error("expected an error for an unknown status")
	}
}

func TestGenAIReadsEmptyHints(t *testing.T) {
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		body string
		args []string
		hint string
	}{
		{"traces", newGenAITracesCmd, `{"data":[]}`, nil, "No GenAI traces between"},
		{"traces by user", newGenAITracesCmd, `{"data":[]}`, []string{"--group-by", "user"}, "with a user ID"},
		{"values", newGenAIValuesCmd, `{"data":[]}`, []string{"tenant"}, `read as the span attribute span::tenant`},
		{"fields", newGenAIValuesCmd, `{"data":[]}`, nil, "No GenAI span fields between"},
		{"agent-graph", newGenAIAgentGraphCmd, `{"nodes":[],"edges":[]}`, nil, "No agent graph nodes between"},
		{"recommendations", newGenAIRecommendationsCmd, `{"recommendations":[]}`, nil, "No recommendations yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newGenAITestServer(t, fixedReply(tt.body))
			stdout, stderr, err := runCmdSplit(t, srv.URL, tt.cmd(), output.FormatJSON, tt.args...)
			if err != nil {
				t.Fatal(err)
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

func TestValuesMatcher(t *testing.T) {
	if _, ok := valuesMatcher("l", []string{" ", ""}); ok {
		t.Error("expected no matcher for empty values")
	}
	m, _ := valuesMatcher("l", []string{"a.b"})
	if m.Type != 0 || m.Value != "a.b" {
		t.Errorf("one value = %+v", m)
	}
	m, _ = valuesMatcher("l", []string{"a.b", "c|d"})
	if m.Type != 2 || m.Value != `(?:a\.b|c\|d)` {
		t.Errorf("two values = %+v", m)
	}
}
