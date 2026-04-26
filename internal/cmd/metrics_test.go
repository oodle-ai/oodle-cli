package cmd

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

func TestWithTimeRange(t *testing.T) {
	fn := withTimeRange(1000, 2000)
	req, _ := http.NewRequest("GET", "http://example.com/api/metrics/names", nil)
	if err := fn(context.Background(), req); err != nil {
		t.Fatalf("withTimeRange returned error: %v", err)
	}
	q := req.URL.Query()
	if got := q.Get("startTimeEpochMs"); got != "1000" {
		t.Errorf("startTimeEpochMs = %q, want %q", got, "1000")
	}
	if got := q.Get("endTimeEpochMs"); got != "2000" {
		t.Errorf("endTimeEpochMs = %q, want %q", got, "2000")
	}
}

func TestWithTimeRange_PreservesExistingParams(t *testing.T) {
	fn := withTimeRange(100, 200)
	req, _ := http.NewRequest("GET", "http://example.com/api?existing=yes", nil)
	if err := fn(context.Background(), req); err != nil {
		t.Fatalf("withTimeRange returned error: %v", err)
	}
	q := req.URL.Query()
	if got := q.Get("existing"); got != "yes" {
		t.Errorf("existing param lost: got %q, want %q", got, "yes")
	}
	if got := q.Get("startTimeEpochMs"); got != "100" {
		t.Errorf("startTimeEpochMs = %q, want %q", got, "100")
	}
	if got := q.Get("endTimeEpochMs"); got != "200" {
		t.Errorf("endTimeEpochMs = %q, want %q", got, "200")
	}
}

func TestPrintStringSlice_TableNames(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := printStringSlice(cmd, output.FormatTable, []string{"alpha", "beta"}, "Name"); err != nil {
		t.Fatalf("printStringSlice: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Name") {
		t.Errorf("expected header 'Name' in output, got: %q", got)
	}
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Errorf("expected entries in output, got: %q", got)
	}
}

func TestPrintStringSlice_TableLabel(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := printStringSlice(cmd, output.FormatTable, []string{"job"}, "Label"); err != nil {
		t.Fatalf("printStringSlice: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Label") {
		t.Errorf("expected header 'Label' in output, got: %q", got)
	}
	if !strings.Contains(got, "job") {
		t.Errorf("expected 'job' in output, got: %q", got)
	}
}

func TestPrintStringSlice_TableValue(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := printStringSlice(cmd, output.FormatTable, []string{"v1", "v2"}, "Value"); err != nil {
		t.Fatalf("printStringSlice: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Value") {
		t.Errorf("expected header 'Value' in output, got: %q", got)
	}
	for _, want := range []string{"v1", "v2"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got: %q", want, got)
		}
	}
}

func TestPrintStringSlice_JSON(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := printStringSlice(cmd, output.FormatJSON, []string{"a", "b"}, "Name"); err != nil {
		t.Fatalf("printStringSlice: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	// JSON path emits the raw slice (no struct wrapping).
	want := `[
  "a",
  "b"
]`
	if got != want {
		t.Errorf("JSON output mismatch.\n got: %q\nwant: %q", got, want)
	}
}

func TestPrintStringSlice_CSV(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := printStringSlice(cmd, output.FormatCSV, []string{"x", "y"}, "Value"); err != nil {
		t.Fatalf("printStringSlice: %v", err)
	}
	got := buf.String()
	// First row is the header, then one row per entry.
	if !strings.HasPrefix(got, "Value\n") {
		t.Errorf("expected CSV to start with header 'Value', got: %q", got)
	}
	if !strings.Contains(got, "x\n") || !strings.Contains(got, "y\n") {
		t.Errorf("expected entries in CSV output, got: %q", got)
	}
}
