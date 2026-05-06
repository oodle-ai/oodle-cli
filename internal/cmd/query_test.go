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

func TestPrintQueryResponse_GraphMatrix(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{StatusCode: 200}
	body := []byte(`{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [{
				"metric": {"job": "prometheus"},
				"values": [
					[1700000000, "1"],
					[1700000060, "2"],
					[1700000120, "3"],
					[1700000180, "2.5"],
					[1700000240, "1.5"]
				]
			}]
		}
	}`)

	if err := printQueryResponse(cmd, output.FormatGraph, resp, body); err != nil {
		t.Fatalf("printQueryResponse graph: %v", err)
	}
	got := buf.String()
	if got == "" {
		t.Fatal("expected non-empty graph output")
	}
	// The legend should contain the job label.
	if !strings.Contains(got, "prometheus") {
		t.Errorf("expected 'prometheus' in graph legend, got:\n%s", got)
	}
}

func TestPrintQueryResponse_GraphScalarError(t *testing.T) {
	cmd := &cobra.Command{}
	resp := &http.Response{StatusCode: 200}
	body := []byte(`{"status":"success","data":{"resultType":"scalar","result":[1700000000,"42"]}}`)

	err := printQueryResponse(cmd, output.FormatGraph, resp, body)
	if err == nil {
		t.Fatal("expected error for scalar result type with graph output")
	}
	if !strings.Contains(err.Error(), "scalar") {
		t.Errorf("error should mention 'scalar', got: %v", err)
	}
}

func TestPrintQueryResponse_GraphVector(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{StatusCode: 200}
	body := []byte(`{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [{
				"metric": {"instance": "localhost:9090"},
				"value": [1700000000, "42"]
			}]
		}
	}`)

	if err := printQueryResponse(cmd, output.FormatGraph, resp, body); err != nil {
		t.Fatalf("printQueryResponse graph vector: %v", err)
	}
	got := buf.String()
	if got == "" {
		t.Fatal("expected non-empty graph output")
	}
	// The legend should contain the instance label.
	if !strings.Contains(got, "localhost") {
		t.Errorf("expected 'localhost' in graph legend, got:\n%s", got)
	}
}

func TestPrintQueryResponse_StatsMatrix(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	resp := &http.Response{StatusCode: 200}
	body := []byte(`{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [{
				"metric": {"job": "prometheus"},
				"values": [
					[1700000000, "10"],
					[1700000060, "20"],
					[1700000120, "30"]
				]
			}]
		}
	}`)

	if err := printQueryResponse(cmd, output.FormatStats, resp, body); err != nil {
		t.Fatalf("printQueryResponse stats: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "prometheus") {
		t.Errorf("expected 'prometheus' in stats output, got:\n%s", got)
	}
	if !strings.Contains(got, "MIN") {
		t.Errorf("expected 'MIN' header in stats output, got:\n%s", got)
	}
	if !strings.Contains(got, "1 series") {
		t.Errorf("expected '1 series' in stats footer, got:\n%s", got)
	}
}

func TestPrintQueryResponse_StatsEmptyResult(t *testing.T) {
	// A Prometheus query returning status: "success" with an empty result set
	// should not produce an error under -o stats. It should render a successful
	// empty summary.
	cases := []struct {
		name string
		body string
	}{
		{
			name: "empty matrix",
			body: `{"status":"success","data":{"resultType":"matrix","result":[]}}`,
		},
		{
			name: "empty vector",
			body: `{"status":"success","data":{"resultType":"vector","result":[]}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&buf)

			resp := &http.Response{StatusCode: 200}
			err := printQueryResponse(cmd, output.FormatStats, resp, []byte(tc.body))
			if err != nil {
				t.Fatalf("printQueryResponse stats with empty result should not error, got: %v", err)
			}
			got := buf.String()
			if !strings.Contains(got, "0 series, 0 total samples") {
				t.Errorf("expected '0 series, 0 total samples' in output, got:\n%s", got)
			}
		})
	}
}

func TestPrintQueryResponse_StatsScalarError(t *testing.T) {
	cmd := &cobra.Command{}
	resp := &http.Response{StatusCode: 200}
	body := []byte(`{"status":"success","data":{"resultType":"scalar","result":[1700000000,"42"]}}`)

	err := printQueryResponse(cmd, output.FormatStats, resp, body)
	if err == nil {
		t.Fatal("expected error for scalar result type with stats output")
	}
	if !strings.Contains(err.Error(), "scalar") {
		t.Errorf("error should mention 'scalar', got: %v", err)
	}
	// The error message should NOT say "graph output" — it should be format-neutral.
	if strings.Contains(err.Error(), "graph output") {
		t.Errorf("error message should not say 'graph output' when using stats format, got: %v", err)
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

func TestNewMetricsQueryRangeCmd_FlagConfiguration(t *testing.T) {
	cmd := newMetricsQueryRangeCmd()

	t.Run("start_end_step_are_optional", func(t *testing.T) {
		// Verify that --start, --end, and --step are no longer required and have
		// sensible defaults applied when omitted.
		for _, name := range []string{"start", "end", "step"} {
			f := cmd.Flags().Lookup(name)
			if f == nil {
				t.Fatalf("expected flag %q to exist", name)
			}
			// NOTE: cobra.BashCompOneRequiredFlag is an internal annotation key
			// cobra uses to track required flags. This works as of cobra v1.x but
			// could change in a future major version.
			if _, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok {
				t.Errorf("flag %q should not be required", name)
			}
		}

		// Verify the help text mentions defaults.
		startFlag := cmd.Flags().Lookup("start")
		if !strings.Contains(startFlag.Usage, defaultStartOffset) {
			t.Errorf("--start usage should mention default %q, got: %q", defaultStartOffset, startFlag.Usage)
		}
		endFlag := cmd.Flags().Lookup("end")
		if !strings.Contains(endFlag.Usage, defaultEndValue) {
			t.Errorf("--end usage should mention default %q, got: %q", defaultEndValue, endFlag.Usage)
		}
		stepFlag := cmd.Flags().Lookup("step")
		if !strings.Contains(stepFlag.Usage, defaultStep) {
			t.Errorf("--step usage should mention default %q, got: %q", defaultStep, stepFlag.Usage)
		}
	})

	t.Run("query_is_required", func(t *testing.T) {
		// --query must remain required.
		f := cmd.Flags().Lookup("query")
		if f == nil {
			t.Fatal("expected flag 'query' to exist")
		}
		// NOTE: cobra.BashCompOneRequiredFlag is an internal annotation key;
		// see comment in the sibling subtest.
		if ann, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; !ok || len(ann) == 0 {
			t.Error("flag 'query' should be required")
		}
	})
}
