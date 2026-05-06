package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

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

// TestAddTimeRangeFlagsMs_Defaults verifies that when --start and --end are
// not provided, the closure defaults to -1h and now respectively.
func TestAddTimeRangeFlagsMs_Defaults(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	parseTimeRange := addTimeRangeFlagsMs(cmd)

	// Do not set any flags — simulate the user omitting --start and --end.
	before := time.Now().UnixMilli()
	start, end, err := parseTimeRange()
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// end should be approximately "now"
	if end < before || end > after {
		t.Errorf("end = %d, want in [%d, %d]", end, before, after)
	}

	// start should be approximately 1 hour before now
	expectedStart := before - int64(time.Hour/time.Millisecond)
	tolerance := int64(2000) // 2 seconds tolerance
	if start < expectedStart-tolerance || start > expectedStart+tolerance {
		t.Errorf("start = %d, want ~%d (1h before now)", start, expectedStart)
	}
}

// TestAddTimeRangeFlagsMs_ExplicitOverride verifies that explicit --start and
// --end values override the defaults.
func TestAddTimeRangeFlagsMs_ExplicitOverride(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	parseTimeRange := addTimeRangeFlagsMs(cmd)

	// Simulate setting flags explicitly.
	if err := cmd.Flags().Set("start", "1700000000000"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("end", "1700003600000"); err != nil {
		t.Fatal(err)
	}

	start, end, err := parseTimeRange()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if start != 1700000000000 {
		t.Errorf("start = %d, want 1700000000000", start)
	}
	if end != 1700003600000 {
		t.Errorf("end = %d, want 1700003600000", end)
	}
}

// TestAddTimeRangeFlagsMs_RelativeValues verifies that relative time strings
// work when provided explicitly.
func TestAddTimeRangeFlagsMs_RelativeValues(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	parseTimeRange := addTimeRangeFlagsMs(cmd)

	if err := cmd.Flags().Set("start", "-2h"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("end", "now"); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UnixMilli()
	start, end, err := parseTimeRange()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// end should be approximately now
	tolerance := int64(2000)
	if end < before-tolerance || end > before+tolerance {
		t.Errorf("end = %d, want ~%d", end, before)
	}

	// start should be approximately 2 hours before now
	expectedStart := before - int64(2*time.Hour/time.Millisecond)
	if start < expectedStart-tolerance || start > expectedStart+tolerance {
		t.Errorf("start = %d, want ~%d (2h before now)", start, expectedStart)
	}
}

// TestAddTimeRangeFlagsMs_OnlyStartProvided verifies that when only --start is
// provided, --end defaults to now.
func TestAddTimeRangeFlagsMs_OnlyStartProvided(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	parseTimeRange := addTimeRangeFlagsMs(cmd)

	if err := cmd.Flags().Set("start", "-30m"); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UnixMilli()
	start, end, err := parseTimeRange()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// end should default to now
	tolerance := int64(2000)
	if end < before-tolerance || end > before+tolerance {
		t.Errorf("end = %d, want ~%d (now)", end, before)
	}

	// start should be ~30 minutes ago
	expectedStart := before - int64(30*time.Minute/time.Millisecond)
	if start < expectedStart-tolerance || start > expectedStart+tolerance {
		t.Errorf("start = %d, want ~%d (30m before now)", start, expectedStart)
	}
}

// TestAddTimeRangeFlagsMs_OnlyEndProvided verifies that when only --end is
// provided, --start defaults to -1h.
func TestAddTimeRangeFlagsMs_OnlyEndProvided(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	parseTimeRange := addTimeRangeFlagsMs(cmd)

	if err := cmd.Flags().Set("end", "now"); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UnixMilli()
	start, end, err := parseTimeRange()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// end should be approximately now
	tolerance := int64(2000)
	if end < before-tolerance || end > before+tolerance {
		t.Errorf("end = %d, want ~%d (now)", end, before)
	}

	// start should default to -1h
	expectedStart := before - int64(time.Hour/time.Millisecond)
	if start < expectedStart-tolerance || start > expectedStart+tolerance {
		t.Errorf("start = %d, want ~%d (1h before now)", start, expectedStart)
	}
}
