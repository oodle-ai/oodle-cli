package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// printQueryResponse handles the common response-checking and output logic
// shared by all query commands. It uses the raw response body for printing
// to avoid deserialization issues with polymorphic Prometheus result types
// (e.g. scalar/string results are a flat tuple, not an array of objects).
func printQueryResponse(cmd *cobra.Command, format output.Format, httpResp *http.Response, body []byte) error {
	if httpResp.StatusCode >= 300 {
		return api.CheckResponse(httpResp, body)
	}
	if len(body) == 0 {
		return fmt.Errorf("unexpected empty response")
	}
	// Parse body into a generic structure so output.Print can render it
	// in any format (JSON, YAML, table, etc.) without being constrained
	// by the generated typed struct.
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	return output.Print(cmd.OutOrStdout(), format, parsed, nil)
}

// readAndPrintQueryResponse reads the body from an HTTP response, closes it,
// and delegates to printQueryResponse. This eliminates duplication of the
// defer/ReadAll/print pattern across raw-client query commands.
func readAndPrintQueryResponse(cmd *cobra.Command, format output.Format, httpResp *http.Response) error {
	defer httpResp.Body.Close()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	return printQueryResponse(cmd, format, httpResp, body)
}

// newMetricsQueryCmd returns the `oodle metrics query` subcommand for
// evaluating a PromQL expression at a single point in time.
func newMetricsQueryCmd() *cobra.Command {
	var (
		query   string
		timeStr string
	)
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Evaluate a PromQL expression at a single point in time",
		Long:  "Evaluate a PromQL expression at a single point in time. Compatible with the Prometheus /api/v1/query endpoint.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			params := &client.QueryMetricsInstantParams{
				Query:         query,
				OODLEINSTANCE: instance,
			}
			if cmd.Flags().Changed("time") {
				t, err := parseTimeFlagSeconds(timeStr)
				if err != nil {
					return fmt.Errorf("--time: %w", err)
				}
				params.Time = &t
			}

			// Use the raw client method (not WithResponse) to avoid the
			// generated JSON parser which fails on scalar/string result
			// types — Prometheus can return data.result as a tuple like
			// [timestamp, "value"] instead of an array of objects.
			httpResp, err := c.Inner.QueryMetricsInstant(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			return readAndPrintQueryResponse(cmd, format, httpResp)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "PromQL expression (e.g. sum(up))")
	cmd.Flags().StringVar(&timeStr, "time", "", "Evaluation timestamp (Unix seconds, 'now', or relative like -1h)")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

// newMetricsQueryRangeCmd returns the `oodle metrics query-range` subcommand
// for evaluating a PromQL expression over a time range.
func newMetricsQueryRangeCmd() *cobra.Command {
	var (
		query           string
		startStr        string
		endStr          string
		step            string
		partialResponse bool
	)
	cmd := &cobra.Command{
		Use:   "query-range",
		Short: "Evaluate a PromQL expression over a time range",
		Long:  "Evaluate a PromQL expression over a time range. Returns a time series of values. Compatible with the Prometheus /api/v1/query_range endpoint.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			start, err := parseTimeFlagSeconds(startStr)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			end, err := parseTimeFlagSeconds(endStr)
			if err != nil {
				return fmt.Errorf("--end: %w", err)
			}

			params := &client.QueryMetricsRangeParams{
				Query:         query,
				Start:         start,
				End:           end,
				Step:          step,
				OODLEINSTANCE: instance,
			}
			if cmd.Flags().Changed("partial-response") {
				params.PartialResponse = &partialResponse
			}

			// Use the raw client method — see comment in newMetricsQueryCmd.
			httpResp, err := c.Inner.QueryMetricsRange(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			return readAndPrintQueryResponse(cmd, format, httpResp)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "PromQL expression (e.g. sum(up))")
	cmd.Flags().StringVar(&startStr, "start", "", "Start timestamp (Unix seconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End timestamp (Unix seconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&step, "step", "", "Query resolution step width (e.g. 60s, 5m)")
	cmd.Flags().BoolVar(&partialResponse, "partial-response", false, "Return partial data if some stores are unavailable")
	_ = cmd.MarkFlagRequired("query")
	_ = cmd.MarkFlagRequired("start")
	_ = cmd.MarkFlagRequired("end")
	_ = cmd.MarkFlagRequired("step")
	return cmd
}
