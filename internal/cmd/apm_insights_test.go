package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	apmSearchPath = "/v1/api/instance/inst/apm/insights/search"
	apmDetailPath = "/v1/api/instance/inst/apm/insights/detail"
)

const apmSearchBody = `{"insights":[` +
	`{"id":"aaa111","pattern":"n_plus_one_query","service":"checkout","env":"prod","resource":"OrderRepo.load",` +
	`"entryResource":"GET /orders","downstreamService":"postgres","downstreamResource":"SELECT * FROM items WHERE id = ?",` +
	`"priority":"high","score":0.9,"detections":120,"estimatedRequests":4500.4,"timeShare":0.42,"newField":1},` +
	`{"id":"bbb222","pattern":"sequential_calls","service":"cart","env":"prod","resource":"Cart.sync",` +
	`"entryResource":"POST /cart","downstreamService":"pricing","downstreamResource":"GET /price",` +
	`"priority":"low","score":0.1,"detections":3,"estimatedRequests":3,"timeShare":0.05}],` +
	`"countsByPattern":{"n_plus_one_query":1,"sequential_calls":1}}`

const apmDetailBody = `{"stepSec":3600,"detections":[{"ts":1700000000000,"value":5},{"ts":1700003600000,"value":null}],` +
	`"avgRunMs":[],"affectedLatencyMs":[],"unaffectedLatencyMs":[]}`

func TestAPMInsights_RequestAndTable(t *testing.T) {
	srv := newRoutedServer(t, map[string]string{apmSearchPath: apmSearchBody}, nil)
	stdout, stderr, err := runCmdSplit(t, srv.URL, newTracesAPMInsightsCmd(), output.FormatTable,
		"--service", "checkout,cart", "--env", "prod", "--pattern", "n_plus_one_query",
		"--start", "1700000000", "--end", "1700086400000", "--limit", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	reqs := srv.find(apmSearchPath)
	if len(reqs) != 1 || reqs[0].method != "POST" {
		t.Fatalf("requests = %+v", reqs)
	}
	var got apmInsightsSearchRequest
	if err := json.Unmarshal([]byte(reqs[0].body), &got); err != nil {
		t.Fatal(err)
	}
	if got.EndTimeEpochMs != 1700086400000 || got.TrendLookbackMs != 86400000 {
		t.Errorf("window = %+v", got)
	}
	if strings.Join(got.Services, ",") != "checkout,cart" || strings.Join(got.Envs, ",") != "prod" ||
		strings.Join(got.Patterns, ",") != "n_plus_one_query" {
		t.Errorf("filters = %+v", got)
	}
	for _, want := range []string{"PRIORITY", "high", "checkout", "GET /orders", "postgres: SELECT", "4500", "42.0%", "aaa111"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table does not contain %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "bbb222") {
		t.Errorf("--limit is not applied:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Showing 1 of 2") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestAPMInsights_DefaultsAndJSON(t *testing.T) {
	srv := newRoutedServer(t, map[string]string{apmSearchPath: apmSearchBody}, nil)
	stdout, _, err := runCmdSplit(t, srv.URL, newTracesAPMInsightsCmd(), output.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got apmInsightsSearchRequest
	_ = json.Unmarshal([]byte(srv.find(apmSearchPath)[0].body), &got)
	if got.TrendLookbackMs < 86399000 || got.TrendLookbackMs > 86401000 {
		t.Errorf("default lookback = %d", got.TrendLookbackMs)
	}
	// Empty filters are sent as [] and not null.
	if !strings.Contains(srv.find(apmSearchPath)[0].body, `"services":[]`) {
		t.Errorf("body = %s", srv.find(apmSearchPath)[0].body)
	}
	var out struct {
		Total    int              `json:"total"`
		Returned int              `json:"returned"`
		Insights []map[string]any `json:"insights"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if out.Total != 2 || out.Returned != 2 || out.Insights[0]["newField"] == nil {
		t.Errorf("output = %s", stdout)
	}
}

func TestAPMInsights_Detail(t *testing.T) {
	srv := newRoutedServer(t, map[string]string{apmSearchPath: apmSearchBody, apmDetailPath: apmDetailBody}, nil)
	stdout, _, err := runCmdSplit(t, srv.URL, newTracesAPMInsightsCmd(), output.FormatTable, "--id", "aaa111")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	reqs := srv.find(apmDetailPath)
	if len(reqs) != 1 {
		t.Fatalf("detail requests = %d", len(reqs))
	}
	var got apmInsightDetailRequest
	_ = json.Unmarshal([]byte(reqs[0].body), &got)
	if got.Key.Service != "checkout" || got.Key.EntryResource != "GET /orders" || got.Key.Pattern != "n_plus_one_query" {
		t.Errorf("detail key = %+v", got.Key)
	}
	for _, want := range []string{
		"aaa111", "Next step: Batch the calls",
		`oodle traces traceql search '{ resource.service.name="checkout" && span.oodle.apm.pattern="n_plus_one_query"`,
		"Detections per 1h0m0s", "2023-11-14 22:13", "5",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output does not contain %q:\n%s", want, stdout)
		}
	}

	stdout, _, err = runCmdSplit(t, srv.URL, newTracesAPMInsightsCmd(), output.FormatJSON, "--id", "aaa111")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	for _, k := range []string{"insight", "trend", "nextStep", "sampleTracesTraceQL"} {
		if _, ok := out[k]; !ok {
			t.Errorf("JSON output has no %q", k)
		}
	}
}

func TestAPMInsights_Errors(t *testing.T) {
	srv := newRoutedServer(t, map[string]string{apmSearchPath: apmSearchBody}, nil)
	if _, _, err := runCmdSplit(t, srv.URL, newTracesAPMInsightsCmd(), output.FormatTable, "--id", "zzz"); err == nil ||
		!strings.Contains(err.Error(), "no insight with ID zzz") {
		t.Errorf("unknown ID: %v", err)
	}
	n := len(srv.requests)
	for _, args := range [][]string{{"--pattern", "slow"}, {"--start", "-8d"}, {"--limit", "0"}} {
		if _, _, err := runCmdSplit(t, srv.URL, newTracesAPMInsightsCmd(), output.FormatTable, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	if len(srv.requests) != n {
		t.Errorf("request was sent for bad input")
	}
}
