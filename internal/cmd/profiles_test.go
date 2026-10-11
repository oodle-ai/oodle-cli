package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// profilesRequest is the request that the profiles test server got.
type profilesRequest struct {
	method      string
	path        string
	contentType string
	instance    string
	apiKey      string
	body        map[string]any
}

// newProfilesTestServer returns a server that records the request and
// replies with status and body.
func newProfilesTestServer(t *testing.T, status int, body string) (*httptest.Server, *profilesRequest) {
	t.Helper()
	got := &profilesRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.contentType = r.Header.Get("Content-Type")
		got.instance = r.Header.Get("OODLE-INSTANCE")
		got.apiKey = r.Header.Get("X-API-Key")
		data, _ := io.ReadAll(r.Body)
		got.body = nil
		if err := json.Unmarshal(data, &got.body); err != nil {
			t.Errorf("request body is not a JSON object: %q", data)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// checkProfilesRequest checks the parts of the request that every profiles
// command sends in the same way.
func checkProfilesRequest(t *testing.T, got *profilesRequest, method string) {
	t.Helper()
	if got.method != http.MethodPost {
		t.Errorf("HTTP method = %q, want POST", got.method)
	}
	if want := "/querier.v1.QuerierService/" + method; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.contentType)
	}
	if got.instance != "inst" {
		t.Errorf("OODLE-INSTANCE = %q, want inst", got.instance)
	}
	if got.apiKey != "test-key" {
		t.Errorf("X-API-Key = %q, want test-key", got.apiKey)
	}
}

// checkBody checks that the request body is equal to want after a JSON
// round trip, so that numbers compare as float64.
func checkBody(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	data, _ := json.Marshal(want)
	var norm map[string]any
	_ = json.Unmarshal(data, &norm)
	if !reflect.DeepEqual(got, norm) {
		t.Errorf("body = %v, want %v", got, norm)
	}
}

// fixed range for the tests: 1700000000 s to 1700003600 s.
var profilesRangeArgs = []string{"--start", "1700000000", "--end", "1700003600"}

const (
	profilesStartMs = int64(1700000000000)
	profilesEndMs   = int64(1700003600000)
	cpuTypeID       = "process_cpu:cpu:nanoseconds:cpu:nanoseconds"
)

