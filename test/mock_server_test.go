//go:build integration

package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runMock runs the oodle binary pointing at the given mock server URL.
// It uses a fresh isolated home directory so real credentials don't interfere.
func runMock(t *testing.T, serverURL string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	homeDir, err := os.MkdirTemp("", "oodle-home-*")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(homeDir) })

	allArgs := append([]string{
		"--api-key", "test-api-key",
		"--instance", "test-instance",
		"--api-url", serverURL,
	}, args...)

	c := exec.Command(oodleBin, allArgs...)
	// Clear real credentials from environment.
	c.Env = append(os.Environ(), "HOME="+homeDir, "OODLE_API_KEY=", "OODLE_INSTANCE=", "OODLE_URL=", "OODLE_API_URL=")
	var outBuf, errBuf strings.Builder
	c.Stdout = &outBuf
	c.Stderr = &errBuf
	if runErr := c.Run(); runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}
	return outBuf.String(), errBuf.String(), code
}

// assertValidJSONMock asserts that stdout is valid JSON.
func assertValidJSONMock(t *testing.T, stdout string) {
	t.Helper()
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatal("expected JSON output, got empty string")
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		t.Fatalf("invalid JSON: %v\noutput:\n%s", err, stdout)
	}
}

// jsonSrv starts a mock server returning the given JSON at any path.
func jsonSrv(payload string, statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		fmt.Fprint(w, payload)
	}))
}

// -------------------------------------------------------------------------
// Mock: Monitors
// -------------------------------------------------------------------------

func TestMock_MonitorsList_JSON(t *testing.T) {
	srv := jsonSrv(`[{"name":"alpha","id":"00000000-0000-0000-0000-000000000001","promql_query":"up","interval":"60s"}]`, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "monitors", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "alpha") {
		t.Errorf("expected 'alpha' in output, got: %s", stdout)
	}
}

func TestMock_MonitorsList_Table(t *testing.T) {
	srv := jsonSrv(`[{"name":"alpha","id":"00000000-0000-0000-0000-000000000001","promql_query":"up","interval":"60s"}]`, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "monitors", "list", "--output", "table")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "NAME") {
		t.Errorf("expected 'NAME' column header in table output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "alpha") {
		t.Errorf("expected 'alpha' in table output, got: %s", stdout)
	}
}

func TestMock_MonitorsList_CSV(t *testing.T) {
	srv := jsonSrv(`[{"name":"csv-mon","id":"00000000-0000-0000-0000-000000000001","promql_query":"up","interval":"60s"}]`, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "monitors", "list", "--output", "csv")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected CSV header+data, got %d lines: %s", len(lines), stdout)
	}
	if !strings.Contains(lines[0], ",") {
		t.Errorf("expected comma in CSV header, got: %s", lines[0])
	}
	if !strings.Contains(stdout, "csv-mon") {
		t.Errorf("expected 'csv-mon' in CSV output, got: %s", stdout)
	}
}

func TestMock_MonitorsList_YAML(t *testing.T) {
	srv := jsonSrv(`[{"name":"yaml-mon","id":"00000000-0000-0000-0000-000000000001","promql_query":"up","interval":"60s"}]`, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "monitors", "list", "--output", "yaml")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected YAML output, got empty")
	}
	if !strings.Contains(stdout, "yaml-mon") {
		t.Errorf("expected 'yaml-mon' in YAML output, got: %s", stdout)
	}
}

func TestMock_MonitorsGet_Valid(t *testing.T) {
	payload := `{"name":"my-monitor","id":"00000000-0000-0000-0000-000000000001","promql_query":"up","interval":"60s"}`
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/instance/test-instance/monitors/00000000-0000-0000-0000-000000000001",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			fmt.Fprint(w, payload)
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "monitors", "get", "00000000-0000-0000-0000-000000000001", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "my-monitor") {
		t.Errorf("expected 'my-monitor', got: %s", stdout)
	}
}

func TestMock_MonitorsGet_NotFound(t *testing.T) {
	srv := jsonSrv(`{"message":"not found"}`, 404)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "monitors", "get", "nonexistent-id", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit for 404")
	}
	if !strings.Contains(strings.ToLower(stderr), "not found") && !strings.Contains(stderr, "404") {
		t.Errorf("expected 'not found' or '404' error, got: %s", stderr)
	}
}

func TestMock_MonitorsTriggers(t *testing.T) {
	srv := jsonSrv(`[{"monitor_id":"mon1","severity":"critical","starts_at":"2024-01-01T00:00:00Z"}]`, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "monitors", "triggers", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_MonitorsDelete_NoArgs(t *testing.T) {
	apiCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
	}))
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "monitors", "delete", "--force")
	if code == 0 {
		t.Fatal("expected non-zero exit when no ID specified")
	}
	if apiCalled {
		t.Error("API should not have been called when no ID is provided")
	}
	if !strings.Contains(strings.ToLower(stderr), "id") && !strings.Contains(strings.ToLower(stderr), "required") {
		t.Errorf("expected error mentioning id/required, got: %s", stderr)
	}
}

