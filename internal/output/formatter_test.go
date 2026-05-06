package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type sampleRow struct {
	ID    string `json:"id" yaml:"id"`
	Name  string `json:"name" yaml:"name"`
	Count int    `json:"count" yaml:"count"`
}

var sampleData = []sampleRow{
	{ID: "a", Name: "Alpha", Count: 1},
	{ID: "b", Name: "Beta", Count: 2},
}

var sampleColumns = []Column{
	{Header: "ID", Field: "ID"},
	{Header: "NAME", Field: "Name"},
	{Header: "COUNT", Field: "Count"},
}

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, FormatJSON, sampleData, sampleColumns); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	var got []sampleRow
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].Count != 2 {
		t.Errorf("unexpected JSON decode: %+v", got)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("JSON output missing trailing newline")
	}
}

func TestPrintYAML(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, FormatYAML, sampleData, sampleColumns); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Alpha") || !strings.Contains(out, "Beta") {
		t.Errorf("YAML missing rows: %s", out)
	}
}

func TestPrintTable(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, FormatTable, sampleData, sampleColumns); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "ID") || !strings.Contains(out, "NAME") || !strings.Contains(out, "COUNT") {
		t.Errorf("table missing headers: %s", out)
	}
	if !strings.Contains(out, "Alpha") || !strings.Contains(out, "Beta") {
		t.Errorf("table missing rows: %s", out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "2") {
		t.Errorf("table missing counts: %s", out)
	}
}

func TestPrintCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, FormatCSV, sampleData, sampleColumns); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("CSV lines = %d, want 3:\n%s", len(lines), out)
	}
	if lines[0] != "ID,NAME,COUNT" {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "a,Alpha,1") {
		t.Errorf("row1 = %q", lines[1])
	}
}

func TestPrintTable_SingleStructWrapped(t *testing.T) {
	var buf bytes.Buffer
	one := sampleRow{ID: "x", Name: "Solo", Count: 9}
	if err := Print(&buf, FormatTable, []sampleRow{one}, sampleColumns); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(buf.String(), "Solo") {
		t.Errorf("missing Solo: %s", buf.String())
	}
}

// TestPrintTable_NilColumnsFallsBackToJSON verifies that when a caller asks
// for table output but provides no columns (e.g. for free-form
// map[string]any responses), Print emits JSON rather than a blank screen.
func TestPrintTable_NilColumnsFallsBackToJSON(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]any{"foo": "bar", "n": 42}
	if err := Print(&buf, FormatTable, data, nil); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"foo"`) || !strings.Contains(out, `"bar"`) {
		t.Errorf("expected JSON fallback, got: %s", out)
	}
}

// TestPrintCSV_NilColumnsFallsBackToJSON mirrors the table fallback for CSV.
func TestPrintCSV_NilColumnsFallsBackToJSON(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]any{"foo": "bar"}
	if err := Print(&buf, FormatCSV, data, nil); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(buf.String(), `"foo"`) {
		t.Errorf("expected JSON fallback, got: %s", buf.String())
	}
}

func TestPrintUnsupportedFormat(t *testing.T) {
	var buf bytes.Buffer
	err := Print(&buf, Format("xml"), sampleData, sampleColumns)
	if err == nil {
		t.Fatal("expected error for xml format")
	}
}

func TestPrintSpecialFormats_ReturnsError(t *testing.T) {
	// FormatGraph and FormatStats must not be handled by Print() — they require
	// special Prometheus parsing done in printQueryResponse. Calling Print()
	// with these formats should return a clear, user-facing error message.
	cases := []struct {
		format  Format
		wantMsg string
	}{
		{FormatGraph, "graph output is only supported for metrics query and query-range commands"},
		{FormatStats, "stats output is only supported for metrics query and query-range commands"},
	}

	for _, tc := range cases {
		t.Run(string(tc.format), func(t *testing.T) {
			var buf bytes.Buffer
			err := Print(&buf, tc.format, sampleData, sampleColumns)
			if err == nil {
				t.Fatalf("expected error when passing %s to Print()", tc.format)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error message should direct user to correct commands, got: %v", err)
			}
			if buf.Len() != 0 {
				t.Errorf("expected no output written when returning error, got %d bytes", buf.Len())
			}
		})
	}
}

func TestDetectFormat_ExplicitFlag(t *testing.T) {
	cases := []struct {
		in   string
		want Format
	}{
		{"json", FormatJSON},
		{"YAML", FormatYAML},
		{"csv", FormatCSV},
		{"table", FormatTable},
		{"graph", FormatGraph},
		{"stats", FormatStats},
	}
	for _, c := range cases {
		if got := DetectFormat(c.in); got != c.want {
			t.Errorf("DetectFormat(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDetectFormat_NoTTYDefaultsJSON(t *testing.T) {
	// Test environment is not a TTY (stdout is captured by `go test`),
	// so DetectFormat("") should pick JSON.
	if got := DetectFormat(""); got != FormatJSON {
		t.Errorf("DetectFormat(\"\") = %q, want %q (non-TTY default)", got, FormatJSON)
	}
}
