package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	noiseMonitorsPath = "/v1/api/instance/inst/monitors"
	noiseMutingPath   = "/v1/api/instance/inst/muting-rules"

	noiseFlapID    = "00000000-0000-0000-0000-000000000001"
	noiseLongID    = "00000000-0000-0000-0000-000000000002"
	noiseMutedID   = "00000000-0000-0000-0000-000000000003"
	noiseUnrouteID = "00000000-0000-0000-0000-000000000004"
)

// noiseEpisodes makes the points of a series that fires in the given
// episodes. Each episode is a first and a last point index, with one point
// each minute from t0.
func noiseEpisodes(t0 int, episodes ...[2]int) [][2]any {
	var out [][2]any
	for _, e := range episodes {
		for i := e[0]; i <= e[1]; i++ {
			out = append(out, [2]any{t0 + i*60, "1"})
		}
	}
	return out
}

func noiseMatrix(series ...map[string]any) string {
	body, _ := json.Marshal(map[string]any{
		"status": "success",
		"data":   map[string]any{"resultType": "matrix", "result": series},
	})
	return string(body)
}

func noiseFiringBody() string {
	const t0 = 1700000000
	flap := noiseEpisodes(t0, [2]int{0, 4}, [2]int{20, 24}, [2]int{40, 44}, [2]int{60, 64}, [2]int{80, 84},
		[2]int{100, 104}, [2]int{120, 124}, [2]int{140, 144}, [2]int{160, 164}, [2]int{180, 184}, [2]int{200, 204})
	return noiseMatrix(
		map[string]any{"metric": map[string]string{"_oodle_monitor_id": noiseFlapID, "_oodle_severity": "warn"}, "values": flap},
		map[string]any{"metric": map[string]string{"_oodle_monitor_id": noiseLongID, "_oodle_severity": "critical"},
			"values": noiseEpisodes(t0, [2]int{0, 900})},
		map[string]any{"metric": map[string]string{"_oodle_monitor_id": noiseMutedID, "_oodle_severity": "warn"},
			"values": noiseEpisodes(t0, [2]int{0, 10}, [2]int{30, 40})},
		map[string]any{"metric": map[string]string{"_oodle_monitor_id": noiseUnrouteID, "_oodle_severity": "warn"},
			"values": noiseEpisodes(t0, [2]int{0, 10})},
	)
}

const noiseStormBody = `{"status":"success","data":{"resultType":"vector","result":[` +
	`{"metric":{"_oodle_monitor_id":"` + noiseFlapID + `"},"value":[1700604800,"3"]}]}}`

var noiseMonitorsBody = `[` +
	`{"id":"` + noiseFlapID + `","name":"Flappy","notifications":[{"matchers":[],"notifiers":{"warn":["n1"]}}]},` +
	`{"id":"` + noiseLongID + `","name":"Long","notification_policy_id":"p1"},` +
	`{"id":"` + noiseMutedID + `","name":"Muted","notification_policy_id":"p1"},` +
	`{"id":"` + noiseUnrouteID + `","name":"Nowhere","notifications":[{"matchers":[],"notifiers":{}}]}]`

var noiseMutingBody = `[` +
	`{"id":"r1","startsAt":"0001-01-01T00:00:00Z","endsAt":"0001-01-01T00:00:00Z",` +
	`"matchers":[{"name":"_oodle_monitor_id","type":0,"value":"` + noiseMutedID + `"}]},` +
	`{"id":"r2","startsAt":"2020-01-01T00:00:00Z","endsAt":"2020-01-02T00:00:00Z",` +
	`"matchers":[{"name":"_oodle_monitor_id","type":0,"value":"` + noiseFlapID + `"}]}]`

func newNoiseServer(t *testing.T) *routedServer {
	return newRoutedServer(t,
		map[string]string{noiseMonitorsPath: noiseMonitorsBody, noiseMutingPath: noiseMutingBody},
		map[string]string{"sum by (_oodle_monitor_id, _oodle_severity)": noiseFiringBody(), "max_over_time": noiseStormBody})
}