func TestMock_MonitorsDelete_BothArgAndFlag(t *testing.T) {
	srv := jsonSrv(`{}`, 200)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "monitors", "delete", "some-id", "--ids", "other-id", "--force")
	if code == 0 {
		t.Fatal("expected non-zero exit with both positional arg and --ids")
	}
	if !strings.Contains(stderr, "both") && !strings.Contains(stderr, "not both") {
		t.Errorf("expected error about 'both', got: %s", stderr)
	}
}

func TestMock_MonitorsDelete_Single(t *testing.T) {
	methodUsed := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methodUsed = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":"00000000-0000-0000-0000-000000000001"}`)
	}))
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "monitors", "delete", "00000000-0000-0000-0000-000000000001", "--force")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if methodUsed != http.MethodDelete {
		t.Errorf("expected DELETE HTTP method, got: %s", methodUsed)
	}
}

// -------------------------------------------------------------------------
// Mock: Auth error handling
// -------------------------------------------------------------------------

func TestMock_AuthError_401(t *testing.T) {
	srv := jsonSrv(`{"message":"Authentication failed. Check your API key."}`, 401)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "monitors", "list", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit on 401")
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "auth") && !strings.Contains(combined, "api key") && !strings.Contains(combined, "401") {
		t.Errorf("expected auth-related error, got: %s", stderr)
	}
}

// -------------------------------------------------------------------------
// Mock: Missing credentials
// -------------------------------------------------------------------------

func TestMock_MissingInstance(t *testing.T) {
	srv := jsonSrv(`[]`, 200)
	defer srv.Close()

	homeDir, _ := os.MkdirTemp("", "oodle-home-*")
	defer os.RemoveAll(homeDir)

	cmd := exec.Command(oodleBin, "--api-key", "test-key", "--output", "json", "monitors", "list")
	cmd.Env = append(os.Environ(), "HOME="+homeDir, "OODLE_INSTANCE=", "OODLE_API_KEY=", "OODLE_URL=", "OODLE_API_URL=")
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	cmd.Run()

	combined := strings.ToLower(out.String() + errBuf.String())
	if !strings.Contains(combined, "instance") {
		t.Errorf("expected error mentioning 'instance', got stdout=%s stderr=%s", out.String(), errBuf.String())
	}
}

func TestMock_MissingAPIKey(t *testing.T) {
	srv := jsonSrv(`[]`, 200)
	defer srv.Close()

	homeDir, _ := os.MkdirTemp("", "oodle-home-*")
	defer os.RemoveAll(homeDir)

	cmd := exec.Command(oodleBin, "--instance", "test-instance", "--output", "json", "monitors", "list")
	cmd.Env = append(os.Environ(), "HOME="+homeDir, "OODLE_INSTANCE=", "OODLE_API_KEY=", "OODLE_URL=", "OODLE_API_URL=")
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	cmd.Run()

	combined := strings.ToLower(out.String() + errBuf.String())
	if !strings.Contains(combined, "api key") && !strings.Contains(combined, "api_key") {
		t.Errorf("expected error mentioning 'api key', got stdout=%s stderr=%s", out.String(), errBuf.String())
	}
}

// -------------------------------------------------------------------------
// Mock: --retries 0 bug fix verification
// -------------------------------------------------------------------------

func TestMock_RetriesZero_SingleAttempt(t *testing.T) {
	// A server that always returns 500.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		fmt.Fprint(w, `{"message":"server error"}`)
	}))
	defer srv.Close()

	start := time.Now()
	_, _, code := runMock(t, srv.URL, "--retries", "0", "monitors", "list", "--output", "json")
	elapsed := time.Since(start)

	if code == 0 {
		t.Fatal("expected non-zero exit on 500")
	}
	// With 0 retries (1 attempt only), should complete in well under 2s.
	if elapsed > 3*time.Second {
		t.Errorf("--retries 0 took %v, expected <3s (should make exactly 1 attempt)", elapsed)
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 API call with --retries 0, got %d (the bug was: 0 → 3 retries)", callCount)
	}
}

func TestMock_RetriesOne_TwoAttempts(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		fmt.Fprint(w, `{"message":"server error"}`)
	}))
	defer srv.Close()

	runMock(t, srv.URL, "--retries", "1", "monitors", "list", "--output", "json")
	if callCount != 2 {
		t.Errorf("expected 2 API calls with --retries 1 (1 retry = 2 attempts total), got %d", callCount)
	}
}

// -------------------------------------------------------------------------
// Mock: API key passed in X-API-Key header
// -------------------------------------------------------------------------

func TestMock_APIKeyInHeader(t *testing.T) {
	receivedKey := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedKey = r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	runMock(t, srv.URL, "--api-key", "my-special-key-xyz", "monitors", "list", "--output", "json")
	if receivedKey != "my-special-key-xyz" {
		t.Errorf("expected X-API-Key header 'my-special-key-xyz', got: %q", receivedKey)
	}
}

// -------------------------------------------------------------------------
// Mock: Other command groups list endpoints
// -------------------------------------------------------------------------