func TestProfilesTypes(t *testing.T) {
	body := `{"profile_types":[{"ID":"` + cpuTypeID + `","name":"process_cpu","sample_type":"cpu",` +
		`"sample_unit":"nanoseconds","period_type":"cpu","period_unit":"nanoseconds"}]}`
	srv, got := newProfilesTestServer(t, 200, body)

	out, _, err := runCmdSplit(t, srv.URL, newProfilesTypesCmd(), output.FormatTable, profilesRangeArgs...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkProfilesRequest(t, got, "ProfileTypes")
	checkBody(t, got.body, map[string]any{"start": profilesStartMs, "end": profilesEndMs})
	for _, want := range []string{"ID", "SAMPLE UNIT", cpuTypeID, "process_cpu", "nanoseconds"} {
		if !strings.Contains(out, want) {
			t.Errorf("table does not contain %q:\n%s", want, out)
		}
	}

	out, _, err = runCmdSplit(t, srv.URL, newProfilesTypesCmd(), output.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var types []map[string]string
	if err := json.Unmarshal([]byte(out), &types); err != nil {
		t.Fatalf("JSON output does not parse: %v\n%s", err, out)
	}
	want := []map[string]string{{
		"id": cpuTypeID, "name": "process_cpu", "sample_type": "cpu",
		"sample_unit": "nanoseconds", "period_type": "cpu", "period_unit": "nanoseconds",
	}}
	if !reflect.DeepEqual(types, want) {
		t.Errorf("JSON = %v, want %v", types, want)
	}
}

func TestProfilesTypes_DefaultRangeIsLastHour(t *testing.T) {
	srv, got := newProfilesTestServer(t, 200, `{}`)
	if _, _, err := runCmdSplit(t, srv.URL, newProfilesTypesCmd(), output.FormatJSON); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	start, _ := got.body["start"].(float64)
	end, _ := got.body["end"].(float64)
	if d := end - start; d < 3599000 || d > 3601000 {
		t.Errorf("default range = %v ms, want one hour", d)
	}
}

func TestProfilesLabels(t *testing.T) {
	srv, got := newProfilesTestServer(t, 200, `{"names":["pod","service_name"]}`)
	args := append([]string{"--type", cpuTypeID}, profilesRangeArgs...)
	out, _, err := runCmdSplit(t, srv.URL, newProfilesLabelsCmd(), output.FormatTable, args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkProfilesRequest(t, got, "LabelNames")
	checkBody(t, got.body, map[string]any{
		"matchers": []string{`{__profile_type__="` + cpuTypeID + `"}`},
		"start":    profilesStartMs,
		"end":      profilesEndMs,
	})
	if !strings.Contains(out, "Label") || !strings.Contains(out, "service_name") {
		t.Errorf("unexpected table:\n%s", out)
	}

	// Without --type the matcher list is empty, which reads all types.
	out, _, err = runCmdSplit(t, srv.URL, newProfilesLabelsCmd(), output.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m, ok := got.body["matchers"].([]any); !ok || len(m) != 0 {
		t.Errorf("matchers = %v, want an empty list", got.body["matchers"])
	}
	var names []string
	if err := json.Unmarshal([]byte(out), &names); err != nil || len(names) != 2 {
		t.Errorf("JSON = %q, want a list of two names", out)
	}
}

func TestProfilesLabelValues(t *testing.T) {
	srv, got := newProfilesTestServer(t, 200, `{"names":["api","worker"]}`)
	args := append([]string{"service_name", "--type", cpuTypeID}, profilesRangeArgs...)
	out, _, err := runCmdSplit(t, srv.URL, newProfilesLabelValuesCmd(), output.FormatJSON, args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkProfilesRequest(t, got, "LabelValues")
	checkBody(t, got.body, map[string]any{
		"name":     "service_name",
		"matchers": []string{`{__profile_type__="` + cpuTypeID + `"}`},
		"start":    profilesStartMs,
		"end":      profilesEndMs,
	})
	var values []string
	if err := json.Unmarshal([]byte(out), &values); err != nil || !reflect.DeepEqual(values, []string{"api", "worker"}) {
		t.Errorf("JSON = %q, want [api worker]", out)
	}

	cmd := newProfilesLabelValuesCmd()
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("label-values without a label name did not fail")
	}
}

const profilesSeriesBody = `{"series":[
  {"labels":[{"name":"service_name","value":"api"}],
   "points":[{"value":2,"timestamp":1700000000000},{"value":3,"timestamp":1700000015000}]},
  {"labels":[{"name":"service_name","value":"idle"}]}
]}`

func TestProfilesSeries(t *testing.T) {
	srv, got := newProfilesTestServer(t, 200, profilesSeriesBody)
	args := append([]string{"--type", cpuTypeID, "--query", `{service_name="api"}`,
		"--group-by", "service_name,pod", "--step", "1m"}, profilesRangeArgs...)
	out, _, err := runCmdSplit(t, srv.URL, newProfilesSeriesCmd(), output.FormatJSON, args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkProfilesRequest(t, got, "SelectSeries")
	checkBody(t, got.body, map[string]any{
		"profile_typeID": cpuTypeID,
		"label_selector": `{service_name="api"}`,
		"start":          profilesStartMs,
		"end":            profilesEndMs,
		"step":           60,
		"group_by":       []string{"service_name", "pod"},
	})

	// The series without points is left out, like in the MCP tool.
	var series []profileSeries
	if err := json.Unmarshal([]byte(out), &series); err != nil {
		t.Fatalf("JSON output does not parse: %v\n%s", err, out)
	}
	if len(series) != 1 || series[0].Labels["service_name"] != "api" || series[0].Count != 2 || series[0].Sum != 5 {
		t.Errorf("series = %+v", series)
	}

	out, _, err = runCmdSplit(t, srv.URL, newProfilesSeriesCmd(), output.FormatTable, "--type", cpuTypeID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `{service_name="api"}`) || !strings.Contains(out, "POINTS") {
		t.Errorf("unexpected table:\n%s", out)
	}
	// Without --step, --query and --group-by, the step is the default and
	// the body has an empty selector and no group_by.
	if got.body["step"] != float64(profilesDefaultStep) {
		t.Errorf("default step = %v, want %d", got.body["step"], profilesDefaultStep)
	}
	if got.body["label_selector"] != "" {
		t.Errorf("label_selector = %v, want empty", got.body["label_selector"])
	}
	if _, ok := got.body["group_by"]; ok {
		t.Error("group_by is in the body without --group-by")
	}
}

func TestProfilesSeriesStep(t *testing.T) {
	if got := profilesSeriesStep(3600); got != profilesDefaultStep {
		t.Errorf("step for 1h = %d, want %d", got, profilesDefaultStep)
	}
	if got := profilesSeriesStep(7 * 24 * 3600); got*traceQLMaxPoints < 7*24*3600 {
		t.Errorf("step for 7d = %d gives more than %d points", got, traceQLMaxPoints)
	}
}

// profilesFlamegraphBody has the root node "total" and two functions. main
// calls work. Each node is [offset, total, self, name index].
const profilesFlamegraphBody = `{"flamegraph":{
  "names":["total","main","work"],
  "levels":[{"values":[0,1000,0,0]},{"values":[0,1000,250,1]},{"values":[0,750,750,2]}],
  "total":1000,"max_self":750}}`

func TestProfilesFlamegraph(t *testing.T) {
	srv, got := newProfilesTestServer(t, 200, profilesFlamegraphBody)
	args := append([]string{"--type", cpuTypeID, "--query", `{service_name="api"}`, "--max-nodes", "512"},
		profilesRangeArgs...)
	out, _, err := runCmdSplit(t, srv.URL, newProfilesFlamegraphCmd(), output.FormatJSON, args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkProfilesRequest(t, got, "SelectMergeStacktraces")
	checkBody(t, got.body, map[string]any{
		"profile_typeID": cpuTypeID,
		"label_selector": `{service_name="api"}`,
		"start":          profilesStartMs,
		"end":            profilesEndMs,
		"max_nodes":      512,
	})
	var summary flameGraphSummary
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("JSON output does not parse: %v\n%s", err, out)
	}
	want := flameGraphSummary{Total: 1000, TopFunctions: []profileFunction{
		{Function: "work", Self: 750, SelfPct: 75, Total: 750},
		{Function: "main", Self: 250, SelfPct: 25, Total: 1000},
	}}
	if !reflect.DeepEqual(summary, want) {
		t.Errorf("summary = %+v, want %+v", summary, want)
	}

	// The table shows CPU time as durations.
	out, _, err = runCmdSplit(t, srv.URL, newProfilesFlamegraphCmd(), output.FormatTable, "--type", cpuTypeID, "--top", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "FUNCTION") || !strings.Contains(out, "work") || !strings.Contains(out, "75.00") ||
		!strings.Contains(out, "750ns") || strings.Contains(out, "main") {
		t.Errorf("unexpected table:\n%s", out)
	}
	// Without --query the selector is "{}", and --max-nodes has its default.
	if got.body["label_selector"] != "{}" {
		t.Errorf("label_selector = %v, want {}", got.body["label_selector"])
	}
	if got.body["max_nodes"] != float64(profilesDefaultMaxNodes) {
		t.Errorf("max_nodes = %v, want %d", got.body["max_nodes"], profilesDefaultMaxNodes)
	}
}

func TestProfilesFlamegraph_Raw(t *testing.T) {
	srv, _ := newProfilesTestServer(t, 200, profilesFlamegraphBody)
	out, _, err := runCmdSplit(t, srv.URL, newProfilesFlamegraphCmd(), output.FormatTable, "--type", cpuTypeID, "--raw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp struct {
		Flamegraph struct {
			Names []string `json:"names"`
		} `json:"flamegraph"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || len(resp.Flamegraph.Names) != 3 {
		t.Errorf("--raw output is not the server tree: %q", out)
	}
}

func TestProfilesFlamegraph_BadFlags(t *testing.T) {
	for _, args := range [][]string{{"--max-nodes", "0"}, {"--top", "0"}} {
		srv, _ := newProfilesTestServer(t, 200, profilesFlamegraphBody)
		_, _, err := runCmdSplit(t, srv.URL, newProfilesFlamegraphCmd(), output.FormatJSON,
			append([]string{"--type", cpuTypeID}, args...)...)
		if err == nil {
			t.Errorf("%v did not fail", args)
		}
	}
}

func TestProfileValueCell(t *testing.T) {
	tests := []struct {
		v    int64
		unit string
		want string
	}{
		{1500000000, "nanoseconds", "1.5s"},
		{512, "bytes", "512 B"},
		{3 * 1024 * 1024 / 2, "bytes", "1.5 MiB"},
		{42, "count", "42"},
	}
	for _, tt := range tests {
		if got := profileValueCell(tt.v, tt.unit); got != tt.want {
			t.Errorf("profileValueCell(%d, %q) = %q, want %q", tt.v, tt.unit, got, tt.want)
		}
	}
}

func TestProfiles_ErrorStatus(t *testing.T) {
	srv, _ := newProfilesTestServer(t, 401, `{"code":"Unauthorized","message":"invalid API key"}`)
	_, _, err := runCmdSplit(t, srv.URL, newProfilesTypesCmd(), output.FormatJSON)
	if err == nil || !strings.Contains(err.Error(), "Authentication failed") {
		t.Errorf("err = %v, want an authentication error", err)
	}
}

func TestProfilesEmptyHints(t *testing.T) {
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		body string
		args []string
		hint string
	}{
		{"types", newProfilesTypesCmd, `{}`, nil, "No profile types between"},
		{"labels", newProfilesLabelsCmd, `{}`, nil, "No profile labels between"},
		{"label-values", newProfilesLabelValuesCmd, `{"names":[]}`, []string{"pod"}, "No values for this label between"},
		{"series", newProfilesSeriesCmd, `{}`, []string{"--type", cpuTypeID}, "No profile series between"},
		{"flamegraph", newProfilesFlamegraphCmd,
			`{"flamegraph":{"names":["total"],"levels":[{"values":[0,0,0,0]}]}}`,
			[]string{"--type", cpuTypeID}, "No profile samples between"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newProfilesTestServer(t, 200, tt.body)
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
