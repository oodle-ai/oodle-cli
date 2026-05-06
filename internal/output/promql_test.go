package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatPromQLResult_Vector(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{
					"metric": {"__name__": "up", "instance": "localhost:9090", "job": "prometheus"},
					"value": [1700000000, "1"]
				},
				{
					"metric": {"__name__": "up", "instance": "localhost:9100", "job": "node"},
					"value": [1700000000, "0"]
				}
			]
		}
	}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatTable, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for vector result")
	}
	out := buf.String()
	// Check headers
	if !strings.Contains(out, "METRIC") {
		t.Errorf("missing METRIC header: %s", out)
	}
	if !strings.Contains(out, "TIMESTAMP") {
		t.Errorf("missing TIMESTAMP header: %s", out)
	}
	if !strings.Contains(out, "VALUE") {
		t.Errorf("missing VALUE header: %s", out)
	}
	// Check metric labels
	if !strings.Contains(out, `instance="localhost:9090"`) {
		t.Errorf("missing instance label: %s", out)
	}
	if !strings.Contains(out, `job="prometheus"`) {
		t.Errorf("missing job label: %s", out)
	}
	// Check __name__ is rendered as prefix
	if !strings.Contains(out, "up{") {
		t.Errorf("expected metric name prefix 'up{': %s", out)
	}
	// Check values
	if !strings.Contains(out, "1") {
		t.Errorf("missing value '1': %s", out)
	}
	if !strings.Contains(out, "0") {
		t.Errorf("missing value '0': %s", out)
	}
}

func TestFormatPromQLResult_VectorNoName(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{
					"metric": {"instance": "localhost:9090"},
					"value": [1700000000, "42.5"]
				}
			]
		}
	}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatTable, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	out := buf.String()
	if !strings.Contains(out, `{instance="localhost:9090"}`) {
		t.Errorf("expected metric without name prefix: %s", out)
	}
	if !strings.Contains(out, "42.5") {
		t.Errorf("missing value: %s", out)
	}
}

func TestFormatPromQLResult_VectorEmptyResult(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": []
		}
	}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatTable, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	out := buf.String()
	// Should have headers but no data rows
	if !strings.Contains(out, "METRIC") {
		t.Errorf("missing header: %s", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line (header only), got %d: %s", len(lines), out)
	}
}

func TestFormatPromQLResult_Matrix(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"__name__": "http_requests_total", "method": "GET"},
					"values": [
						[1700000000, "100"],
						[1700000060, "105"],
						[1700000120, "110"]
					]
				},
				{
					"metric": {"__name__": "http_requests_total", "method": "POST"},
					"values": [
						[1700000000, "50"],
						[1700000060, "52"]
					]
				}
			]
		}
	}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatTable, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for matrix result")
	}
	out := buf.String()
	// Check headers
	if !strings.Contains(out, "METRIC") {
		t.Errorf("missing METRIC header: %s", out)
	}
	if !strings.Contains(out, "VALUES") {
		t.Errorf("missing VALUES header: %s", out)
	}
	// Check metric labels
	if !strings.Contains(out, `method="GET"`) {
		t.Errorf("missing method=GET: %s", out)
	}
	if !strings.Contains(out, `method="POST"`) {
		t.Errorf("missing method=POST: %s", out)
	}
	// Check values are present
	if !strings.Contains(out, "100@Nov14") {
		t.Errorf("missing value 100 with compact time: %s", out)
	}
	if !strings.Contains(out, "105@Nov14") {
		t.Errorf("missing value 105 with compact time: %s", out)
	}
}

func TestFormatPromQLResult_MatrixTruncation(t *testing.T) {
	// Build a matrix result with more than maxMatrixSamples points
	values := make([]any, 20)
	for i := range values {
		values[i] = []any{float64(1700000000 + i*60), "1"}
	}
	raw := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result": []any{
				map[string]any{
					"metric": map[string]any{"job": "test"},
					"values": values,
				},
			},
		},
	}
	rawJSON, _ := json.Marshal(raw)
	var parsed any
	if err := json.Unmarshal(rawJSON, &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatTable, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	out := buf.String()
	if !strings.Contains(out, "... (20 total)") {
		t.Errorf("expected truncation indicator: %s", out)
	}
}

