package cmd

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

func TestDBMHosts_RequestAndTable(t *testing.T) {
	body := `[{"host":"db-1","dbInstanceIdentifier":"orders-prod","databaseType":"postgres","version":"16.2",` +
		`"count":1200,"avgDuration":12.5,"maxDuration":2500,"avgCPUUtilization":41.234}]`
	srv, got := newTraceQLTestServer(t, 200, body)
	out, err := runTraceQLCmd(t, srv, newDBMHostsCmd(), output.FormatTable, "inst",
		"--start", "1700000000", "--end", "1700003600", "--database-type", "postgres",
		"--host", "db-1", "--db-instance", "orders-prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/v1/api/instance/inst/dbm/database-hosts"; got.escapedPath != want {
		t.Errorf("path = %q, want %q", got.escapedPath, want)
	}
	for k, v := range map[string]string{
		"startTimeEpochMs":       "1700000000000",
		"endTimeEpochMs":         "1700003600000",
		"database_type":          "postgres",
		"host":                   "db-1",
		"db_instance_identifier": "orders-prod",
	} {
		if g := got.query.Get(k); g != v {
			t.Errorf("param %s = %q, want %q", k, g, v)
		}
	}
	if got.query.Has("version") {
		t.Errorf("an unset filter was sent: version")
	}
	for _, s := range []string{"db-1", "postgres", "16.2", "orders-prod", "1200", "12.5ms", "2.5s", "41.2"} {
		if !strings.Contains(out, s) {
			t.Errorf("table does not contain %q:\n%s", s, out)
		}
	}
}

const dbmSamplesBody = `[{"oodle_guid":"g1","timestamp":1700000000000,"query_signature":"sig1",` +
	`"normalized_query":"SELECT *\n  FROM orders WHERE id = ?","duration":2.5,"host":"db-1","database":"shop",` +
	`"user":"app","state":"active","wait_event_type":"Lock","wait_event":"transactionid"}]`

func TestDBMSamples_RequestAndTable(t *testing.T) {
	srv, got := newTraceQLTestServer(t, 200, dbmSamplesBody)
	out, err := runTraceQLCmd(t, srv, newDBMSamplesCmd(), output.FormatTable, "inst",
		"--limit", "20", "--sort-by", "duration", "--sort-order", "asc", "--query-signature", "sig1",
		"--table", "orders", "--table", "items", "--command", "SELECT",
		"--min-duration", "500ms", "--max-duration", "10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got.escapedPath, "/dbm/samples") {
		t.Errorf("path = %q", got.escapedPath)
	}
	for k, v := range map[string]string{
		"limit": "20", "sortBy": "duration", "sortOrder": "asc", "querySignature": "sig1",
		"commands": "SELECT", "minDuration": "0.5", "maxDuration": "10",
	} {
		if g := got.query.Get(k); g != v {
			t.Errorf("param %s = %q, want %q", k, g, v)
		}
	}
	if !slices.Equal(got.query["tables"], []string{"orders", "items"}) {
		t.Errorf("tables = %v, want [orders items]", got.query["tables"])
	}
	for _, s := range []string{"2023-11-14 22:13:20", "db-1", "shop", "app", "2.5s", "active",
		"Lock:transactionid", "sig1", "SELECT * FROM orders WHERE id = ?"} {
		if !strings.Contains(out, s) {
			t.Errorf("table does not contain %q:\n%s", s, out)
		}
	}
}

func TestDBMSamples_BadInput(t *testing.T) {
	for _, args := range [][]string{
		{"--limit", "0"},
		{"--limit", "1001"},
		{"--sort-order", "up"},
		{"--min-duration", "slow"},
	} {
		srv, got := newTraceQLTestServer(t, 200, `[]`)
		if _, err := runTraceQLCmd(t, srv, newDBMSamplesCmd(), output.FormatTable, "inst", args...); err == nil {
			t.Errorf("args %v: expected an error", args)
		}
		if got.escapedPath != "" {
			t.Errorf("args %v: a request was sent", args)
		}
	}
}

func TestDBMActivity_WithSignature(t *testing.T) {
	srv := newRouteTestServer(t, map[string]string{
		"/dbm/samples": dbmSamplesBody,
		"/dbm/queries/blocking-activity/waiting": `[{"blocker_pid":101,"blocker_host":"db-1",` +
			`"blocker_statement":"UPDATE orders SET x = ?","blocked_duration_ms":1500,"blocker_query_signature":"sig2",` +
			`"blocker_database_name":"shop","blocker_user_name":"batch"}]`,
		"/dbm/queries/blocking-activity/blocking": `[{"blocked_pid":202,"blocked_host":"db-1",` +
			`"blocked_statement":"DELETE FROM orders","blocking_duration_ms":250,"blocked_query_signature":"sig3"}]`,
	})
	out, err := runTraceQLCmd(t, srv.Server, newDBMActivityCmd(), output.FormatTable, "inst",
		"--query-signature", "sig1", "--limit", "5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r := srv.find("/dbm/samples"); r == nil || r.query.Get("limit") != "5" || r.query.Has("querySignature") {
		t.Errorf("samples request = %+v", r)
	}
	for _, suffix := range []string{"/blocking-activity/waiting", "/blocking-activity/blocking"} {
		if r := srv.find(suffix); r == nil || r.query.Get("querySignature") != "sig1" {
			t.Errorf("%s request = %+v", suffix, r)
		}
	}
	for _, s := range []string{"sig1", "Blocked by", "101", "UPDATE orders SET x = ?", "1.5s", "batch",
		"Blocking (", "202", "DELETE FROM orders", "250ms", "sig3"} {
		if !strings.Contains(out, s) {
			t.Errorf("output does not contain %q:\n%s", s, out)
		}
	}
}

func TestDBMActivity_NoSignatureJSON(t *testing.T) {
	srv := newRouteTestServer(t, map[string]string{"/dbm/samples": dbmSamplesBody})
	out, err := runTraceQLCmd(t, srv.Server, newDBMActivityCmd(), output.FormatJSON, "inst")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := len(srv.paths()); n != 1 {
		t.Errorf("requests = %v, want only samples", srv.paths())
	}
	if r := srv.find("/dbm/samples"); r.query.Get("limit") != "50" {
		t.Errorf("default limit = %q, want 50", r.query.Get("limit"))
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, out)
	}
	if !strings.Contains(string(resp["recent_samples"]), `"oodle_guid":"g1"`) {
		t.Errorf("recent_samples = %s", resp["recent_samples"])
	}
	if _, ok := resp["blocking_waiting"]; ok {
		t.Errorf("blocking lists must be absent without --query-signature: %s", out)
	}
}

