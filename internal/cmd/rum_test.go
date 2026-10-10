package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// routeTestServer answers each request with the body for the last path
// segment(s) that match, and records every request.
type routeTestServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []traceQLRequest
}

func (s *routeTestServer) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.requests))
	for i, r := range s.requests {
		out[i] = r.escapedPath
	}
	return out
}

func (s *routeTestServer) find(suffix string) *traceQLRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.requests {
		if strings.HasSuffix(s.requests[i].escapedPath, suffix) {
			return &s.requests[i]
		}
	}
	return nil
}

// newRouteTestServer returns a server that replies with bodies[suffix] for
// the first suffix that the request path ends with, and 404 otherwise.
func newRouteTestServer(t *testing.T, bodies map[string]string) *routeTestServer {
	t.Helper()
	s := &routeTestServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, traceQLRequest{
			escapedPath: r.URL.EscapedPath(),
			query:       r.URL.Query(),
			apiKey:      r.Header.Get("X-API-Key"),
		})
		s.mu.Unlock()
		best := ""
		for suffix := range bodies {
			if strings.HasSuffix(r.URL.Path, suffix) && len(suffix) > len(best) {
				best = suffix
			}
		}
		if best == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, bodies[best])
	}))
	t.Cleanup(s.Close)
	return s
}

const rumSessionsBody = `[` +
	`{"session_id":"s1","user_id":"u1","user_email":"a@example.com","browser_name":"Chrome","geo_country":"US",` +
	`"start_time":"2026-01-02T10:00:00Z","end_time":"2026-01-02T10:05:30Z","view_count":4,"error_count":2,` +
	`"action_count":7,"frustration_count":0,"extra_field":"kept"},` +
	`{"session_id":"s2","user_id":"u2","start_time":"2026-01-02T11:00:00Z","end_time":"2026-01-02T11:00:10Z",` +
	`"view_count":1,"error_count":0,"action_count":0,"frustration_count":1}]`

func TestRUMSessions_RequestAndTable(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, rumSessionsBody)
	out, err := runTraceQLCmd(t, srv, newRUMSessionsCmd(), output.FormatTable, "inst",
		"--start", "1700000000", "--end", "1700003600000", "--limit", "50",
		"--user-id", "u1", "--view-path", "/checkout", "--browser", "Chrome",
		"--where", "plan=pro", "--where", "resource_url=~/api/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/v1/api/instance/inst/rum/sessions"; got.escapedPath != want {
		t.Errorf("path = %q, want %q", got.escapedPath, want)
	}
	want := map[string]string{
		// The start was given in seconds; the route reads milliseconds.
		"startTimeEpochMs": "1700000000000",
		"endTimeEpochMs":   "1700003600000",
		"limit":            "50",
		"userId":           "u1",
		"viewUrlPath":      "/checkout",
		"browserName":      "Chrome",
		"tagFilters":       `[{"key":"plan","operator":"eq","values":["pro"]},{"key":"resource_url","operator":"re","values":["/api/"]}]`,
	}
	for k, v := range want {
		if g := got.query.Get(k); g != v {
			t.Errorf("param %s = %q, want %q", k, g, v)
		}
	}
	if got.query.Has("deviceType") {
		t.Errorf("an unset filter was sent: deviceType")
	}
	for _, s := range []string{"SESSION ID", "s1", "a@example.com", "2026-01-02 10:00:00", "5m30s", "Chrome", "u2"} {
		if !strings.Contains(out, s) {
			t.Errorf("table does not contain %q:\n%s", s, out)
		}
	}
}

func TestRUMSessions_HasErrorsKeepsFields(t *testing.T) {
	srv, _ := newTraceQLTestServer(t, 200, rumSessionsBody)
	out, err := runTraceQLCmd(t, srv, newRUMSessionsCmd(), output.FormatJSON, "inst", "--has-errors")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var sessions []map[string]any
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out)
	}
	if len(sessions) != 1 || sessions[0]["session_id"] != "s1" {
		t.Fatalf("sessions = %v, want only s1", sessions)
	}
	if sessions[0]["extra_field"] != "kept" {
		t.Errorf("a field that the CLI does not know was dropped: %v", sessions[0])
	}
}