func TestMock_NotifiersList(t *testing.T) {
	srv := jsonSrv(`[{"name":"slack","type":1}]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "notifiers", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_NotificationPoliciesList(t *testing.T) {
	srv := jsonSrv(`[{"name":"default","id":"00000000-0000-0000-0000-000000000001"}]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "notification-policies", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_MutingRulesList(t *testing.T) {
	// MutingRule.Id is a plain string, not an object.
	// Required non-optional fields: comment, createdBy, endsAt, id, matchers
	srv := jsonSrv(`[{"name":"maintenance","id":"muting-id-001","comment":"","createdBy":"","endsAt":"2024-01-01T00:00:00Z","matchers":null}]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "muting-rules", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_LogMetricsList(t *testing.T) {
	srv := jsonSrv(`[{"name":"error_rate","id":"00000000-0000-0000-0000-000000000001"}]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "log-metrics", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_SyntheticMonitorsList(t *testing.T) {
	// SyntheticMonitor: wrapped in {"monitors":[...]}.
	srv := jsonSrv(`{"monitors":[{"name":"homepage-check","id":"sm-001","enabled":true,"instance":"test","interval":"60s","timeout":"10s","rule_type":"http","rule_config":{}}]}`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "synthetic-monitors", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_DashboardsList(t *testing.T) {
	srv := jsonSrv(`[{"title":"Main Dashboard","id":1}]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "dashboards", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_FoldersList(t *testing.T) {
	srv := jsonSrv(`[{"title":"My Folder","id":1}]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "folders", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_DropRulesList(t *testing.T) {
	// DropRule: id is a plain string, not an object. Also has required fields: metric_name, rule_name, type.
	srv := jsonSrv(`[{"id":"drop-id-001","rule_name":"drop-debug","type":"drop","metric_name":{}}]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "drop-rules", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_MetricsNames(t *testing.T) {
	srv := jsonSrv(`["up","http_requests_total"]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "metrics", "names", "--start", "-1h", "--end", "now", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_TracesLabels(t *testing.T) {
	srv := jsonSrv(`{"data":["service","env"],"total":2,"limit":0,"offset":0}`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "traces", "labels", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_ApiKeysList(t *testing.T) {
	srv := jsonSrv(`[{"name":"my-key","id":"key-001"}]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "api-keys", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

func TestMock_UsersList(t *testing.T) {
	// ListUsersResponse is a struct with a "users" field, not a plain array.
	srv := jsonSrv(`{"users":[{"email":"user@example.com","user_id":"user-001"}]}`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "users", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
}

// -------------------------------------------------------------------------
// Mock: Configure command
// -------------------------------------------------------------------------

func TestMock_Configure_SavesConfig(t *testing.T) {
	homeDir, _ := os.MkdirTemp("", "oodle-home-*")
	defer os.RemoveAll(homeDir)

	// configure makes a validation API call which will fail here (no mock server for configure).
	// That's fine — we just want to verify the config file was written.
	// Use --retries 0 to avoid slow exponential-backoff retries on the connection failure.
	cmd := exec.Command(oodleBin, "configure",
		"--api-key", "saved-api-key",
		"--instance", "saved-instance",
		"--api-url", "https://example.test",
		"--retries", "0",
	)
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	cmd.Run() // exit code might be non-zero due to validation failure

	configPath := filepath.Join(homeDir, ".oodle", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config file not created at %s: %v\nstdout: %s\nstderr: %s", configPath, err, out.String(), errBuf.String())
	}
	configStr := string(data)
	if !strings.Contains(configStr, "saved-api-key") {
		t.Errorf("expected api_key='saved-api-key' in config, got:\n%s", configStr)
	}
	if !strings.Contains(configStr, "saved-instance") {
		t.Errorf("expected instance='saved-instance' in config, got:\n%s", configStr)
	}
}

// -------------------------------------------------------------------------
// Mock: Content-Type requirement
// -------------------------------------------------------------------------

func TestMock_NoContentType_StillParsesJSON(t *testing.T) {
	// Server omits Content-Type header (Go auto-detects as text/plain).
	// The jsonFixTransport normalizes text/plain responses to
	// application/json so oapi-codegen can parse them.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, `[{"name":"test"}]`)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "monitors", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 (jsonFixTransport should normalize Content-Type), got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "test") {
		t.Errorf("expected 'test' in output, got: %s", stdout)
	}
}

func TestMock_MetricsLabels(t *testing.T) {
	srv := jsonSrv(`["job","instance","env"]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "metrics", "labels", "up", "--start", "-1h", "--end", "now", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "job") {
		t.Errorf("expected 'job' in output, got: %s", stdout)
	}
}

func TestMock_MetricsLabelValues(t *testing.T) {
	srv := jsonSrv(`["prometheus","grafana","oodle"]`, 200)
	defer srv.Close()
	stdout, stderr, code := runMock(t, srv.URL, "metrics", "label-values", "up", "job", "--start", "-1h", "--end", "now", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "prometheus") {
		t.Errorf("expected 'prometheus' in output, got: %s", stdout)
	}
}

func TestMock_MetricsNames_DefaultTimeRange(t *testing.T) {
	srv, capturedQuery, _ := metricsQueryServer(`["up"]`)
	defer srv.Close()
	// Omit --start and --end; they should default to -1h and now.
	stdout, stderr, code := runMock(t, srv.URL, "metrics", "names", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 with default time range, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	// Verify that startTimeEpochMs and endTimeEpochMs were sent as query params.
	if !strings.Contains(*capturedQuery, "startTimeEpochMs=") {
		t.Errorf("expected startTimeEpochMs in query %q", *capturedQuery)
	}
	if !strings.Contains(*capturedQuery, "endTimeEpochMs=") {
		t.Errorf("expected endTimeEpochMs in query %q", *capturedQuery)
	}
}

func TestMock_MetricsLabels_DefaultTimeRange(t *testing.T) {
	srv, capturedQuery, _ := metricsQueryServer(`["job"]`)
	defer srv.Close()
	// Omit --end; it should default to now.
	stdout, stderr, code := runMock(t, srv.URL, "metrics", "labels", "up", "--start", "-1h", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 with default --end, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(*capturedQuery, "startTimeEpochMs=") {
		t.Errorf("expected startTimeEpochMs in query %q", *capturedQuery)
	}
	if !strings.Contains(*capturedQuery, "endTimeEpochMs=") {
		t.Errorf("expected endTimeEpochMs in query %q", *capturedQuery)
	}
}

func TestMock_MetricsLabelValues_DefaultTimeRange(t *testing.T) {
	srv, capturedQuery, _ := metricsQueryServer(`["val"]`)
	defer srv.Close()
	// Omit both --start and --end; they should default to -1h and now.
	stdout, stderr, code := runMock(t, srv.URL, "metrics", "label-values", "up", "job", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 with default time range, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(*capturedQuery, "startTimeEpochMs=") {
		t.Errorf("expected startTimeEpochMs in query %q", *capturedQuery)
	}
	if !strings.Contains(*capturedQuery, "endTimeEpochMs=") {
		t.Errorf("expected endTimeEpochMs in query %q", *capturedQuery)
	}
}

// metricsQueryServer returns an httptest.Server that captures the request
// query string and path, and responds with the given JSON body. It is used
// to verify that the CLI wires --start/--end through to the wire request.
func metricsQueryServer(body string) (*httptest.Server, *string, *string) {
	var capturedQuery, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, body)
	}))
	return srv, &capturedQuery, &capturedPath
}

func TestMock_MetricsNames_SendsQueryParams(t *testing.T) {
	srv, capturedQuery, _ := metricsQueryServer(`["up"]`)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "metrics", "names",
		"--start", "1700000000000", "--end", "1700003600000", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(*capturedQuery, "startTimeEpochMs=1700000000000") {
		t.Errorf("expected startTimeEpochMs=1700000000000 in query %q", *capturedQuery)
	}
	if !strings.Contains(*capturedQuery, "endTimeEpochMs=1700003600000") {
		t.Errorf("expected endTimeEpochMs=1700003600000 in query %q", *capturedQuery)
	}
}

func TestMock_MetricsLabels_SendsQueryParams(t *testing.T) {
	srv, capturedQuery, capturedPath := metricsQueryServer(`["job"]`)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "metrics", "labels", "up",
		"--start", "1700000000000", "--end", "1700003600000", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(*capturedQuery, "startTimeEpochMs=1700000000000") {
		t.Errorf("expected startTimeEpochMs=1700000000000 in query %q", *capturedQuery)
	}
	if !strings.Contains(*capturedQuery, "endTimeEpochMs=1700003600000") {
		t.Errorf("expected endTimeEpochMs=1700003600000 in query %q", *capturedQuery)
	}
	// Sanity-check that the metric name is in the path so we know we hit
	// the labels endpoint (and not, say, names) with the right routing.
	if !strings.Contains(*capturedPath, "up") {
		t.Errorf("expected metric name 'up' in path %q", *capturedPath)
	}
}

func TestMock_MetricsLabelValues_SendsQueryParams(t *testing.T) {
	srv, capturedQuery, capturedPath := metricsQueryServer(`["prometheus"]`)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "metrics", "label-values", "up", "job",
		"--start", "1700000000000", "--end", "1700003600000", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(*capturedQuery, "startTimeEpochMs=1700000000000") {
		t.Errorf("expected startTimeEpochMs=1700000000000 in query %q", *capturedQuery)
	}
	if !strings.Contains(*capturedQuery, "endTimeEpochMs=1700003600000") {
		t.Errorf("expected endTimeEpochMs=1700003600000 in query %q", *capturedQuery)
	}
	// Both metric name and label name should appear in the path.
	if !strings.Contains(*capturedPath, "up") || !strings.Contains(*capturedPath, "job") {
		t.Errorf("expected metric 'up' and label 'job' in path %q", *capturedPath)
	}
}

// -------------------------------------------------------------------------
// Mock: Metrics Query (instant)
// -------------------------------------------------------------------------

func TestMock_MetricsQuery_JSON(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up","job":"prometheus"},"value":[1700000000,"1"]}]}}`
	var capturedQuery, capturedPath, capturedInstanceHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		capturedPath = r.URL.Path
		capturedInstanceHeader = r.Header.Get("OODLE-INSTANCE")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "up", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "success") {
		t.Errorf("expected 'success' in output, got: %s", stdout)
	}
	if capturedPath != "/api/v1/query" {
		t.Errorf("expected path /api/v1/query, got: %s", capturedPath)
	}
	if !strings.Contains(capturedQuery, "query=up") {
		t.Errorf("expected query=up in query string %q", capturedQuery)
	}
	if capturedInstanceHeader != "test-instance" {
		t.Errorf("expected OODLE-INSTANCE header 'test-instance', got: %q", capturedInstanceHeader)
	}
}

func TestMock_MetricsQuery_ScalarResult(t *testing.T) {
	// Prometheus scalar results use a tuple [timestamp, "value"] instead of
	// an array of objects. The generated typed parser would fail on this, so
	// we use the raw client method + printQueryResponse to handle it.
	payload := `{"status":"success","data":{"resultType":"scalar","result":[1700000000,"2"]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "1+1", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 for scalar result, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "scalar") {
		t.Errorf("expected 'scalar' in output, got: %s", stdout)
	}
}

func TestMock_MetricsQuery_WithTime(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"vector","result":[]}}`
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "up", "--time", "1700000000", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(capturedQuery, "time=17") {
		t.Errorf("expected time param in query string %q", capturedQuery)
	}
}

func TestMock_MetricsQuery_MissingQuery(t *testing.T) {
	srv := jsonSrv(`{}`, 200)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "metrics", "query", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit when --query is missing")
	}
	if !strings.Contains(stderr, "query") {
		t.Errorf("expected 'query' in error, got: %s", stderr)
	}
}

func TestMock_MetricsQuery_400Error(t *testing.T) {
	payload := `{"status":"error","errorType":"bad_data","error":"invalid expression"}`
	srv := jsonSrv(payload, 400)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "invalid{", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit on 400")
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "invalid") && !strings.Contains(combined, "error") && !strings.Contains(combined, "400") {
		t.Errorf("expected error message, got: %s", stderr)
	}
}

func TestMock_MetricsQuery_401Error(t *testing.T) {
	srv := jsonSrv(`{"message":"unauthorized"}`, 401)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "up", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit on 401")
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "auth") && !strings.Contains(combined, "api key") {
		t.Errorf("expected auth-related error, got: %s", stderr)
	}
}

// -------------------------------------------------------------------------
// Mock: Metrics Query-Range
// -------------------------------------------------------------------------

func TestMock_MetricsQueryRange_JSON(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"up"},"values":[[1700000000,"1"],[1700000060,"1"]]}]}}`
	var capturedQuery, capturedPath, capturedInstanceHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		capturedPath = r.URL.Path
		capturedInstanceHeader = r.Header.Get("OODLE-INSTANCE")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query-range",
		"--query", "up",
		"--start", "1700000000",
		"--end", "1700003600",
		"--step", "60s",
		"--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "matrix") {
		t.Errorf("expected 'matrix' in output, got: %s", stdout)
	}
	if capturedPath != "/api/v1/query_range" {
		t.Errorf("expected path /api/v1/query_range, got: %s", capturedPath)
	}
	if !strings.Contains(capturedQuery, "query=up") {
		t.Errorf("expected query=up in query string %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "start=17") {
		t.Errorf("expected start param in query string %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "end=17") {
		t.Errorf("expected end param in query string %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "step=60s") {
		t.Errorf("expected step=60s in query string %q", capturedQuery)
	}
	if capturedInstanceHeader != "test-instance" {
		t.Errorf("expected OODLE-INSTANCE header 'test-instance', got: %q", capturedInstanceHeader)
	}
}

func TestMock_MetricsQueryRange_WithPartialResponse(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query-range",
		"--query", "up",
		"--start", "1700000000",
		"--end", "1700003600",
		"--step", "60s",
		"--partial-response",
		"--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(capturedQuery, "partial_response=true") {
		t.Errorf("expected partial_response=true in query string %q", capturedQuery)
	}
}

func TestMock_MetricsQueryRange_MissingFlags(t *testing.T) {
	srv := jsonSrv(`{}`, 200)
	defer srv.Close()

	// Missing --query
	_, stderr, code := runMock(t, srv.URL, "metrics", "query-range",
		"--start", "1700000000", "--end", "1700003600", "--step", "60s", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit when --query is missing")
	}
	if !strings.Contains(stderr, "query") {
		t.Errorf("expected 'query' in error, got: %s", stderr)
	}

	// Missing --start
	_, stderr, code = runMock(t, srv.URL, "metrics", "query-range",
		"--query", "up", "--end", "1700003600", "--step", "60s", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit when --start is missing")
	}
	if !strings.Contains(stderr, "start") {
		t.Errorf("expected 'start' in error, got: %s", stderr)
	}

	// Missing --step
	_, stderr, code = runMock(t, srv.URL, "metrics", "query-range",
		"--query", "up", "--start", "1700000000", "--end", "1700003600", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit when --step is missing")
	}
	if !strings.Contains(stderr, "step") {
		t.Errorf("expected 'step' in error, got: %s", stderr)
	}
}

func TestMock_MetricsQueryRange_400Error(t *testing.T) {
	payload := `{"status":"error","errorType":"bad_data","error":"invalid expression"}`
	srv := jsonSrv(payload, 400)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "metrics", "query-range",
		"--query", "invalid{",
		"--start", "1700000000",
		"--end", "1700003600",
		"--step", "60s",
		"--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit on 400")
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "invalid") && !strings.Contains(combined, "error") && !strings.Contains(combined, "400") {
		t.Errorf("expected error message, got: %s", stderr)
	}
}

// -------------------------------------------------------------------------
// Mock: Logs Query
// -------------------------------------------------------------------------

func TestMock_LogsQuery_JSON(t *testing.T) {
	payload := `{"responses":[{"status":200,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_source":{"message":"hello"}}]},"timed_out":false,"took":5}],"took":10}`
	var capturedPath, capturedContentType, capturedInstanceHeader, capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedContentType = r.Header.Get("Content-Type")
		capturedInstanceHeader = r.Header.Get("X-OODLE-INSTANCE")
		bodyBytes, _ := io.ReadAll(r.Body)
		capturedBody = string(bodyBytes)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "logs", "query", "-f", "testdata/logs_query.ndjson", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "hello") {
		t.Errorf("expected 'hello' in output, got: %s", stdout)
	}
	if capturedPath != "/api/v1/query_logs" {
		t.Errorf("expected path /api/v1/query_logs, got: %s", capturedPath)
	}
	if capturedContentType != "application/x-ndjson" {
		t.Errorf("expected Content-Type application/x-ndjson, got: %s", capturedContentType)
	}
	if capturedInstanceHeader != "test-instance" {
		t.Errorf("expected X-OODLE-INSTANCE header 'test-instance', got: %q", capturedInstanceHeader)
	}
	if !strings.Contains(capturedBody, "logs-*") {
		t.Errorf("expected body to contain 'logs-*', got: %s", capturedBody)
	}
}

func TestMock_LogsQuery_MissingFile(t *testing.T) {
	srv := jsonSrv(`{}`, 200)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "logs", "query", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit when -f is missing")
	}
	if !strings.Contains(stderr, "file") {
		t.Errorf("expected 'file' in error, got: %s", stderr)
	}
}

func TestMock_LogsQuery_NonexistentFile(t *testing.T) {
	srv := jsonSrv(`{}`, 200)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "logs", "query", "-f", "/nonexistent/file.ndjson", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit for nonexistent file")
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "no such file") && !strings.Contains(combined, "reading") {
		t.Errorf("expected file error, got: %s", stderr)
	}
}

func TestMock_LogsQuery_400Error(t *testing.T) {
	srv := jsonSrv(`{"message":"invalid request body"}`, 400)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "logs", "query", "-f", "testdata/logs_query.ndjson", "--output", "json")
	if code == 0 {
		t.Fatalf("expected non-zero exit on 400\nstderr: %s", stderr)
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "invalid") && !strings.Contains(combined, "error") && !strings.Contains(combined, "400") {
		t.Errorf("expected error message, got: %s", stderr)
	}
}

func TestMock_LogsQuery_401Error(t *testing.T) {
	srv := jsonSrv(`{"message":"unauthorized"}`, 401)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "logs", "query", "-f", "testdata/logs_query.ndjson", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit on 401")
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "auth") && !strings.Contains(combined, "api key") {
		t.Errorf("expected auth-related error, got: %s", stderr)
	}
}

