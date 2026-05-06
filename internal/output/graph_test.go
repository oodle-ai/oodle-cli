package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParsePromResponse_Matrix(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result": []any{
				map[string]any{
					"metric": map[string]any{
						"__name__": "up",
						"job":      "prometheus",
					},
					"values": []any{
						[]any{float64(1700000000), "1"},
						[]any{float64(1700000060), "1"},
						[]any{float64(1700000120), "0.5"},
					},
				},
				map[string]any{
					"metric": map[string]any{
						"__name__": "up",
						"job":      "grafana",
					},
					"values": []any{
						[]any{float64(1700000000), "0.8"},
						[]any{float64(1700000060), "0.9"},
						[]any{float64(1700000120), "1"},
					},
				},
			},
		},
	}

	series, err := ParsePromResponse(parsed)
	if err != nil {
		t.Fatalf("ParsePromResponse: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("expected 2 series, got %d", len(series))
	}

	// First series.
	if series[0].Labels["job"] != "prometheus" {
		t.Errorf("series[0] job = %q, want 'prometheus'", series[0].Labels["job"])
	}
	if len(series[0].Values) != 3 {
		t.Fatalf("series[0] values = %d, want 3", len(series[0].Values))
	}
	if series[0].Values[0].Value != 1.0 {
		t.Errorf("series[0].Values[0].Value = %f, want 1.0", series[0].Values[0].Value)
	}
	if series[0].Values[2].Value != 0.5 {
		t.Errorf("series[0].Values[2].Value = %f, want 0.5", series[0].Values[2].Value)
	}

	// Second series.
	if series[1].Labels["job"] != "grafana" {
		t.Errorf("series[1] job = %q, want 'grafana'", series[1].Labels["job"])
	}
	if series[1].Values[2].Value != 1.0 {
		t.Errorf("series[1].Values[2].Value = %f, want 1.0", series[1].Values[2].Value)
	}
}

func TestParsePromResponse_Vector(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result": []any{
				map[string]any{
					"metric": map[string]any{
						"instance": "localhost:9090",
					},
					"value": []any{float64(1700000000), "42"},
				},
			},
		},
	}

	series, err := ParsePromResponse(parsed)
	if err != nil {
		t.Fatalf("ParsePromResponse: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(series))
	}
	if series[0].Values[0].Value != 42.0 {
		t.Errorf("value = %f, want 42.0", series[0].Values[0].Value)
	}
	if series[0].Labels["instance"] != "localhost:9090" {
		t.Errorf("instance = %q, want 'localhost:9090'", series[0].Labels["instance"])
	}
}

func TestParsePromResponse_Scalar(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "scalar",
			"result":     []any{float64(1700000000), "42"},
		},
	}

	_, err := ParsePromResponse(parsed)
	if err == nil {
		t.Fatal("expected error for scalar result type")
	}
	if !strings.Contains(err.Error(), "scalar") {
		t.Errorf("error should mention 'scalar', got: %v", err)
	}
}

func TestParsePromResponse_MissingData(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
	}

	_, err := ParsePromResponse(parsed)
	if err == nil {
		t.Fatal("expected error for missing data field")
	}
}

func TestParsePromResponse_NotObject(t *testing.T) {
	_, err := ParsePromResponse([]any{"not", "an", "object"})
	if err == nil {
		t.Fatal("expected error for non-object response")
	}
}

func TestParsePromResponse_NaNValues(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result": []any{
				map[string]any{
					"metric": map[string]any{"job": "test"},
					"values": []any{
						[]any{float64(1700000000), "1"},
						[]any{float64(1700000060), "NaN"},
						[]any{float64(1700000120), "2"},
					},
				},
			},
		},
	}

	series, err := ParsePromResponse(parsed)
	if err != nil {
		t.Fatalf("ParsePromResponse: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(series))
	}
	// NaN values should be skipped.
	if len(series[0].Values) != 2 {
		t.Errorf("expected 2 values (NaN skipped), got %d", len(series[0].Values))
	}
}

func TestParsePromResponse_InfValues(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result": []any{
				map[string]any{
					"metric": map[string]any{"job": "test"},
					"values": []any{
						[]any{float64(1700000000), "1"},
						[]any{float64(1700000060), "+Inf"},
						[]any{float64(1700000120), "2"},
					},
				},
			},
		},
	}

	series, err := ParsePromResponse(parsed)
	if err != nil {
		t.Fatalf("ParsePromResponse: %v", err)
	}
	if len(series[0].Values) != 2 {
		t.Errorf("expected 2 values (+Inf skipped), got %d", len(series[0].Values))
	}
}

