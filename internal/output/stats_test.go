package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPrintStats_SingleSeries(t *testing.T) {
	series := []PromSeries{
		{
			Labels: map[string]string{"job": "prometheus"},
			Values: []PromSample{
				{Timestamp: 1700000000, Value: 10},
				{Timestamp: 1700000060, Value: 20},
				{Timestamp: 1700000120, Value: 30},
				{Timestamp: 1700000180, Value: 25},
				{Timestamp: 1700000240, Value: 15},
			},
		},
	}

	var buf bytes.Buffer
	err := PrintStats(&buf, series)
	if err != nil {
		t.Fatalf("PrintStats: %v", err)
	}

	out := buf.String()
	// Should contain the header columns.
	for _, col := range []string{"SERIES", "COUNT", "MIN", "MAX", "AVG", "CURRENT", "TREND"} {
		if !strings.Contains(out, col) {
			t.Errorf("expected header with %s", col)
		}
	}
	// Should contain the series label.
	if !strings.Contains(out, "prometheus") {
		t.Errorf("expected 'prometheus' in output, got:\n%s", out)
	}
	// Should contain the summary footer.
	if !strings.Contains(out, "1 series") {
		t.Errorf("expected '1 series' in footer, got:\n%s", out)
	}
	if !strings.Contains(out, "5 total samples") {
		t.Errorf("expected '5 total samples' in footer, got:\n%s", out)
	}
}

func TestPrintStats_MultipleSeries(t *testing.T) {
	series := []PromSeries{
		{
			Labels: map[string]string{"job": "alpha"},
			Values: []PromSample{
				{Timestamp: 1700000000, Value: 1},
				{Timestamp: 1700000060, Value: 2},
				{Timestamp: 1700000120, Value: 3},
			},
		},
		{
			Labels: map[string]string{"job": "beta"},
			Values: []PromSample{
				{Timestamp: 1700000000, Value: 100},
				{Timestamp: 1700000060, Value: 200},
				{Timestamp: 1700000120, Value: 300},
			},
		},
	}

	var buf bytes.Buffer
	err := PrintStats(&buf, series)
	if err != nil {
		t.Fatalf("PrintStats: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "alpha") {
		t.Error("expected 'alpha' in output")
	}
	if !strings.Contains(out, "beta") {
		t.Error("expected 'beta' in output")
	}
	if !strings.Contains(out, "2 series") {
		t.Errorf("expected '2 series' in footer, got:\n%s", out)
	}
	if !strings.Contains(out, "6 total samples") {
		t.Errorf("expected '6 total samples' in footer, got:\n%s", out)
	}
}

func TestPrintStats_Empty(t *testing.T) {
	cases := []struct {
		name   string
		series []PromSeries
	}{
		{"nil series", nil},
		{"empty slice", []PromSeries{}},
		{"series with no values", []PromSeries{
			{Labels: map[string]string{"job": "empty"}, Values: nil},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := PrintStats(&buf, tc.series)
			if err != nil {
				t.Fatalf("PrintStats should not return error for empty results, got: %v", err)
			}
			out := buf.String()
			if !strings.Contains(out, "0 series, 0 total samples") {
				t.Errorf("expected '0 series, 0 total samples' in output, got:\n%s", out)
			}
		})
	}
}

func TestComputeTrend(t *testing.T) {
	cases := []struct {
		name   string
		values []PromSample
		want   string
	}{
		{
			name:   "single point",
			values: []PromSample{{Value: 5}},
			want:   "→",
		},
		{
			name: "flat",
			values: []PromSample{
				{Value: 10}, {Value: 10}, {Value: 10},
				{Value: 10}, {Value: 10}, {Value: 10},
			},
			want: "→",
		},
		{
			name: "rising",
			values: []PromSample{
				{Value: 1}, {Value: 2}, {Value: 3},
				{Value: 4}, {Value: 5}, {Value: 6},
				{Value: 7}, {Value: 8}, {Value: 9},
			},
			want: "↑↑",
		},
		{
			name: "falling",
			values: []PromSample{
				{Value: 9}, {Value: 8}, {Value: 7},
				{Value: 6}, {Value: 5}, {Value: 4},
				{Value: 3}, {Value: 2}, {Value: 1},
			},
			want: "↓↓",
		},
		{
			name: "slight rise",
			values: []PromSample{
				{Value: 100}, {Value: 100}, {Value: 100},
				{Value: 102}, {Value: 103}, {Value: 104},
				{Value: 106}, {Value: 107}, {Value: 108},
			},
			want: "↑",
		},
		{
			name: "slight fall",
			values: []PromSample{
				{Value: 108}, {Value: 107}, {Value: 106},
				{Value: 104}, {Value: 103}, {Value: 102},
				{Value: 100}, {Value: 100}, {Value: 100},
			},
			want: "↓",
		},
		{
			name: "zero to positive",
			values: []PromSample{
				{Value: 0}, {Value: 0}, {Value: 0},
				{Value: 1}, {Value: 2}, {Value: 3},
				{Value: 4}, {Value: 5}, {Value: 6},
			},
			want: "↑",
		},
		{
			name: "zero to negative",
			values: []PromSample{
				{Value: 0}, {Value: 0}, {Value: 0},
				{Value: -1}, {Value: -2}, {Value: -3},
				{Value: -4}, {Value: -5}, {Value: -6},
			},
			want: "↓",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeTrend(tc.values)
			if got != tc.want {
				t.Errorf("computeTrend = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatFloat(t *testing.T) {
	cases := []struct {
		input float64
		want  string
	}{
		{0, "0"},
		{1.5, "1.50"},
		{42.123, "42.12"},
		{1500, "1.50K"},
		{2500000, "2.50M"},
		{0.001, "0.0010"},
		{-1500, "-1.50K"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := formatFloat(tc.input)
			if got != tc.want {
				t.Errorf("formatFloat(%f) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"30 seconds", 30 * time.Second, "30s"},
		{"5 minutes", 5 * time.Minute, "5m"},
		{"1 hour", 1 * time.Hour, "1h"},
		{"1h30m", 90 * time.Minute, "1h30m"},
		{"1 day", 24 * time.Hour, "1d"},
		{"2d3h", 51 * time.Hour, "2d3h"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDuration(tc.d)
			if got != tc.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestPrintStats_ValuesCorrect(t *testing.T) {
	series := []PromSeries{
		{
			Labels: map[string]string{"job": "test"},
			Values: []PromSample{
				{Timestamp: 1700000000, Value: 5},
				{Timestamp: 1700000060, Value: 10},
				{Timestamp: 1700000120, Value: 15},
			},
		},
	}

	var buf bytes.Buffer
	err := PrintStats(&buf, series)
	if err != nil {
		t.Fatalf("PrintStats: %v", err)
	}

	out := buf.String()
	// Min should be 5.00.
	if !strings.Contains(out, "5.00") {
		t.Errorf("expected min '5.00' in output, got:\n%s", out)
	}
	// Max should be 15.00.
	if !strings.Contains(out, "15.00") {
		t.Errorf("expected max '15.00' in output, got:\n%s", out)
	}
	// Avg should be 10.00.
	if !strings.Contains(out, "10.00") {
		t.Errorf("expected avg '10.00' in output, got:\n%s", out)
	}
}