// -------------------------------------------------------------------------
// Mock: Logs Index Patterns
// -------------------------------------------------------------------------

func TestMock_LogsIndexPatterns_JSON(t *testing.T) {
	payload := `[{"id":"abc-123","title":"logs-*","fields":[{"name":"message","type":"string"},{"name":"timestamp","type":"date"}]},{"id":"def-456","title":"app-logs","fields":[{"name":"level","type":"string"}]}]`
	var capturedPath, capturedInstanceHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedInstanceHeader = r.Header.Get("X-OODLE-INSTANCE")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "logs", "index-patterns", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSONMock(t, stdout)
	if !strings.Contains(stdout, "logs-*") {
		t.Errorf("expected 'logs-*' in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "app-logs") {
		t.Errorf("expected 'app-logs' in output, got: %s", stdout)
	}
	if capturedPath != "/api/v1/log_index_patterns" {
		t.Errorf("expected path /api/v1/log_index_patterns, got: %s", capturedPath)
	}
	if capturedInstanceHeader != "test-instance" {
		t.Errorf("expected X-OODLE-INSTANCE header 'test-instance', got: %q", capturedInstanceHeader)
	}
}

func TestMock_LogsIndexPatterns_400Error(t *testing.T) {
	srv := jsonSrv(`{"message":"bad request"}`, 400)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "logs", "index-patterns", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit on 400")
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "bad request") && !strings.Contains(combined, "error") && !strings.Contains(combined, "400") {
		t.Errorf("expected error message, got: %s", stderr)
	}
}