func TestFormatPromQLResult_Scalar(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "scalar",
			"result": [1700000000, "42"]
		}
	}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatTable, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for scalar result")
	}
	out := buf.String()
	if !strings.Contains(out, "TIMESTAMP") {
		t.Errorf("missing TIMESTAMP header: %s", out)
	}
	if !strings.Contains(out, "VALUE") {
		t.Errorf("missing VALUE header: %s", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("missing value '42': %s", out)
	}
	if !strings.Contains(out, "2023-11-14") {
		t.Errorf("missing formatted date: %s", out)
	}
}

func TestFormatPromQLResult_String(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "string",
			"result": [1700000000, "hello world"]
		}
	}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatTable, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for string result")
	}
	out := buf.String()
	if !strings.Contains(out, "hello world") {
		t.Errorf("missing string value: %s", out)
	}
}

func TestFormatPromQLResult_NonPromResponse(t *testing.T) {
	// A response that doesn't look like Prometheus should return handled=false
	raw := `{"items": [{"name": "foo"}]}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatTable, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult: %v", err)
	}
	if handled {
		t.Fatal("expected handled=false for non-Prometheus response")
	}
}

func TestFormatPromQLResult_MissingData(t *testing.T) {
	raw := `{"status": "success"}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, _ := FormatPromQLResult(&buf, FormatTable, parsed)
	if handled {
		t.Fatal("expected handled=false when data field is missing")
	}
}

func TestFormatPromQLResult_UnknownResultType(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "unknown_type",
			"result": []
		}
	}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, _ := FormatPromQLResult(&buf, FormatTable, parsed)
	if handled {
		t.Fatal("expected handled=false for unknown resultType")
	}
}

func TestFormatPromQLResult_CSV(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{
					"metric": {"__name__": "up", "job": "test"},
					"value": [1700000000, "1"]
				}
			]
		}
	}`
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	handled, err := FormatPromQLResult(&buf, FormatCSV, parsed)
	if err != nil {
		t.Fatalf("FormatPromQLResult CSV: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 CSV lines, got %d: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], "METRIC") {
		t.Errorf("missing CSV header: %s", lines[0])
	}
}

func TestFormatMetricLabels(t *testing.T) {
	tests := []struct {
		name   string
		metric any
		want   string
	}{
		{
			name:   "with __name__",
			metric: map[string]any{"__name__": "up", "job": "prom", "instance": "localhost"},
			want:   `up{instance="localhost", job="prom"}`,
		},
		{
			name:   "without __name__",
			metric: map[string]any{"job": "prom"},
			want:   `{job="prom"}`,
		},
		{
			name:   "empty metric",
			metric: map[string]any{},
			want:   "{}",
		},
		{
			name:   "nil metric",
			metric: nil,
			want:   "{}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetricLabels(tt.metric)
			if got != tt.want {
				t.Errorf("formatMetricLabels() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractSample(t *testing.T) {
	tests := []struct {
		name      string
		sample    any
		wantTS    string
		wantValue string
	}{
		{
			name:      "normal sample",
			sample:    []any{float64(1700000000), "42.5"},
			wantTS:    "2023-11-14 22:13:20",
			wantValue: "42.5",
		},
		{
			name:      "nil sample",
			sample:    nil,
			wantTS:    "",
			wantValue: "",
		},
		{
			name:      "short array",
			sample:    []any{float64(1700000000)},
			wantTS:    "",
			wantValue: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, val := extractSample(tt.sample)
			if ts != tt.wantTS {
				t.Errorf("timestamp = %q, want %q", ts, tt.wantTS)
			}
			if val != tt.wantValue {
				t.Errorf("value = %q, want %q", val, tt.wantValue)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	got := formatTimestamp(float64(1700000000))
	want := "2023-11-14 22:13:20"
	if got != want {
		t.Errorf("formatTimestamp(1700000000) = %q, want %q", got, want)
	}
}

func TestCompactTime(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"2023-11-14 22:13:20", "Nov14 22:13:20"},
		{"2024-01-02 08:00:00", "Jan02 08:00:00"},
		{"short", "short"},
		{"", ""},
	}
	for _, tt := range tests {
		got := compactTime(tt.in)
		if got != tt.want {
			t.Errorf("compactTime(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
