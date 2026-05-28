package cmd

import (
	"bytes"
	"context"
	"encoding/json"
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
	var startStr, endStr string
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Search log data using OpenSearch-compatible query DSL",
		Long: `Search log data using the OpenSearch-compatible multi-search API.

The request body uses NDJSON format with two JSON objects separated by a newline:
the first selects the index, the second contains the search query using OpenSearch
Query DSL. Pass the body via -f <file>.

When --start and --end are provided (or defaulted), a range filter on timestamp
is injected into the query body automatically. If the query already contains a
bool filter, the range clause is appended to the existing filter array.

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

			// Track whether the user explicitly provided --start/--end.
			startExplicit := startStr != ""
			endExplicit := endStr != ""

			// If the query body already contains a timestamp range and
			// the user did not explicitly provide --start/--end, honour
			// the range from the query and skip injection.
			if !startExplicit && !endExplicit && queryContainsTimestampRange(data) {
				// Use the query's own timestamp range as-is.
			} else {
				// Apply default time range values.
				if startStr == "" {
					startStr = defaultStartOffset
				}
				if endStr == "" {
					endStr = defaultEndValue
				}

				startMs, err := parseTimeFlagMs(startStr)
				if err != nil {
					return fmt.Errorf("--start: %w", err)
				}
				endMs, err := parseTimeFlagMs(endStr)
				if err != nil {
					return fmt.Errorf("--end: %w", err)
				}

				data, err = injectTimeRange(data, startMs, endMs)
				if err != nil {
					return fmt.Errorf("injecting time range: %w", err)
				}
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
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch milliseconds, 'now', or relative like -1h). Defaults to "+defaultStartOffset+" if omitted")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch milliseconds, 'now', or relative like -1h). Defaults to "+defaultEndValue+" if omitted")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// injectTimeRange parses the NDJSON body (header + search lines) and injects
// a range filter on timestamp into the search query. If the query is already
// wrapped in a bool query with a filter clause, the range is appended;
// otherwise the original query is wrapped in a bool/must+filter structure.
func injectTimeRange(data []byte, startMs, endMs int64) ([]byte, error) {
	lines := splitNDJSON(data)
	if len(lines) < 2 {
		return nil, fmt.Errorf("expected at least 2 NDJSON lines (header + search), got %d", len(lines))
	}

	// Parse the search body (second line).
	var search map[string]any
	if err := json.Unmarshal(lines[1], &search); err != nil {
		return nil, fmt.Errorf("parsing search body: %w", err)
	}

	// Build the range filter clause.
	rangeFilter := map[string]any{
		"range": map[string]any{
			"timestamp": map[string]any{
				"gte":    startMs,
				"lte":    endMs,
				"format": "epoch_millis",
			},
		},
	}

	// Inject into the query.
	query, _ := search["query"].(map[string]any)
	if query == nil {
		query = map[string]any{"match_all": map[string]any{}}
	}

	// Check if there's already a bool query with a filter.
	if boolQ, ok := query["bool"].(map[string]any); ok {
		// Normalize existing filter (can be nil, a single object, or an array)
		// and append the range clause.
		switch f := boolQ["filter"].(type) {
		case nil:
			boolQ["filter"] = []any{rangeFilter}
		case []any:
			boolQ["filter"] = append(f, rangeFilter)
		default:
			// Single filter object — wrap into an array alongside the range.
			boolQ["filter"] = []any{f, rangeFilter}
		}
	} else {
		// Wrap the original query in a bool/must+filter.
		search["query"] = map[string]any{
			"bool": map[string]any{
				"must":   []any{query},
				"filter": []any{rangeFilter},
			},
		}
	}

	// Re-serialize.
	searchBytes, err := json.Marshal(search)
	if err != nil {
		return nil, fmt.Errorf("serializing search body: %w", err)
	}

	// Reconstruct NDJSON: header + modified search + any remaining lines.
	var result []byte
	result = append(result, lines[0]...)
	result = append(result, '\n')
	result = append(result, searchBytes...)
	result = append(result, '\n')
	// Additional header+search pairs (if any) are passed through unchanged;
	// time range is only injected into the first search body.
	for i := 2; i < len(lines); i++ {
		result = append(result, lines[i]...)
		result = append(result, '\n')
	}
	return result, nil
}

// splitNDJSON splits NDJSON data into individual JSON lines, skipping empty lines.
func splitNDJSON(data []byte) [][]byte {
	var lines [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// queryContainsTimestampRange checks whether the NDJSON search body (second
// line) contains a range filter referencing "timestamp". It returns false on
// any parse error so the warning is silently skipped.
func queryContainsTimestampRange(data []byte) bool {
	lines := splitNDJSON(data)
	if len(lines) < 2 {
		return false
	}
	var body any
	if err := json.Unmarshal(lines[1], &body); err != nil {
		return false
	}
	return containsTimestampRange(body)
}

// containsTimestampRange recursively walks a JSON-decoded value looking for
// a map with key "range" whose value is a map containing key "timestamp".
func containsTimestampRange(v any) bool {
	switch val := v.(type) {
	case map[string]any:
		// Direct check: is this a {"range": {"timestamp": ...}} node?
		if rangeVal, ok := val["range"]; ok {
			if rangeMap, ok := rangeVal.(map[string]any); ok {
				if _, ok := rangeMap["timestamp"]; ok {
					return true
				}
			}
		}
		// Recurse into all values.
		for _, child := range val {
			if containsTimestampRange(child) {
				return true
			}
		}
	case []any:
		for _, child := range val {
			if containsTimestampRange(child) {
				return true
			}
		}
	}
	return false
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
