package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newLogsCmd returns the `oodle logs` command tree.
func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "logs",
		Aliases: []string{"log"},
		Short:   "Search and query log data",
	}
	cmd.AddCommand(newLogsQueryCmd())
	cmd.AddCommand(newLogsIndexPatternsCmd())
	return cmd
}

// newLogsQueryCmd returns the `oodle logs query` subcommand for searching
// log data using the OpenSearch-compatible multi-search API.
func newLogsQueryCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Search log data using OpenSearch-compatible query DSL",
		Long: `Search log data using the OpenSearch-compatible multi-search API.

The request body uses NDJSON format with two JSON objects separated by a newline:
the first selects the index, the second contains the search query using OpenSearch
Query DSL. Pass the body via -f <file>.

Example NDJSON file contents:
  {"index": "logs-*"}
  {"query": {"match_all": {}}, "size": 10}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("reading %q: %w", file, err)
			}
			// Ensure the body ends with a newline (NDJSON requirement).
			if !bytes.HasSuffix(data, []byte("\n")) {
				data = append(data, '\n')
			}

			params := &client.QueryLogsParams{
				XOODLEINSTANCE: instance,
			}

			// Use the raw client method (not WithResponse) to preserve the
			// full OpenSearch response shape. The generated typed struct
			// only captures a subset of fields and would silently discard
			// per-search errors, _id, _index, _score, _shards, etc.
			httpResp, err := c.Inner.QueryLogsWithBody(
				cmd.Context(),
				params,
				"application/x-ndjson",
				bytes.NewReader(data),
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			return readAndPrintQueryResponse(cmd, format, httpResp)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to NDJSON query file")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// newLogsIndexPatternsCmd returns the `oodle logs index-patterns` subcommand
// for listing available log index patterns.
func newLogsIndexPatternsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index-patterns",
		Short: "List available log index patterns",
		Long:  "Returns the available log index patterns for the authenticated instance. Use the returned title as the index value in logs query requests.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			// TODO(api-spec): The OpenAPI spec omits the X-OODLE-INSTANCE
			// header for the ListLogIndexPatterns endpoint, but the server
			// requires it. Once the spec is fixed and the client is
			// regenerated, remove this manual header injection.
			setInstance := func(ctx context.Context, req *http.Request) error {
				req.Header.Set("X-OODLE-INSTANCE", instance)
				return nil
			}

			resp, err := c.Inner.ListLogIndexPatternsWithResponse(cmd.Context(), setInstance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
	return cmd
}
