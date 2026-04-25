package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
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

// newMetricsCmd returns the `oodle metrics` command tree.
func newMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "metrics",
		Aliases: []string{"metric"},
		Short:   "Inspect metrics, labels, and label values",
	}
	cmd.AddCommand(newMetricsNamesCmd())
	cmd.AddCommand(newMetricsLabelsCmd())
	cmd.AddCommand(newMetricsLabelValuesCmd())
	return cmd
}

func newMetricsNamesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "names",
		Short: "List metric names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListNamesWithResponse(cmd.Context(), instance)
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
		},
	}
}

func newMetricsLabelsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "labels <metric_name>",
		Short: "List label names for a metric",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetLabelsByIdWithResponse(cmd.Context(), instance, args[0])
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
		},
	}
}

func newMetricsLabelValuesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "label-values <metric_name> <label_name>",
		Short: "List values for a label of a metric",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetValuesByIdWithResponse(cmd.Context(), instance, args[0], args[1])
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
		},
	}
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