func TestParsePromResponse_EmptyResult(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result":     []any{},
		},
	}

	series, err := ParsePromResponse(parsed)
	if err != nil {
		t.Fatalf("ParsePromResponse: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("expected 0 series, got %d", len(series))
	}
}

func TestFormatLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "empty",
			labels: nil,
			want:   "{}",
		},
		{
			name:   "only __name__",
			labels: map[string]string{"__name__": "up"},
			want:   "up",
		},
		{
			name:   "single label",
			labels: map[string]string{"job": "prometheus"},
			want:   `{job="prometheus"}`,
		},
		{
			name:   "multiple labels sorted",
			labels: map[string]string{"method": "GET", "handler": "/api"},
			want:   `{handler="/api", method="GET"}`,
		},
		{
			name:   "__name__ excluded from display",
			labels: map[string]string{"__name__": "http_requests", "job": "web"},
			want:   `{job="web"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLabels(tc.labels)
			if got != tc.want {
				t.Errorf("formatLabels(%v) = %q, want %q", tc.labels, got, tc.want)
			}
		})
	}
}

func TestPrintGraph_SingleSeries(t *testing.T) {
	series := []PromSeries{
		{
			Labels: map[string]string{"job": "test"},
			Values: []PromSample{
				{Timestamp: 1700000000, Value: 1},
				{Timestamp: 1700000060, Value: 2},
				{Timestamp: 1700000120, Value: 3},
				{Timestamp: 1700000180, Value: 2},
				{Timestamp: 1700000240, Value: 1},
			},
		},
	}

	var buf bytes.Buffer
	err := PrintGraph(&buf, series)
	if err != nil {
		t.Fatalf("PrintGraph: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected non-empty graph output")
	}
	// Should contain the formatted label in the legend.
	if !strings.Contains(out, `{job="test"}`) {
		t.Errorf("expected legend with '{job=\"test\"}' in output, got:\n%s", out)
	}
}

func TestPrintGraph_MultipleSeries(t *testing.T) {
	series := []PromSeries{
		{
			Labels: map[string]string{"job": "alpha"},
			Values: []PromSample{
				{Timestamp: 1700000000, Value: 1},
				{Timestamp: 1700000060, Value: 3},
				{Timestamp: 1700000120, Value: 2},
			},
		},
		{
			Labels: map[string]string{"job": "beta"},
			Values: []PromSample{
				{Timestamp: 1700000000, Value: 5},
				{Timestamp: 1700000060, Value: 4},
				{Timestamp: 1700000120, Value: 6},
			},
		},
	}

	var buf bytes.Buffer
	err := PrintGraph(&buf, series)
	if err != nil {
		t.Fatalf("PrintGraph: %v", err)
	}

	out := buf.String()
	// Should contain both legends.
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected 'alpha' in legend")
	}
	if !strings.Contains(out, "beta") {
		t.Errorf("expected 'beta' in legend")
	}
}

func TestPrintGraph_EmptySeries(t *testing.T) {
	err := PrintGraph(&bytes.Buffer{}, nil)
	if err == nil {
		t.Fatal("expected error for nil series")
	}

	err = PrintGraph(&bytes.Buffer{}, []PromSeries{})
	if err == nil {
		t.Fatal("expected error for empty series")
	}
}

func TestPrintGraph_SeriesWithNoValues(t *testing.T) {
	series := []PromSeries{
		{
			Labels: map[string]string{"job": "empty"},
			Values: nil,
		},
	}

	err := PrintGraph(&bytes.Buffer{}, series)
	if err == nil {
		t.Fatal("expected error for series with no values")
	}
}

func TestToFloat64(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  float64
		err   bool
	}{
		{"float64", float64(3.14), 3.14, false},
		{"string float", "42.5", 42.5, false},
		{"string int", "1", 1.0, false},
		{"int", int(7), 7.0, false},
		{"int64", int64(99), 99.0, false},
		{"bool unsupported", true, 0, true},
		{"nil unsupported", nil, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toFloat64(tc.input)
			if tc.err {
				if err == nil {
					t.Errorf("toFloat64(%v) expected error", tc.input)
				}
				return
			}
			if err != nil {
				t.Errorf("toFloat64(%v) unexpected error: %v", tc.input, err)
				return
			}
			if got != tc.want {
				t.Errorf("toFloat64(%v) = %f, want %f", tc.input, got, tc.want)
			}
		})
	}
}

func TestParsePromResponse_VectorNaN(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result": []any{
				map[string]any{
					"metric": map[string]any{"job": "test"},
					"value":  []any{float64(1700000000), "NaN"},
				},
				map[string]any{
					"metric": map[string]any{"job": "healthy"},
					"value":  []any{float64(1700000000), "42"},
				},
			},
		},
	}

	series, err := ParsePromResponse(parsed)
	if err != nil {
		t.Fatalf("ParsePromResponse: %v", err)
	}
	// NaN vector entry should be skipped.
	if len(series) != 1 {
		t.Fatalf("expected 1 series (NaN skipped), got %d", len(series))
	}
	if series[0].Labels["job"] != "healthy" {
		t.Errorf("expected 'healthy' series, got %q", series[0].Labels["job"])
	}
}

func TestParseSamplePair(t *testing.T) {
	cases := []struct {
		name    string
		input   any
		wantTS  float64
		wantVal float64
		wantErr bool
	}{
		{"valid pair", []any{float64(1700000000), "42"}, 1700000000, 42, false},
		{"not array", "bad", 0, 0, true},
		{"too short", []any{float64(1)}, 0, 0, true},
		{"NaN value", []any{float64(1700000000), "NaN"}, 0, 0, true},
		{"Inf value", []any{float64(1700000000), "+Inf"}, 0, 0, true},
		{"bad timestamp", []any{true, "1"}, 0, 0, true},
		{"bad value", []any{float64(1700000000), true}, 0, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sample, err := parseSamplePair(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseSamplePair(%v) expected error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSamplePair(%v) unexpected error: %v", tc.input, err)
			}
			if sample.Timestamp != tc.wantTS {
				t.Errorf("Timestamp = %f, want %f", sample.Timestamp, tc.wantTS)
			}
			if sample.Value != tc.wantVal {
				t.Errorf("Value = %f, want %f", sample.Value, tc.wantVal)
			}
		})
	}
}

func TestPrintGraph_HistoricalTimeRange(t *testing.T) {
	// Regression test: historical data (timestamps well before time.Now())
	// should render X-axis labels based on the data timestamps, not the
	// current wall clock. Use timestamps from 2020-01-01.
	baseTS := float64(1577836800) // 2020-01-01 00:00:00 UTC
	series := []PromSeries{
		{
			Labels: map[string]string{"job": "historical"},
			Values: []PromSample{
				{Timestamp: baseTS, Value: 10},
				{Timestamp: baseTS + 60, Value: 20},
				{Timestamp: baseTS + 120, Value: 15},
				{Timestamp: baseTS + 180, Value: 25},
				{Timestamp: baseTS + 240, Value: 30},
			},
		},
	}

	var buf bytes.Buffer
	err := PrintGraph(&buf, series)
	if err != nil {
		t.Fatalf("PrintGraph: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected non-empty graph output")
	}

	// The rendered output should NOT contain timestamps near "now" —
	// the X-axis labels should reflect the data's 2020 timestamps.
	// We verify by checking that the output contains a time string
	// that corresponds to the data range. The exact format depends on
	// the local timezone, but the time.Unix(1577836800, 0) hour:minute
	// should appear somewhere in the output.
	dataTime := time.Unix(int64(baseTS), 0).Local()
	// The chart uses "15:04:05" format for ranges < 24h.
	expectedTimePart := dataTime.Format("15:04")
	if !strings.Contains(out, expectedTimePart) {
		t.Errorf("expected X-axis to contain time near %q for historical data, got:\n%s", expectedTimePart, out)
	}
}

func TestPrintGraph_SinglePointVector(t *testing.T) {
	// Single data point (instant query / vector) should render without error.
	// The minT == maxT case should be handled by expanding the range.
	series := []PromSeries{
		{
			Labels: map[string]string{"job": "instant"},
			Values: []PromSample{
				{Timestamp: 1700000000, Value: 42},
			},
		},
	}

	var buf bytes.Buffer
	err := PrintGraph(&buf, series)
	if err != nil {
		t.Fatalf("PrintGraph: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected non-empty graph output")
	}
}

func TestParsePromResponse_VectorInf(t *testing.T) {
	parsed := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result": []any{
				map[string]any{
					"metric": map[string]any{"job": "test"},
					"value":  []any{float64(1700000000), "+Inf"},
				},
			},
		},
	}

	series, err := ParsePromResponse(parsed)
	if err != nil {
		t.Fatalf("ParsePromResponse: %v", err)
	}
	// +Inf vector entry should be skipped.
	if len(series) != 0 {
		t.Fatalf("expected 0 series (+Inf skipped), got %d", len(series))
	}
}