func TestParseRUMWhere(t *testing.T) {
	tests := []struct {
		in      []string
		want    string
		wantErr bool
	}{
		{nil, "", false},
		{[]string{"a=1"}, `[{"key":"a","operator":"eq","values":["1"]}]`, false},
		{[]string{"a!=1"}, `[{"key":"a","operator":"neq","values":["1"]}]`, false},
		{[]string{"a=~x.*"}, `[{"key":"a","operator":"re","values":["x.*"]}]`, false},
		{[]string{"a!~x"}, `[{"key":"a","operator":"nre","values":["x"]}]`, false},
		{[]string{"a=1", "a=2"}, `[{"key":"a","operator":"oneof","values":["1","2"]}]`, false},
		{[]string{"a=~x", "a=~y"}, `[{"key":"a","operator":"re","values":["x","y"]}]`, false},
		{[]string{"a!=1", "a!=2"}, `[{"key":"a","operator":"neq","values":["1"]},{"key":"a","operator":"neq","values":["2"]}]`, false},
		{[]string{"url=https://x/?q=1"}, `[{"key":"url","operator":"eq","values":["https://x/?q=1"]}]`, false},
		{[]string{"novalue"}, "", true},
		{[]string{"=x"}, "", true},
		{[]string{"a="}, "", true},
	}
	for _, tt := range tests {
		got, err := parseRUMWhere(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseRUMWhere(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseRUMWhere(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestRUMSessionEvents_AllTypesTimeline(t *testing.T) {
	srv := newRouteTestServer(t, map[string]string{
		"/rum/views":     `[{"timestamp":"2026-01-02T10:00:00Z","view_url":"https://shop.example.com/cart","view_url_path":"/cart"}]`,
		"/rum/errors":    `[{"timestamp":"2026-01-02T10:00:03Z","error_type":"TypeError","error_message":"x is undefined","view_url_path":"/cart"}]`,
		"/rum/actions":   `[{"timestamp":"2026-01-02T10:00:01Z","action_type":"click","action_target_name":"Pay","frustration_type":"rage_click"}]`,
		"/rum/resources": `[{"timestamp":"2026-01-02T10:00:02Z","resource_method":"POST","resource_url":"https://shop.example.com/api/pay","resource_status_code":500,"resource_duration_ns":416.5}]`,
		"/rum/console":   `[]`,
	})
	out, err := runTraceQLCmd(t, srv.Server, newRUMSessionEventsCmd(), output.FormatTable, "inst",
		"--limit", "10", "--views-group-by", "url", "sess-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := len(srv.paths()); n != 5 {
		t.Errorf("requests = %d (%v), want 5", n, srv.paths())
	}
	views := srv.find("/rum/views")
	if views == nil || views.query.Get("sessionId") != "sess-1" || views.query.Get("limit") != "10" ||
		views.query.Get("viewsGroupBy") != "url" {
		t.Errorf("views request = %+v", views)
	}
	if r := srv.find("/rum/errors"); r == nil || r.query.Has("viewsGroupBy") {
		t.Errorf("viewsGroupBy must be sent only for views: %+v", r)
	}
	order := []string{"https://shop.example.com/cart", `click "Pay" [rage_click]`,
		"POST https://shop.example.com/api/pay 500 416.5ms", "TypeError: x is undefined"}
	last := -1
	for _, s := range order {
		i := strings.Index(out, s)
		if i < 0 {
			t.Fatalf("table does not contain %q:\n%s", s, out)
		}
		if i < last {
			t.Errorf("%q is not in time order:\n%s", s, out)
		}
		last = i
	}
}

func TestRUMSessionEvents_TypesAndJSON(t *testing.T) {
	srv := newRouteTestServer(t, map[string]string{
		"/rum/errors":  `[{"timestamp":"2026-01-02T10:00:03Z","error_message":"boom","error_stack":"at f"}]`,
		"/rum/console": `[]`,
	})
	out, err := runTraceQLCmd(t, srv.Server, newRUMSessionEventsCmd(), output.FormatJSON, "inst",
		"--types", "console,errors", "--where", "view_url_path=/cart", "sess-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := srv.paths(); len(got) != 2 || !strings.HasSuffix(got[0], "/rum/errors") {
		t.Errorf("requests = %v, want errors then console", got)
	}
	if r := srv.find("/rum/errors"); r.query.Get("tagFilters") == "" {
		t.Errorf("tagFilters not sent")
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, out)
	}
	if string(resp["session_id"]) != `"sess-1"` || !strings.Contains(string(resp["errors"]), `"error_stack":"at f"`) {
		t.Errorf("JSON output = %s", out)
	}
	if _, ok := resp["views"]; ok {
		t.Errorf("JSON has a type that was not requested: %s", out)
	}
}

func TestRUMSessionEvents_BadInput(t *testing.T) {
	srv := newRouteTestServer(t, map[string]string{})
	for _, args := range [][]string{
		{"--types", "views,clicks", "s"},
		{"--views-group-by", "host", "s"},
		{"--limit", "0", "s"},
	} {
		if _, err := runTraceQLCmd(t, srv.Server, newRUMSessionEventsCmd(), output.FormatTable, "inst", args...); err == nil {
			t.Errorf("args %v: expected an error", args)
		}
	}
	if n := len(srv.paths()); n != 0 {
		t.Errorf("requests were sent for bad input: %v", srv.paths())
	}
}

func TestRUMIssues_RequestAndTable(t *testing.T) {
	body := `[{"fingerprint":"fp1","error_type":"TypeError","error_message":"x is undefined","error_source":"source",` +
		`"occurrence_count":42,"affected_sessions":7,"affected_users":3,"last_seen":"2026-01-02T10:00:00Z",` +
		`"sample_page":"/cart","sample_session_id":"sess-9"}]`
	srv, got := newTraceQLTestServer(t, 200, body)
	out, err := runTraceQLCmd(t, srv, newRUMIssuesCmd(), output.FormatTable, "inst",
		"--source", "console", "--search", "fetch", "--error-type", "TypeError", "--limit", "5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got.escapedPath, "/rum/issues") {
		t.Errorf("path = %q", got.escapedPath)
	}
	for k, v := range map[string]string{"issueSource": "console", "search": "fetch", "errorType": "TypeError", "limit": "5"} {
		if g := got.query.Get(k); g != v {
			t.Errorf("param %s = %q, want %q", k, g, v)
		}
	}
	for _, s := range []string{"TypeError", "x is undefined", "42", "/cart", "sess-9", "2026-01-02 10:00:00"} {
		if !strings.Contains(out, s) {
			t.Errorf("table does not contain %q:\n%s", s, out)
		}
	}
	if _, err := runTraceQLCmd(t, srv, newRUMIssuesCmd(), output.FormatTable, "inst", "--source", "network"); err == nil {
		t.Errorf("expected an error for --source network")
	}
}

func TestRUMErrors_MessageContains(t *testing.T) {
	body := `[{"timestamp":"2026-01-02T10:00:00Z","error_type":"Error","error_message":"Request TIMEOUT after 30s","session_id":"s1"},` +
		`{"timestamp":"2026-01-02T10:01:00Z","error_type":"Error","error_message":"x is undefined","session_id":"s2"}]`
	srv, got := newTraceQLTestServer(t, 200, body)
	out, err := runTraceQLCmd(t, srv, newRUMErrorsCmd(), output.FormatTable, "inst",
		"--session-id", "s1", "--error-source", "network", "--message-contains", "timeout")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got.escapedPath, "/rum/errors") || got.query.Get("sessionId") != "s1" ||
		got.query.Get("errorSource") != "network" || got.query.Get("limit") != "100" {
		t.Errorf("request = %s %v", got.escapedPath, got.query)
	}
	if got.query.Has("message_contains") || got.query.Has("messageContains") {
		t.Errorf("--message-contains must not be sent to the server")
	}
	if !strings.Contains(out, "Request TIMEOUT") || strings.Contains(out, "x is undefined") {
		t.Errorf("--message-contains did not filter the rows:\n%s", out)
	}
}

func TestShortCell(t *testing.T) {
	if got := shortCell("a\n  b\tc", 10); got != "a b c" {
		t.Errorf("shortCell = %q", got)
	}
	if got := shortCell(strings.Repeat("x", 20), 10); got != "xxxxxxx..." {
		t.Errorf("shortCell = %q", got)
	}
}
