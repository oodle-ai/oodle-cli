package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const anomaliesBody = `{"anomalies":[{"title":"Inbound communication errors","type":1,"category":2,` +
	`"description":"High error rates for calls to db","promqlExpression":"up",` +
	`"anomaly_series":[{"metric":{"server":"db"},"occurrence":"recurring"},` +
	`{"metric":{"server":"cache"},"occurrence":"new"}]}]}`

const anomaliesPath = "/v1/api/instance/inst/jarvis/anomalies"

func TestAnomaliesList_RequestAndTable(t *testing.T) {
	srv := newRoutedServer(t, map[string]string{anomaliesPath: anomaliesBody}, nil)
	stdout, stderr, err := runCmdSplit(t, srv.URL, newAnomaliesListCmd(), output.FormatTable,
		"--time", "1700000000000", "--namespace", "prod", "--service", "api", "--compare", "24h,7d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	reqs := srv.find(anomaliesPath)
	if len(reqs) != 1 || reqs[0].method != "POST" {
		t.Fatalf("requests = %+v", reqs)
	}
	if reqs[0].apiKey != "test-key" {
		t.Errorf("X-API-Key = %q", reqs[0].apiKey)
	}
	var got anomaliesRequest
	if err := json.Unmarshal([]byte(reqs[0].body), &got); err != nil {
		t.Fatal(err)
	}
	// The time was given in milliseconds; the window is one hour on each
	// side, in seconds.
	if got.StartTimeEpochSec != 1699996400 || got.EndTimeEpochSec != 1700003600 {
		t.Errorf("window = %d..%d", got.StartTimeEpochSec, got.EndTimeEpochSec)
	}
	if got.Namespace != "prod" || got.Service != "api" || got.Instance != "inst" {
		t.Errorf("filters = %+v", got)
	}
	if len(got.CompareWithOffsetSec) != 2 || got.CompareWithOffsetSec[0] != 86400 || got.CompareWithOffsetSec[1] != 604800 {
		t.Errorf("offsets = %v", got.CompareWithOffsetSec)
	}
	for _, want := range []string{"Inbound communication errors", "application", "large_change", "High error rates"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table does not contain %q:\n%s", want, stdout)
		}
	}
	// Two series, one of them new.
	if !strings.Contains(stdout, "2       1") {
		t.Errorf("series counts are wrong:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %q", stderr)
	}
}

func TestAnomaliesList_DefaultsAndJSONPassThrough(t *testing.T) {
	srv := newRoutedServer(t, map[string]string{anomaliesPath: anomaliesBody}, nil)
	stdout, _, err := runCmdSplit(t, srv.URL, newAnomaliesListCmd(), output.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(stdout) != anomaliesBody {
		t.Errorf("JSON output is not the response body:\n%s", stdout)
	}
	var got anomaliesRequest
	_ = json.Unmarshal([]byte(srv.find(anomaliesPath)[0].body), &got)
	now := time.Now().Unix()
	if got.EndTimeEpochSec-got.StartTimeEpochSec != 7200 || got.EndTimeEpochSec < now+3590 || got.EndTimeEpochSec > now+3610 {
		t.Errorf("default window = %d..%d, now %d", got.StartTimeEpochSec, got.EndTimeEpochSec, now)
	}
	if len(got.CompareWithOffsetSec) != 1 || got.CompareWithOffsetSec[0] != 86400 {
		t.Errorf("default offsets = %v", got.CompareWithOffsetSec)
	}
}

func TestAnomaliesList_BadCompare(t *testing.T) {
	srv := newRoutedServer(t, nil, nil)
	_, _, err := runCmdSplit(t, srv.URL, newAnomaliesListCmd(), output.FormatTable, "--compare", "-1h")
	if err == nil || !strings.Contains(err.Error(), "--compare") {
		t.Fatalf("expected a --compare error, got %v", err)
	}
	if len(srv.requests) != 0 {
		t.Errorf("request was sent for bad input")
	}
}

func TestAnomaliesList_APIError(t *testing.T) {
	srv := newRoutedServer(t, nil, nil)
	_, _, err := runCmdSplit(t, srv.URL, newAnomaliesListCmd(), output.FormatTable)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected the server message, got %v", err)
	}
}