func TestMock_LogsIndexPatterns_401Error(t *testing.T) {
	srv := jsonSrv(`{"message":"unauthorized"}`, 401)
	defer srv.Close()

	_, stderr, code := runMock(t, srv.URL, "logs", "index-patterns", "--output", "json")
	if code == 0 {
		t.Fatal("expected non-zero exit on 401")
	}
	combined := strings.ToLower(stderr)
	if !strings.Contains(combined, "auth") && !strings.Contains(combined, "api key") {
		t.Errorf("expected auth-related error, got: %s", stderr)
	}
}

// -------------------------------------------------------------------------
// Mock: Metrics Query — table and CSV output formats
// -------------------------------------------------------------------------

func TestMock_MetricsQuery_TableVector(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up","job":"prometheus","instance":"localhost:9090"},"value":[1700000000,"1"]},{"metric":{"__name__":"up","job":"node","instance":"localhost:9100"},"value":[1700000000,"0"]}]}}`
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "up", "--output", "table")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	// Must have table headers, not raw JSON.
	if !strings.Contains(stdout, "METRIC") {
		t.Errorf("expected METRIC header in table output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "TIMESTAMP") {
		t.Errorf("expected TIMESTAMP header in table output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "VALUE") {
		t.Errorf("expected VALUE header in table output, got: %s", stdout)
	}
	// Metric name should appear as prefix with labels.
	if !strings.Contains(stdout, "up{") {
		t.Errorf("expected metric name prefix 'up{' in table output, got: %s", stdout)
	}
	// Both series should appear.
	if !strings.Contains(stdout, `job="prometheus"`) {
		t.Errorf("expected job=prometheus label in table output, got: %s", stdout)
	}
	if !strings.Contains(stdout, `job="node"`) {
		t.Errorf("expected job=node label in table output, got: %s", stdout)
	}
	// Must NOT be raw JSON.
	if strings.Contains(stdout, `"resultType"`) {
		t.Errorf("table output must not contain raw JSON keys, got: %s", stdout)
	}
}

func TestMock_MetricsQuery_TableScalar(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"scalar","result":[1700000000,"42"]}}`
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "1+1", "--output", "table")
	if code != 0 {
		t.Fatalf("expected exit 0 for scalar table, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "TIMESTAMP") {
		t.Errorf("expected TIMESTAMP header, got: %s", stdout)
	}
	if !strings.Contains(stdout, "VALUE") {
		t.Errorf("expected VALUE header, got: %s", stdout)
	}
	if !strings.Contains(stdout, "42") {
		t.Errorf("expected value '42' in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "2023-11-14") {
		t.Errorf("expected formatted date '2023-11-14' in output, got: %s", stdout)
	}
	if strings.Contains(stdout, `"resultType"`) {
		t.Errorf("table output must not contain raw JSON keys, got: %s", stdout)
	}
}

func TestMock_MetricsQuery_TableEmptyVector(t *testing.T) {
	// An empty result set should print the header row but no data rows —
	// not an error and not a blank screen.
	payload := `{"status":"success","data":{"resultType":"vector","result":[]}}`
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "nonexistent_metric", "--output", "table")
	if code != 0 {
		t.Fatalf("expected exit 0 for empty vector, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "METRIC") {
		t.Errorf("expected METRIC header even for empty result, got: %s", stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line (header only) for empty result, got %d lines:\n%s", len(lines), stdout)
	}
}

func TestMock_MetricsQuery_CSVVector(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up","job":"test"},"value":[1700000000,"1"]}]}}`
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "up", "--output", "csv")
	if code != 0 {
		t.Fatalf("expected exit 0 for CSV, got %d\nstderr: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 CSV lines (header + 1 data row), got %d:\n%s", len(lines), stdout)
	}
	// Header line must contain METRIC.
	if !strings.Contains(lines[0], "METRIC") {
		t.Errorf("expected METRIC in CSV header, got: %s", lines[0])
	}
	// Both lines must contain comma separators.
	if !strings.Contains(lines[0], ",") {
		t.Errorf("expected comma in CSV header, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], ",") {
		t.Errorf("expected comma in CSV data row, got: %s", lines[1])
	}
	// Data row must contain value "1".
	if !strings.Contains(lines[1], "1") {
		t.Errorf("expected value '1' in CSV data row, got: %s", lines[1])
	}
}