func TestMonitorsNoise_QueriesAndJSON(t *testing.T) {
	srv := newNoiseServer(t)
	stdout, stderr, err := runCmdSplit(t, srv.URL, newMonitorsNoiseCmd(), output.FormatJSON,
		"--start", "1700000000", "--end", "1700604800", "--label", "env=prod")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stderr)
	}
	ranges := srv.find("/api/v1/query_range")
	if len(ranges) != 1 {
		t.Fatalf("query_range requests = %d", len(ranges))
	}
	wantRange := `sum by (_oodle_monitor_id, _oodle_severity) (ALERTS{alertstate="firing", env="prod"})`
	if got := ranges[0].query.Get("query"); got != wantRange {
		t.Errorf("range query = %s", got)
	}
	if ranges[0].query.Get("step") != "60s" {
		t.Errorf("step = %s", ranges[0].query.Get("step"))
	}
	instants := srv.find("/api/v1/query")
	wantStorm := `max_over_time(count(ALERTS{alertstate="firing", env="prod"}) by (_oodle_monitor_id)[604800s:60s])`
	if len(instants) != 1 || instants[0].query.Get("query") != wantStorm {
		t.Errorf("storm query = %+v", instants)
	}

	var res noiseResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if res.Summary.MonitorsAnalyzed != 4 || res.Summary.MutedExcluded != 1 || res.Summary.UnroutedExcluded != 1 {
		t.Errorf("summary = %+v", res.Summary)
	}
	if res.Filters["env"] != "prod" {
		t.Errorf("filters = %v", res.Filters)
	}
	if len(res.Monitors) != 2 {
		t.Fatalf("monitors = %+v", res.Monitors)
	}
	flap := res.Monitors[0]
	if flap.MonitorID != noiseFlapID || flap.Name != "Flappy" || flap.TriggerCount != 11 ||
		flap.MaxSeries != 3 || flap.Category != "flapping" || !flap.Noisy || flap.AvgDurationMin != 4 {
		t.Errorf("flapping monitor = %+v", flap)
	}
	long := res.Monitors[1]
	if long.TriggerCount != 1 || long.Category != "perpetual" || !long.Noisy || long.MTTRHours != 15 {
		t.Errorf("long monitor = %+v", long)
	}
}

func TestMonitorsNoise_TableSortAndIncludes(t *testing.T) {
	srv := newNoiseServer(t)
	stdout, _, err := runCmdSplit(t, srv.URL, newMonitorsNoiseCmd(), output.FormatTable,
		"--start", "1700000000", "--end", "1700604800", "--sort", "mttr", "--include-muted", "--include-unrouted", "--top", "3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(srv.find(noiseMutingPath)) != 0 {
		t.Errorf("muting rules were read with --include-muted")
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if !strings.HasPrefix(lines[0], "NAME") || !strings.HasPrefix(lines[1], "Long") {
		t.Errorf("rows are not sorted by MTTR:\n%s", stdout)
	}
	if len(lines) != 6 {
		t.Errorf("--top 3 is not applied:\n%s", stdout)
	}
	if !strings.Contains(stdout, "4 monitors fired") {
		t.Errorf("no summary line:\n%s", stdout)
	}
}

func TestMonitorsNoise_LookupFailureKeepsRows(t *testing.T) {
	srv := newRoutedServer(t, nil, map[string]string{
		"sum by (_oodle_monitor_id, _oodle_severity)": noiseFiringBody(), "max_over_time": noiseStormBody})
	stdout, stderr, err := runCmdSplit(t, srv.URL, newMonitorsNoiseCmd(), output.FormatJSON,
		"--start", "1700000000", "--end", "1700604800")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "could not read monitors") || !strings.Contains(stderr, "could not read muting rules") {
		t.Errorf("stderr = %q", stderr)
	}
	var res noiseResult
	_ = json.Unmarshal([]byte(stdout), &res)
	if len(res.Monitors) != 4 {
		t.Errorf("rows were removed after a failed lookup: %+v", res.Monitors)
	}
}

func TestMonitorsNoise_Validation(t *testing.T) {
	for _, args := range [][]string{
		{"--sort", "name"},
		{"--category", "loud"},
		{"--label", "bad label=x"},
		{"--top", "0"},
	} {
		srv := newRoutedServer(t, nil, nil)
		_, _, err := runCmdSplit(t, srv.URL, newMonitorsNoiseCmd(), output.FormatTable, args...)
		if err == nil {
			t.Errorf("%v: expected an error", args)
		}
		if len(srv.requests) != 0 {
			t.Errorf("%v: request was sent for bad input", args)
		}
	}
}

