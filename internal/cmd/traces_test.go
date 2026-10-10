package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

var testTraceLabels = []string{
	"parent_span_id",
	"resource::env",
	"resource::service.name",
	"resource::service.version",
	"span::env",
	"span::http.method",
}

func TestResolveTraceLabel(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"resource::service.name", "resource::service.name"},
		{"parent_span_id", "parent_span_id"},
		{"service.name", "resource::service.name"},
		{"service", "resource::service.name"},
		{"http.method", "span::http.method"},
	}
	for _, tt := range tests {
		got, err := resolveTraceLabel(tt.name, testTraceLabels)
		if err != nil {
			t.Errorf("resolveTraceLabel(%q): %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("resolveTraceLabel(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestResolveTraceLabel_Ambiguous(t *testing.T) {
	_, err := resolveTraceLabel("env", testTraceLabels)
	if err == nil {
		t.Fatal("expected an error for an ambiguous label")
	}
	for _, want := range []string{"ambiguous", "resource::env", "span::env"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestResolveTraceLabel_UnknownSuggests(t *testing.T) {
	_, err := resolveTraceLabel("service.nam", testTraceLabels)
	if err == nil {
		t.Fatal("expected an error for an unknown label")
	}
	msg := err.Error()
	if !strings.Contains(msg, `unknown trace label "service.nam"`) {
		t.Errorf("unexpected error: %q", msg)
	}
	if !strings.Contains(msg, "did you mean resource::service.name") {
		t.Errorf("expected suggestion of resource::service.name, got: %q", msg)
	}
}

func TestResolveTraceLabel_UnknownNoSuggestion(t *testing.T) {
	_, err := resolveTraceLabel("zzzzzzzz", testTraceLabels)
	if err == nil {
		t.Fatal("expected an error for an unknown label")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected no suggestion, got: %q", err)
	}
	if !strings.Contains(err.Error(), "oodle traces labels") {
		t.Errorf("expected a hint to run 'oodle traces labels', got: %q", err)
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"service.name", "service.name", 0},
	}
	for _, tt := range tests {
		if got := levenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// testTracesBody has one trace. The child span starts first and ends last,
// so the start and duration must come from all spans, not from the root.
const testTracesBody = `{"data":[{"traceID":"t1","processes":{"p1":{"serviceName":"api"},"p2":{"serviceName":"db"}},` +
	`"spans":[` +
	`{"traceID":"t1","spanID":"s2","parentSpanID":"s1","operationName":"SELECT","processID":"p2",` +
	`"startTime":1699999999000000,"duration":3000000},` +
	`{"traceID":"t1","spanID":"s1","parentSpanID":"","operationName":"GET /users","processID":"p1",` +
	`"startTime":1700000000000000,"duration":1500000}]}],` +
	`"limit":20,"offset":0,"total":1,"future_field":1}`

func TestTracesTable(t *testing.T) {
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{"list", newTracesListCmd, []string{"--start", "-1h", "--end", "now"}},
		{"get", newTracesGetCmd, []string{"t1", "--start", "-1h", "--end", "now"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _, _ := logMetricsServer(t, testTracesBody)
			out, err := runLogMetricsCmd(t, srv.URL, tt.cmd(), output.FormatTable, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"TRACE ID", "ROOT SERVICE", "t1", "api", "GET /users",
				"2023-11-14 22:13:19", "3s", "2"} {
				if !strings.Contains(out, want) {
					t.Errorf("table output does not contain %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "{") {
				t.Errorf("table output has JSON:\n%s", out)
			}

			srv, _, _ = logMetricsServer(t, testTracesBody)
			out, err = runLogMetricsCmd(t, srv.URL, tt.cmd(), output.FormatJSON, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "future_field") {
				t.Errorf("JSON output dropped an unknown field:\n%s", out)
			}
		})
	}
}