func TestMock_MetricsQuery_YAMLBypassesPromQL(t *testing.T) {
	// YAML format must NOT trigger the PromQL table formatter — it should
	// produce valid YAML with no table headers.
	payload := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"test"},"value":[1700000000,"1"]}]}}`
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "up", "--output", "yaml")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if strings.Contains(stdout, "METRIC") {
		t.Errorf("YAML output must not contain table headers, got: %s", stdout)
	}
	// YAML output must contain the resultType key.
	if !strings.Contains(stdout, "resultType") {
		t.Errorf("expected 'resultType' in YAML output, got: %s", stdout)
	}
}

func TestMock_MetricsQuery_TableNonPromFallsBackToJSON(t *testing.T) {
	// If the API returns a non-Prometheus shaped response, --output table
	// must NOT produce empty output. It must fall back to JSON
	// (output.Print with FormatTable and no columns falls back to JSON).
	payload := `{"items":[{"name":"foo"}]}`
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query", "--query", "up", "--output", "table")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected non-empty output for non-Prom table fallback, got empty string")
	}
	// Should contain JSON-like content from the fallback path.
	if !strings.Contains(stdout, "items") {
		t.Errorf("expected 'items' in fallback JSON output, got: %s", stdout)
	}
}

// -------------------------------------------------------------------------
// Mock: Metrics Query-Range — table and CSV output formats
// -------------------------------------------------------------------------

func TestMock_MetricsQueryRange_TableMatrix(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"http_requests_total","method":"GET"},"values":[[1700000000,"100"],[1700000060,"105"],[1700000120,"110"]]},{"metric":{"__name__":"http_requests_total","method":"POST"},"values":[[1700000000,"50"],[1700000060,"52"]]}]}}`
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query-range",
		"--query", "http_requests_total",
		"--start", "1700000000",
		"--end", "1700000180",
		"--step", "60s",
		"--output", "table")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "METRIC") {
		t.Errorf("expected METRIC header, got: %s", stdout)
	}
	if !strings.Contains(stdout, "VALUES") {
		t.Errorf("expected VALUES header, got: %s", stdout)
	}
	// Both series must appear.
	if !strings.Contains(stdout, `method="GET"`) {
		t.Errorf("expected method=GET label, got: %s", stdout)
	}
	if !strings.Contains(stdout, `method="POST"`) {
		t.Errorf("expected method=POST label, got: %s", stdout)
	}
	// Compact time format: value@MonthDay.
	if !strings.Contains(stdout, "100@Nov14") {
		t.Errorf("expected '100@Nov14' compact format, got: %s", stdout)
	}
	if strings.Contains(stdout, `"resultType"`) {
		t.Errorf("table output must not contain raw JSON keys, got: %s", stdout)
	}
}

