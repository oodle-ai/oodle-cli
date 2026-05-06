package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// nameEntry wraps a string value so output.Print can render it as a single
// column for table/CSV output. JSON/YAML output is unaffected because we
// pass the original *[]string in those code paths.
type nameEntry struct {
	Name string
}

type labelEntry struct {
	Label string
}

type valueEntry struct {
	Value string
}

// addTimeRangeFlagsMs registers --start/--end flags on cmd and returns a
// closure the RunE can invoke to parse them as epoch milliseconds. When
// omitted, --start defaults to "-1h" (one hour ago) and --end defaults to
// "now", providing a sensible recent window for interactive exploration.
// All three metrics subcommands share the exact same flag wiring, so this
// helper keeps them in lockstep.
func addTimeRangeFlagsMs(cmd *cobra.Command) func() (start, end int64, err error) {
	var startStr, endStr string
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch milliseconds, 'now', or relative like -1h). Defaults to -1h if omitted")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch milliseconds, 'now', or relative like -1h). Defaults to now if omitted")
	return func() (int64, int64, error) {
		if startStr == "" {
			startStr = "-1h"
		}
		if endStr == "" {
			endStr = "now"
		}
		start, err := parseTimeFlagMs(startStr)
		if err != nil {
			return 0, 0, fmt.Errorf("--start: %w", err)
		}
		end, err := parseTimeFlagMs(endStr)
		if err != nil {
			return 0, 0, fmt.Errorf("--end: %w", err)
		}
		return start, end, nil
	}
}

// newMetricsCmd returns the `oodle metrics` command tree.
func newMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "metrics",
		Aliases: []string{"metric"},
		Short:   "Query metrics and inspect labels and label values",
	}
	cmd.AddCommand(newMetricsNamesCmd())
	cmd.AddCommand(newMetricsLabelsCmd())
	cmd.AddCommand(newMetricsLabelValuesCmd())
	cmd.AddCommand(newMetricsQueryCmd())
	cmd.AddCommand(newMetricsQueryRangeCmd())
	return cmd
}

func newMetricsNamesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "names",
		Short: "List metric names",
		Args:  cobra.NoArgs,
	}
	parseTimeRange := addTimeRangeFlagsMs(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c := getClient(cmd)
		instance := getInstance(cmd)
		format := getOutputFormat(cmd)

		start, end, err := parseTimeRange()
		if err != nil {
			return err
		}

		resp, err := c.Inner.ListNamesWithResponse(cmd.Context(), instance, &client.ListNamesParams{
			StartTimeEpochMs: start,
			EndTimeEpochMs:   end,
		})
		if err != nil {
			return fmt.Errorf("API request failed: %w", err)
		}
		if resp.StatusCode() >= 300 {
			return api.CheckResponse(resp.HTTPResponse, resp.Body)
		}
		if resp.JSON200 == nil {
			return fmt.Errorf("unexpected empty response")
		}
		return printStringSlice(cmd, format, *resp.JSON200, "Name")
	}
	return cmd
}

func newMetricsLabelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "labels <metric_name>",
		Short: "List label names for a metric",
		Args:  exactArgs(1),
	}
	parseTimeRange := addTimeRangeFlagsMs(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c := getClient(cmd)
		instance := getInstance(cmd)
		format := getOutputFormat(cmd)

		start, end, err := parseTimeRange()
		if err != nil {
			return err
		}

		resp, err := c.Inner.GetLabelsByIdWithResponse(cmd.Context(), instance, args[0], &client.GetLabelsByIdParams{
			StartTimeEpochMs: start,
			EndTimeEpochMs:   end,
		})
		if err != nil {
			return fmt.Errorf("API request failed: %w", err)
		}
		if resp.StatusCode() >= 300 {
			return api.CheckResponse(resp.HTTPResponse, resp.Body)
		}
		if resp.JSON200 == nil {
			return fmt.Errorf("unexpected empty response")
		}
		return printStringSlice(cmd, format, *resp.JSON200, "Label")
	}
	return cmd
}

func newMetricsLabelValuesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label-values <metric_name> <label_name>",
		Short: "List values for a label of a metric",
		Args:  exactArgs(2),
	}
	parseTimeRange := addTimeRangeFlagsMs(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c := getClient(cmd)
		instance := getInstance(cmd)
		format := getOutputFormat(cmd)

		start, end, err := parseTimeRange()
		if err != nil {
			return err
		}

		resp, err := c.Inner.GetValuesByIdWithResponse(cmd.Context(), instance, args[0], args[1], &client.GetValuesByIdParams{
			StartTimeEpochMs: start,
			EndTimeEpochMs:   end,
		})
		if err != nil {
			return fmt.Errorf("API request failed: %w", err)
		}
		if resp.StatusCode() >= 300 {
			return api.CheckResponse(resp.HTTPResponse, resp.Body)
		}
		if resp.JSON200 == nil {
			return fmt.Errorf("unexpected empty response")
		}
		return printStringSlice(cmd, format, *resp.JSON200, "Value")
	}
	return cmd
}

// printStringSlice renders a []string in the desired format. For JSON/YAML
// the raw slice is emitted; for table/CSV we wrap each entry in a struct so
// the formatter can render a single column with the given header.
func printStringSlice(cmd *cobra.Command, format output.Format, items []string, header string) error {
	switch format {
	case output.FormatTable, output.FormatCSV:
		columns := []output.Column{{Header: header, Field: header}}
		switch header {
		case "Name":
			rows := make([]nameEntry, len(items))
			for i, s := range items {
				rows[i] = nameEntry{Name: s}
			}
			return output.Print(cmd.OutOrStdout(), format, rows, columns)
		case "Label":
			rows := make([]labelEntry, len(items))
			for i, s := range items {
				rows[i] = labelEntry{Label: s}
			}
			return output.Print(cmd.OutOrStdout(), format, rows, columns)
		default:
			rows := make([]valueEntry, len(items))
			for i, s := range items {
				rows[i] = valueEntry{Value: s}
			}
			return output.Print(cmd.OutOrStdout(), format, rows, columns)
		}
	default:
		return output.Print(cmd.OutOrStdout(), format, items, nil)
	}
}
