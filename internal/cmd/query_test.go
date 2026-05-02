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
