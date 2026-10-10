package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// traceAnomalyMatrix has 15 one-minute points from 1700000100 (a period
// boundary). The first 10 minutes are quiet. Minutes 10 to 14 have many
// errors and slow spans.
func traceAnomalyMatrix() string {
	type series struct {
		Metric map[string]string `json:"metric"`
		Values [][2]any          `json:"values"`
	}
	mk := func(bucket, status string, val func(i int) string) series {
		s := series{Metric: map[string]string{"duration_ns_bucket": bucket, "span_status": status}}
		for i := 0; i < 15; i++ {
			s.Values = append(s.Values, [2]any{1700000100 + i*60, val(i)})
		}
		return s
	}
	fast := mk("1000000", "Ok", func(i int) string { return "100" })
	slow := mk("500000000", "Ok", func(i int) string {
		if i >= 10 {
			return "100"
		}
		return "1"
	})
	errs := mk("1000000", "Error", func(i int) string {
		if i >= 10 {
			return "50"
		}
		return "NaN"
	})
	body, _ := json.Marshal(map[string]any{
		"status": "success",
		"data":   map[string]any{"resultType": "matrix", "result": []series{fast, slow, errs}},
	})
	return string(body)
}

func TestTracesAnomalies_QueryAndAnalysis(t *testing.T) {
	srv := newRoutedServer(t, nil, map[string]string{"oodle_trace_metrics": traceAnomalyMatrix()})
	stdout, _, err := runCmdSplit(t, srv.URL, newTracesAnomaliesCmd(), output.FormatJSON,
		"--service", `api"x`, "--env", "prod", "--start", "1700000040", "--end", "1700003640")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	reqs := srv.find("/api/v1/query_range")
	if len(reqs) != 1 {
		t.Fatalf("query_range requests = %d", len(reqs))
	}
	q := reqs[0].query
	wantQuery := `sum by (duration_ns_bucket, span_status) (increase(oodle_trace_metrics{service_name="api\"x", env="prod"}[60s]))`
	if q.Get("query") != wantQuery {
		t.Errorf("query = %s\nwant    %s", q.Get("query"), wantQuery)
	}
	if q.Get("step") != "60s" || q.Get("start") != "1700000040" || q.Get("end") != "1700003640" {
		t.Errorf("params = %v", q)
	}
	if reqs[0].inst != "inst" {
		t.Errorf("OODLE-INSTANCE = %q", reqs[0].inst)
	}

	var res traceAnomalyResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	// 15*100 fast + (10*1 + 5*100) slow + 5*50 errors.
	if res.Overall.TotalSpans != 2260 || res.Overall.ErrorSpans != 250 || res.Overall.SlowSpans != 510 {
		t.Errorf("overall = %+v", res.Overall)
	}
	if res.PeriodSeconds != 300 || len(res.Periods) != 3 {
		t.Fatalf("periods = %d of %ds", len(res.Periods), res.PeriodSeconds)
	}
	last := res.Periods[2]
	if len(last.Anomalies) != 2 || !strings.HasPrefix(last.Anomalies[0], "High error rate") ||
		!strings.HasPrefix(last.Anomalies[1], "Elevated latency") {
		t.Errorf("last period anomalies = %v", last.Anomalies)
	}
	if len(res.Periods[0].Anomalies) != 0 {
		t.Errorf("quiet period is marked: %v", res.Periods[0].Anomalies)
	}
	if last.TopBuckets[0].Duration != "1ms" {
		t.Errorf("top bucket = %+v", last.TopBuckets)
	}
	if len(res.Insights) == 0 {
		t.Errorf("no insights")
	}
}

func TestTracesAnomalies_TableAnomalousOnly(t *testing.T) {
	srv := newRoutedServer(t, nil, map[string]string{"oodle_trace_metrics": traceAnomalyMatrix()})
	stdout, _, err := runCmdSplit(t, srv.URL, newTracesAnomaliesCmd(), output.FormatTable,
		"--service", "api", "--start", "1700000040", "--end", "1700003640", "--anomalous-only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout, "2023-11-14 22:15") {
		t.Errorf("a quiet period is shown with --anomalous-only:\n%s", stdout)
	}
	for _, want := range []string{"PERIOD START (UTC)", "2023-11-14 22:25", "High error rate", "Total: 2260 spans"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestTracesAnomalies_Validation(t *testing.T) {
	srv := newRoutedServer(t, nil, nil)
	_, _, err := runCmdSplit(t, srv.URL, newTracesAnomaliesCmd(), output.FormatTable,
		"--service", "api", "--start", "-7d", "--period", "1m")
	if err == nil || !strings.Contains(err.Error(), "--period") {
		t.Fatalf("expected a --period error, got %v", err)
	}
}

func TestTraceAnomalyStepAndPeriod(t *testing.T) {
	tests := []struct {
		rangeSec, step, period int64
	}{
		{3600, 60, 300},
		{86400, 60, 1440},
		{7 * 86400, 420, 10080},
	}
	for _, tt := range tests {
		step := traceAnomalyStep(tt.rangeSec)
		period := traceAnomalyPeriod(tt.rangeSec, step)
		if step != tt.step || period != tt.period {
			t.Errorf("range %d: step %d period %d, want %d %d", tt.rangeSec, step, period, tt.step, tt.period)
		}
	}
}