func TestMock_MetricsQueryRange_CSVMatrix(t *testing.T) {
	// Matrix CSV uses formatMatrixCSV which emits one row PER SAMPLE (not per
	// series) so that output is lossless — no truncation. Two samples in the
	// payload → header + 2 data rows = 3 lines total.
	// Columns are METRIC, TIMESTAMP, VALUE (same as vector CSV — no VALUES summary).
	payload := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"method":"GET"},"values":[[1700000000,"100"],[1700000060,"105"]]}]}}`
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query-range",
		"--query", "http_requests_total",
		"--start", "1700000000",
		"--end", "1700000120",
		"--step", "60s",
		"--output", "csv")
	if code != 0 {
		t.Fatalf("expected exit 0 for CSV matrix, got %d\nstderr: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// 1 header + 2 samples = 3 lines (one row per sample, not per series)
	if len(lines) != 3 {
		t.Fatalf("expected 3 CSV lines (header + 2 sample rows), got %d:\n%s", len(lines), stdout)
	}
	// Header uses METRIC, TIMESTAMP, VALUE (per-sample columns, not VALUES summary)
	if !strings.Contains(lines[0], "METRIC") {
		t.Errorf("expected METRIC in CSV header, got: %s", lines[0])
	}
	if !strings.Contains(lines[0], "TIMESTAMP") {
		t.Errorf("expected TIMESTAMP in CSV header, got: %s", lines[0])
	}
	if !strings.Contains(lines[0], "VALUE") {
		t.Errorf("expected VALUE in CSV header, got: %s", lines[0])
	}
	// Each data row must contain commas (multiple columns) and the sample values.
	if !strings.Contains(lines[1], ",") {
		t.Errorf("expected comma in CSV data row 1, got: %s", lines[1])
	}
	if !strings.Contains(stdout, "100") {
		t.Errorf("expected sample value '100' in CSV output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "105") {
		t.Errorf("expected sample value '105' in CSV output, got: %s", stdout)
	}
	// Both rows must reference the same metric label
	if strings.Count(stdout, `method=`) < 2 {
		t.Errorf("expected metric label to appear in each data row, got: %s", stdout)
	}
}

func TestMock_MetricsQueryRange_TableMatrixTruncation(t *testing.T) {
	// Build a matrix payload with 20 samples — should show truncation indicator.
	values := make([]string, 20)
	for i := range values {
		values[i] = fmt.Sprintf("[%d,\"1\"]", 1700000000+i*60)
	}
	payload := fmt.Sprintf(
		`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"job":"test"},"values":[%s]}]}}`,
		strings.Join(values, ","),
	)
	srv := jsonSrv(payload, 200)
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query-range",
		"--query", "up",
		"--start", "1700000000",
		"--end", fmt.Sprintf("%d", 1700000000+19*60),
		"--step", "60s",
		"--output", "table")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "... (20 total)") {
		t.Errorf("expected truncation indicator '... (20 total)', got: %s", stdout)
	}
}
