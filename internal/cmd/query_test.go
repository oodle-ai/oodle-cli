package cmd

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

func TestPrintQueryResponse_Success(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{
		StatusCode: 200,
	}
	body := []byte(`{"status":"success","data":{"resultType":"scalar","result":[1700000000,"42"]}}`)

	if err := printQueryResponse(cmd, output.FormatJSON, resp, body); err != nil {
		t.Fatalf("printQueryResponse: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "success") {
		t.Errorf("expected 'success' in output, got: %q", got)
	}
	if !strings.Contains(got, "42") {
		t.Errorf("expected '42' in output, got: %q", got)
	}
}

func TestPrintQueryResponse_ErrorStatus(t *testing.T) {
	resp := &http.Response{
		StatusCode: 400,
	}
	body := []byte(`{"message":"bad request"}`)

	cmd := &cobra.Command{}
	err := printQueryResponse(cmd, output.FormatJSON, resp, body)
	if err == nil {
		t.Fatal("expected error for 400 status")
	}
}

func TestPrintQueryResponse_EmptyBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
	}

	cmd := &cobra.Command{}
	err := printQueryResponse(cmd, output.FormatJSON, resp, nil)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error, got: %v", err)
	}
}

func TestReadAndPrintQueryResponse_Success(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	body := `{"result":"ok"}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	if err := readAndPrintQueryResponse(cmd, output.FormatJSON, resp); err != nil {
		t.Fatalf("readAndPrintQueryResponse: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "ok") {
		t.Errorf("expected 'ok' in output, got: %q", got)
	}
}

func TestReadAndPrintQueryResponse_ErrorStatus(t *testing.T) {
	body := `{"message":"not found"}`
	resp := &http.Response{
		StatusCode: 404,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	cmd := &cobra.Command{}
	err := readAndPrintQueryResponse(cmd, output.FormatJSON, resp)
	if err == nil {
		t.Fatal("expected error for 404 status")
	}
}

func TestPrintQueryResponse_TableFormatUsesPromQL(t *testing.T) {
	// Verify the integration plumbing: table format for Prometheus responses
	// uses the PromQL formatter (producing table headers) instead of raw JSON.
	// Detailed output assertions are in output/promql_test.go.
	tests := []struct {
		name string
		body string
	}{
		{
			name: "vector",
			body: `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up","job":"prom"},"value":[1700000000,"1"]}]}}`,
		},
		{
			name: "scalar",
			body: `{"status":"success","data":{"resultType":"scalar","result":[1700000000,"42"]}}`,
		},
		{
			name: "matrix",
			body: `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"method":"GET"},"values":[[1700000000,"100"]]}]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&buf)

			resp := &http.Response{StatusCode: 200}
			if err := printQueryResponse(cmd, output.FormatTable, resp, []byte(tt.body)); err != nil {
				t.Fatalf("printQueryResponse: %v", err)
			}
			got := buf.String()
			// Table output should NOT contain raw JSON structure markers
			if strings.Contains(got, `"resultType"`) {
				t.Errorf("table format should not produce raw JSON, got: %q", got)
			}
			// Should contain table-style output (column headers)
			if !strings.Contains(got, "METRIC") && !strings.Contains(got, "TIMESTAMP") {
				t.Errorf("expected table column headers, got: %q", got)
			}
		})
	}
}

func TestPrintQueryResponse_JSONFormatBypassesPromQL(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{StatusCode: 200}
	body := []byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"test"},"value":[1700000000,"1"]}]}}`)

	if err := printQueryResponse(cmd, output.FormatJSON, resp, body); err != nil {
		t.Fatalf("printQueryResponse: %v", err)
	}
	got := buf.String()
	// JSON format should NOT use the table formatter
	if strings.Contains(got, "METRIC") {
		t.Errorf("JSON format should not produce table headers, got: %q", got)
	}
	// Should contain raw JSON structure
	if !strings.Contains(got, `"resultType"`) {
		t.Errorf("expected raw JSON output, got: %q", got)
	}
}

func TestPrintQueryResponse_TableNonPromFallsBackToJSON(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{StatusCode: 200}
	body := []byte(`{"items":[{"name":"foo"}]}`)

	if err := printQueryResponse(cmd, output.FormatTable, resp, body); err != nil {
		t.Fatalf("printQueryResponse: %v", err)
	}
	got := buf.String()
	// Non-Prometheus response with table format should fall back to JSON
	if !strings.Contains(got, `"items"`) {
		t.Errorf("expected JSON fallback for non-Prom response, got: %q", got)
	}
}
