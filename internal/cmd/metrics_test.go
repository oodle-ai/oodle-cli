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

// testTimeTolerance is the maximum acceptable drift (in milliseconds) between
// expected and actual timestamps in time-range tests. Kept generous for CI.
const testTimeTolerance = int64(2000)

// TestAddTimeRangeFlagsMs verifies default, explicit, and partial flag
// combinations via a table-driven approach.
func TestAddTimeRangeFlagsMs(t *testing.T) {
	cases := []struct {
		name string
		// flags to set before invoking the closure. nil means "omit".
		startFlag *string
		endFlag   *string
		// For epoch-exact assertions (relative tests leave these zero).
		wantStartExact int64
		wantEndExact   int64
		// For relative assertions: expected offset from "now" for start/end.
		// A zero duration means "expect approximately now".
		startOffset time.Duration
		endOffset   time.Duration
		useRelative bool // when true, assert using offsets instead of exact values
	}{
		{
			name:        "Defaults",
			useRelative: true,
			startOffset: time.Hour,
			endOffset:   0,
		},
		{
			name:           "ExplicitOverride",
			startFlag:      strPtr("1700000000000"),
			endFlag:        strPtr("1700003600000"),
			wantStartExact: 1700000000000,
			wantEndExact:   1700003600000,
		},
		{
			name:        "RelativeValues",
			startFlag:   strPtr("-2h"),
			endFlag:     strPtr("now"),
			useRelative: true,
			startOffset: 2 * time.Hour,
			endOffset:   0,
		},
		{
			name:        "OnlyStartProvided",
			startFlag:   strPtr("-30m"),
			useRelative: true,
			startOffset: 30 * time.Minute,
			endOffset:   0, // end defaults to now
		},
		{
			name:        "OnlyEndProvided",
			endFlag:     strPtr("-30m"),
			useRelative: true,
			startOffset: time.Hour, // start defaults to -1h (relative to now, not to --end)
			endOffset:   30 * time.Minute,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			parseTimeRange := addTimeRangeFlagsMs(cmd)

			if tc.startFlag != nil {
				if err := cmd.Flags().Set("start", *tc.startFlag); err != nil {
					t.Fatal(err)
				}
			}
			if tc.endFlag != nil {
				if err := cmd.Flags().Set("end", *tc.endFlag); err != nil {
					t.Fatal(err)
				}
			}

			before := time.Now().UnixMilli()
			start, end, err := parseTimeRange()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.useRelative {
				expectedEnd := before - int64(tc.endOffset/time.Millisecond)
				if end < expectedEnd-testTimeTolerance || end > expectedEnd+testTimeTolerance {
					t.Errorf("end = %d, want ~%d", end, expectedEnd)
				}
				expectedStart := before - int64(tc.startOffset/time.Millisecond)
				if start < expectedStart-testTimeTolerance || start > expectedStart+testTimeTolerance {
					t.Errorf("start = %d, want ~%d", start, expectedStart)
				}
			} else {
				if start != tc.wantStartExact {
					t.Errorf("start = %d, want %d", start, tc.wantStartExact)
				}
				if end != tc.wantEndExact {
					t.Errorf("end = %d, want %d", end, tc.wantEndExact)
				}
			}
		})
	}
}

// testTimeToleranceSec is the maximum acceptable drift (in seconds) between
// expected and actual timestamps in time-range seconds tests.
const testTimeToleranceSec = float64(2)

// TestAddTimeRangeFlagsSeconds verifies default, explicit, and partial flag
// combinations for the seconds-precision variant used by query-range.
func TestAddTimeRangeFlagsSeconds(t *testing.T) {
	cases := []struct {
		name string
		// flags to set before invoking the closure. nil means "omit".
		startFlag *string
		endFlag   *string
		// For epoch-exact assertions (relative tests leave these zero).
		wantStartExact float64
		wantEndExact   float64
		// For relative assertions: expected offset from "now" for start/end.
		// A zero duration means "expect approximately now".
		startOffset time.Duration
		endOffset   time.Duration
		useRelative bool // when true, assert using offsets instead of exact values
	}{
		{
			name:        "Defaults",
			useRelative: true,
			startOffset: time.Hour,
			endOffset:   0,
		},
		{
			name:           "ExplicitOverride",
			startFlag:      strPtr("1700000000"),
			endFlag:        strPtr("1700003600"),
			wantStartExact: 1700000000,
			wantEndExact:   1700003600,
		},
		{
			name:        "RelativeValues",
			startFlag:   strPtr("-2h"),
			endFlag:     strPtr("now"),
			useRelative: true,
			startOffset: 2 * time.Hour,
			endOffset:   0,
		},
		{
			name:        "OnlyStartProvided",
			startFlag:   strPtr("-30m"),
			useRelative: true,
			startOffset: 30 * time.Minute,
			endOffset:   0, // end defaults to now
		},
		{
			name:        "OnlyEndProvided",
			endFlag:     strPtr("-30m"),
			useRelative: true,
			startOffset: time.Hour, // start defaults to -1h (relative to now, not to --end)
			endOffset:   30 * time.Minute,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			parseTimeRange := addTimeRangeFlagsSeconds(cmd)

			if tc.startFlag != nil {
				if err := cmd.Flags().Set("start", *tc.startFlag); err != nil {
					t.Fatal(err)
				}
			}
			if tc.endFlag != nil {
				if err := cmd.Flags().Set("end", *tc.endFlag); err != nil {
					t.Fatal(err)
				}
			}

			before := float64(time.Now().Unix())
			start, end, err := parseTimeRange()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.useRelative {
				expectedEnd := before - tc.endOffset.Seconds()
				if end < expectedEnd-testTimeToleranceSec || end > expectedEnd+testTimeToleranceSec {
					t.Errorf("end = %v, want ~%v", end, expectedEnd)
				}
				expectedStart := before - tc.startOffset.Seconds()
				if start < expectedStart-testTimeToleranceSec || start > expectedStart+testTimeToleranceSec {
					t.Errorf("start = %v, want ~%v", start, expectedStart)
				}
			} else {
				if start != tc.wantStartExact {
					t.Errorf("start = %v, want %v", start, tc.wantStartExact)
				}
				if end != tc.wantEndExact {
					t.Errorf("end = %v, want %v", end, tc.wantEndExact)
				}
			}
		})
	}
}

// strPtr is a helper that returns a pointer to s.
func strPtr(s string) *string { return &s }
