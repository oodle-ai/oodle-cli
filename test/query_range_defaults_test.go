//go:build integration

package test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMock_MetricsQueryRange_AllDefaultsOmitted verifies that omitting --start,
// --end, and --step all together causes the CLI to apply defaults and issue a
// valid request (exit 0, correct query-string params).
func TestMock_MetricsQueryRange_AllDefaultsOmitted(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"up"},"values":[[1700000000,"1"]]}]}}`
	var capturedQuery string
	beforeCall := time.Now().Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	stdout, stderr, code := runMock(t, srv.URL, "metrics", "query-range",
		"--query", "up",
		"--output", "json")
	// Should exit 0 — all three flags are optional now.
	if code != 0 {
		t.Fatalf("expected exit 0 with all defaults omitted, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "matrix") {
		t.Errorf("expected 'matrix' in output, got: %s", stdout)
	}

	// start= must be present and look like a Unix epoch ~1h in the past.
	if !strings.Contains(capturedQuery, "start=") {
		t.Errorf("expected start= in query string, got: %s", capturedQuery)
	}
	// end= must be present and close to now.
	if !strings.Contains(capturedQuery, "end=") {
		t.Errorf("expected end= in query string, got: %s", capturedQuery)
	}
	// step must be the default 60s.
	if !strings.Contains(capturedQuery, "step=60s") {
		t.Errorf("expected step=60s in query string, got: %s", capturedQuery)
	}

	// Parse the actual start and end values and sanity-check the range.
	var startVal, endVal float64
	for _, kv := range strings.Split(capturedQuery, "&") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		var f float64
		if _, err := fmt.Sscanf(parts[1], "%f", &f); err != nil {
			continue
		}
		switch parts[0] {
		case "start":
			startVal = f
		case "end":
			endVal = f
		}
	}
	afterCall := time.Now().Unix()
	// end should be within [beforeCall, afterCall] (i.e. "now").
	if float64(beforeCall) > endVal || endVal > float64(afterCall+5) {
		t.Errorf("end value %v not close to now (window: %d–%d)", endVal, beforeCall, afterCall)
	}
	// start should be roughly 1h (3600s) before end.
	expectedStart := endVal - 3600
	if startVal < expectedStart-10 || startVal > expectedStart+10 {
		t.Errorf("start value %v not ~1h before end %v", startVal, endVal)
	}
}

// TestMock_MetricsQueryRange_SingleDefaultStart verifies that omitting only
// --start causes start= to be filled with a value ~1h ago while the explicit
// --end and --step values are transmitted unchanged.
func TestMock_MetricsQueryRange_SingleDefaultStart(t *testing.T) {
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
		"--end", "1700003600",
		"--step", "5m",
		"--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 when --start is omitted, got %d\nstderr: %s", code, stderr)
	}
	_ = stdout
	if !strings.Contains(capturedQuery, "start=") {
		t.Errorf("expected start= in query string when defaulted, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "end=1.7000036e") && !strings.Contains(capturedQuery, "end=1700003600") {
		t.Errorf("expected explicit end=1700003600 in query string, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "step=5m") {
		t.Errorf("expected explicit step=5m in query string, got: %s", capturedQuery)
	}
}

// TestMock_MetricsQueryRange_SingleDefaultEnd verifies that omitting only
// --end causes end= to be filled with "now" while --start and --step are
// transmitted as supplied.
func TestMock_MetricsQueryRange_SingleDefaultEnd(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	var capturedQuery string
	beforeCall := time.Now().Unix()
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
		"--step", "5m",
		"--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 when --end is omitted, got %d\nstderr: %s", code, stderr)
	}
	_ = stdout
	if !strings.Contains(capturedQuery, "end=") {
		t.Errorf("expected end= in query string when defaulted, got: %s", capturedQuery)
	}
	afterCall := time.Now().Unix()
	// end value should be close to now.
	var endVal float64
	for _, kv := range strings.Split(capturedQuery, "&") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && parts[0] == "end" {
			fmt.Sscanf(parts[1], "%f", &endVal)
		}
	}
	if float64(beforeCall) > endVal || endVal > float64(afterCall+5) {
		t.Errorf("end value %v not close to now (window: %d–%d)", endVal, beforeCall, afterCall)
	}
}

// TestMock_MetricsQueryRange_SingleDefaultStep verifies that omitting only
// --step causes step=60s to be used while --start and --end are transmitted
// as supplied.
func TestMock_MetricsQueryRange_SingleDefaultStep(t *testing.T) {
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
		"--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 when --step is omitted, got %d\nstderr: %s", code, stderr)
	}
	_ = stdout
	if !strings.Contains(capturedQuery, "step=60s") {
		t.Errorf("expected step=60s (default) in query string, got: %s", capturedQuery)
	}
}

// TestMetricsQueryRange_NoFlags verifies end-to-end that running query-range
// with only --query (no --start, --end, --step) against the real Oodle API
// returns a valid JSON response (exit 0).
func TestMetricsQueryRange_NoFlags(t *testing.T) {
	stdout, stderr, code := runOodle(t, "metrics", "query-range",
		"--query", "count(up)",
		"--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0 with all defaults omitted against live API, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "status") {
		t.Errorf("expected JSON with 'status' field in response, got: %s", stdout)
	}
}
