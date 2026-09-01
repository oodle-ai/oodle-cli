package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newTracesCmd returns the `oodle traces` command tree.
func newTracesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "traces",
		Aliases: []string{"trace"},
		Short:   "Query traces, trace labels, and label values",
	}
	cmd.AddCommand(newTracesListCmd())
	cmd.AddCommand(newTracesGetCmd())
	cmd.AddCommand(newTracesLabelsCmd())
	cmd.AddCommand(newTracesLabelValuesCmd())
	return cmd
}

func newTracesListCmd() *cobra.Command {
	var (
		startStr    string
		endStr      string
		service     string
		operation   string
		minDuration string
		maxDuration string
		tags        string
		search      string
		limit       int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List traces in a time range",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			start, err := parseTimeFlag(startStr)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			end, err := parseTimeFlag(endStr)
			if err != nil {
				return fmt.Errorf("--end: %w", err)
			}

			params := &client.ListTracesParams{
				Start: start,
				End:   end,
			}
			if cmd.Flags().Changed("service") {
				v := service
				params.Service = &v
			}
			if cmd.Flags().Changed("operation") {
				v := operation
				params.Operation = &v
			}
			if cmd.Flags().Changed("min-duration") {
				v := minDuration
				params.MinDuration = &v
			}
			if cmd.Flags().Changed("max-duration") {
				v := maxDuration
				params.MaxDuration = &v
			}
			if cmd.Flags().Changed("tags") {
				v := tags
				params.Tags = &v
			}
			if cmd.Flags().Changed("search") {
				v := search
				params.Search = &v
			}
			if cmd.Flags().Changed("limit") {
				v := limit
				params.Limit = &v
			}

			resp, err := c.Inner.ListTracesWithResponse(cmd.Context(), instance, params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			// The response is map[string]interface{}; render as JSON/YAML for
			// structured formats and dump key/value pairs for tabular output.
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&service, "service", "", "Filter by service name")
	cmd.Flags().StringVar(&operation, "operation", "", "Filter by operation name")
	cmd.Flags().StringVar(&minDuration, "min-duration", "", "Minimum trace duration (e.g. 100ms, 1s)")
	cmd.Flags().StringVar(&maxDuration, "max-duration", "", "Maximum trace duration (e.g. 5s, 10s)")
	cmd.Flags().StringVar(&tags, "tags", "", `JSON-encoded map of tag filters (e.g. {"http.method":"GET"})`)
	cmd.Flags().StringVar(&search, "search", "", "Free-text search across trace data")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of traces to return")
	_ = cmd.MarkFlagRequired("start")
	_ = cmd.MarkFlagRequired("end")
	return cmd
}

func newTracesGetCmd() *cobra.Command {
	var (
		startStr string
		endStr   string
	)
	cmd := &cobra.Command{
		Use:   "get <trace_id>",
		Short: "Get a trace by ID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			start, err := parseTimeFlag(startStr)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			end, err := parseTimeFlag(endStr)
			if err != nil {
				return fmt.Errorf("--end: %w", err)
			}

			params := &client.GetTracesByIdParams{
				Start: start,
				End:   end,
			}
			resp, err := c.Inner.GetTracesByIdWithResponse(cmd.Context(), instance, args[0], params)
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
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch microseconds, 'now', or relative like -1h)")
	_ = cmd.MarkFlagRequired("start")
	_ = cmd.MarkFlagRequired("end")
	return cmd
}

func newTracesLabelsCmd() *cobra.Command {
	var (
		startStr string
		endStr   string
	)
	cmd := &cobra.Command{
		Use:   "labels",
		Short: "List trace label names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			params := &client.ListLabelsParams{}
			if cmd.Flags().Changed("start") {
				start, err := parseTimeFlag(startStr)
				if err != nil {
					return fmt.Errorf("--start: %w", err)
				}
				params.Start = &start
			}
			if cmd.Flags().Changed("end") {
				end, err := parseTimeFlag(endStr)
				if err != nil {
					return fmt.Errorf("--end: %w", err)
				}
				params.End = &end
			}

			resp, err := c.Inner.ListLabelsWithResponse(cmd.Context(), instance, params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil || resp.JSON200.Data == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printStringSlice(cmd, format, *resp.JSON200.Data, "Label")
		},
	}
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch microseconds, 'now', or relative like -1h)")
	return cmd
}

func newTracesLabelValuesCmd() *cobra.Command {
	var (
		startStr string
		endStr   string
	)
	cmd := &cobra.Command{
		Use:   "label-values <label_name>",
		Short: "List values for a trace label",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			params := &client.GetTraceLabelValuesByIdParams{}
			if cmd.Flags().Changed("start") {
				start, err := parseTimeFlag(startStr)
				if err != nil {
					return fmt.Errorf("--start: %w", err)
				}
				params.Start = &start
			}
			if cmd.Flags().Changed("end") {
				end, err := parseTimeFlag(endStr)
				if err != nil {
					return fmt.Errorf("--end: %w", err)
				}
				params.End = &end
			}

			resp, err := c.Inner.GetTraceLabelValuesByIdWithResponse(cmd.Context(), instance, args[0], params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil || resp.JSON200.Data == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printStringSlice(cmd, format, *resp.JSON200.Data, "Value")
		},
	}
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch microseconds, 'now', or relative like -1h)")
	return cmd
}