func TestDBMExplain_RequestAndText(t *testing.T) {
	body := `[{"query_signature":"sig1","explain_plans":["{\"Plan\":{\"Node Type\":\"Seq Scan\"}}","Index Scan on orders"]}]`
	srv, got := newTraceQLTestServer(t, 200, body)
	out, err := runTraceQLCmd(t, srv, newDBMExplainCmd(), output.FormatTable, "inst", "--start", "-24h", "sig1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got.escapedPath, "/dbm/queries/explain-plans") || got.query.Get("querySignature") != "sig1" {
		t.Errorf("request = %s %v", got.escapedPath, got.query)
	}
	for _, s := range []string{"--- Plan 1 (signature sig1) ---", "\"Node Type\": \"Seq Scan\"",
		"--- Plan 2 (signature sig1) ---", "Index Scan on orders"} {
		if !strings.Contains(out, s) {
			t.Errorf("output does not contain %q:\n%s", s, out)
		}
	}

	jsonOut, err := runTraceQLCmd(t, srv, newDBMExplainCmd(), output.FormatJSON, "inst", "sig1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(jsonOut) != body {
		t.Errorf("JSON output is not the response body:\n%s", jsonOut)
	}
}

func TestParseDurationSeconds(t *testing.T) {
	for in, want := range map[string]float64{"2": 2, "0.25": 0.25, "500ms": 0.5, "1m": 60} {
		got, err := parseDurationSeconds(in)
		if err != nil || got != want {
			t.Errorf("parseDurationSeconds(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseDurationSeconds("fast"); err == nil {
		t.Errorf("expected an error for an invalid duration")
	}
}
