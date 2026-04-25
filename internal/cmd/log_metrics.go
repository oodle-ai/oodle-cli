package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newLogMetricsCmd returns the `oodle log-metrics` command tree.
func newLogMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "log-metrics",
		Aliases: []string{"lm", "logmetrics"},
		Short:   "Manage log-derived metrics",
	}

	cmd.AddCommand(newLogMetricsListCmd())
	cmd.AddCommand(newLogMetricsGetCmd())
	cmd.AddCommand(newLogMetricsCreateCmd())
	cmd.AddCommand(newLogMetricsUpdateCmd())
	cmd.AddCommand(newLogMetricsDeleteCmd())

	return cmd
}

func newLogMetricsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List log metrics rules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListLogmetricsWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			columns := []output.Column{
				{Header: "NAME", Field: "Name"},
				{Header: "ID", Field: "Id"},
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, columns)
		},
	}
}

func newLogMetricsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a log metrics rule by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetLogmetricsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			columns := []output.Column{
				{Header: "NAME", Field: "Name"},
				{Header: "ID", Field: "Id"},
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, columns)
		},
	}
}

func newLogMetricsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a log metrics rule from a JSON or YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.CreateLogmetricsJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.CreateLogmetricsWithResponse(cmd.Context(), instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			columns := []output.Column{
				{Header: "NAME", Field: "Name"},
				{Header: "ID", Field: "Id"},
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, columns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the log metrics rule (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newLogMetricsUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a log metrics rule from a JSON or YAML file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.UpdateLogmetricsByIdJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.UpdateLogmetricsByIdWithResponse(cmd.Context(), instance, args[0], body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			columns := []output.Column{
				{Header: "NAME", Field: "Name"},
				{Header: "ID", Field: "Id"},
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, columns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the updated log metrics rule (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newLogMetricsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a log metrics rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			if !confirmAction(fmt.Sprintf("Delete log metrics rule %q?", args[0]), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteLogmetricsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted log metrics rule %s\n", args[0])
				return nil
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
}
