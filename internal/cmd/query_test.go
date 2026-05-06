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

func TestPrintQueryResponse_TableFormatVector(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{StatusCode: 200}
	body := []byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up","job":"prom"},"value":[1700000000,"1"]}]}}`)

	if err := printQueryResponse(cmd, output.FormatTable, resp, body); err != nil {
		t.Fatalf("printQueryResponse: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "METRIC") {
		t.Errorf("expected METRIC header in table output, got: %q", got)
	}
	if !strings.Contains(got, "up{") {
		t.Errorf("expected metric name in table output, got: %q", got)
	}
	if !strings.Contains(got, "1") {
		t.Errorf("expected value '1' in table output, got: %q", got)
	}
}

func TestPrintQueryResponse_TableFormatScalar(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{StatusCode: 200}
	body := []byte(`{"status":"success","data":{"resultType":"scalar","result":[1700000000,"42"]}}`)

	if err := printQueryResponse(cmd, output.FormatTable, resp, body); err != nil {
		t.Fatalf("printQueryResponse: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "TIMESTAMP") {
		t.Errorf("expected TIMESTAMP header, got: %q", got)
	}
	if !strings.Contains(got, "42") {
		t.Errorf("expected value '42', got: %q", got)
	}
}

func TestPrintQueryResponse_TableFormatMatrix(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{StatusCode: 200}
	body := []byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"method":"GET"},"values":[[1700000000,"100"],[1700000060,"105"]]}]}}`)

	if err := printQueryResponse(cmd, output.FormatTable, resp, body); err != nil {
		t.Fatalf("printQueryResponse: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "METRIC") {
		t.Errorf("expected METRIC header, got: %q", got)
	}
	if !strings.Contains(got, "VALUES") {
		t.Errorf("expected VALUES header, got: %q", got)
	}
	if !strings.Contains(got, `method="GET"`) {
		t.Errorf("expected metric labels, got: %q", got)
	}
	if !strings.Contains(got, "100@Nov14") {
		t.Errorf("expected value '100@Nov14', got: %q", got)
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