func TestDetectFiringEpisodes(t *testing.T) {
	pts := []promPoint{{0, 1}, {60, 1}, {120, 1}, {300, 1}, {360, 0}, {420, 1}}
	got := detectFiringEpisodes(pts, 60)
	want := []noiseEpisode{{0, 120}, {300, 300}, {420, 420}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("episodes = %v, want %v", got, want)
	}
}

func TestClassifyNoise(t *testing.T) {
	tests := []struct {
		triggers  int
		avg, day  float64
		maxSeries int
		want      string
	}{
		{1, 10, 0.1, 11, "storm"},
		{20, 10, 3, 1, "flapping"},
		{1, 800, 0.1, 1, "perpetual"},
		{5, 10, 0.8, 1, "auto_resolving"},
		{5, 40, 0.8, 1, "boundary"},
		{3, 100, 0.4, 1, "uncategorized"},
	}
	for _, tt := range tests {
		if got := classifyNoise(tt.triggers, tt.avg, tt.day, tt.maxSeries); got != tt.want {
			t.Errorf("classifyNoise(%+v) = %s", tt, got)
		}
	}
}

func TestMonitorsNoiseBreakdown(t *testing.T) {
	const t0 = 1700000000
	body := noiseMatrix(
		map[string]any{"metric": map[string]string{"namespace": "a", "_oodle_monitor_id": noiseFlapID},
			"values": noiseEpisodes(t0, [2]int{0, 30}, [2]int{60, 90})},
		map[string]any{"metric": map[string]string{"namespace": "b", "_oodle_monitor_id": noiseLongID},
			"values": noiseEpisodes(t0, [2]int{0, 20})},
		map[string]any{"metric": map[string]string{"namespace": "a", "_oodle_monitor_id": noiseMutedID},
			"values": noiseEpisodes(t0, [2]int{0, 600})},
		map[string]any{"metric": map[string]string{"_oodle_monitor_id": noiseLongID},
			"values": noiseEpisodes(t0, [2]int{0, 10})},
	)
	srv := newRoutedServer(t, map[string]string{noiseMutingPath: noiseMutingBody},
		map[string]string{"ALERTS": body})
	stdout, _, err := runCmdSplit(t, srv.URL, newMonitorsNoiseBreakdownCmd(), output.FormatJSON,
		"--group-by", "namespace", "--start", "1700000000", "--end", "1700604800")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := srv.find("/api/v1/query_range")[0].query.Get("query")
	if q != `sum by (namespace, _oodle_monitor_id) (ALERTS{alertstate="firing"})` {
		t.Errorf("query = %s", q)
	}
	var res noiseBreakdownResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatal(err)
	}
	if res.Summary.MutedSeriesExcluded != 1 || res.Summary.TotalTriggers != 4 || res.Summary.TotalFiringMinutes != 90 {
		t.Errorf("summary = %+v", res.Summary)
	}
	if len(res.Breakdown) != 3 || res.Breakdown[0].Value != "a" || res.Breakdown[0].FiringMinutes != 60 ||
		res.Breakdown[0].TriggerCount != 2 || res.Breakdown[0].PercentOfTotal != 66.7 || res.Breakdown[2].Value != "(none)" {
		t.Errorf("breakdown = %+v", res.Breakdown)
	}
}

func TestMonitorsNoiseBreakdown_MonitorFilterAndTable(t *testing.T) {
	srv := newRoutedServer(t, nil, nil)
	stdout, _, err := runCmdSplit(t, srv.URL, newMonitorsNoiseBreakdownCmd(), output.FormatTable,
		"--group-by", "pod", "--monitor", `m"1`, "--include-muted")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := srv.find("/api/v1/query_range")[0].query.Get("query")
	if q != `sum by (pod) (ALERTS{alertstate="firing", _oodle_monitor_id="m\"1"})` {
		t.Errorf("query = %s", q)
	}
	if !strings.Contains(stdout, "POD") || !strings.Contains(stdout, "FIRING TIME") {
		t.Errorf("table header = %q", stdout)
	}
	if _, _, err := runCmdSplit(t, srv.URL, newMonitorsNoiseBreakdownCmd(), output.FormatTable,
		"--group-by", "pod) or vector(1"); err == nil {
		t.Errorf("a bad --group-by is accepted")
	}
}
