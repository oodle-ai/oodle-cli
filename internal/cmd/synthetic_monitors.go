package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newSyntheticMonitorsCmd returns the `oodle synthetic-monitors` command tree.
func newSyntheticMonitorsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "synthetic-monitors",
		Aliases: []string{"sm", "synthetics"},
		Short:   "Manage synthetic monitors",
	}
	cmd.AddCommand(newSyntheticMonitorsListCmd())
	cmd.AddCommand(newSyntheticMonitorsGetCmd())
	cmd.AddCommand(newSyntheticMonitorsCreateCmd())
	cmd.AddCommand(newSyntheticMonitorsUpdateCmd())
	cmd.AddCommand(newSyntheticMonitorsDeleteCmd())
	cmd.AddCommand(newSyntheticMonitorsRunCmd())
	return cmd
}

func syntheticMonitorListColumns() []output.Column {
	return []output.Column{
		{Header: "NAME", Field: "Name"},
		{Header: "ID", Field: "Id"},
		{Header: "TYPE", Field: "Type"},
		{Header: "ENABLED", Field: "Enabled"},
		{Header: "INTERVAL", Field: "Interval"},
	}
}

func newSyntheticMonitorsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List synthetic monitors",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListSyntheticMonitorsWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil || resp.JSON200.Monitors == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200.Monitors, syntheticMonitorListColumns())
		},
	}
}

func newSyntheticMonitorsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a synthetic monitor by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetSyntheticMonitorsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, syntheticMonitorListColumns())
		},
	}
}

func newSyntheticMonitorsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a synthetic monitor from a JSON or YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.CreateSyntheticMonitorsJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.CreateSyntheticMonitorsWithResponse(cmd.Context(), instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, syntheticMonitorListColumns())
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the synthetic monitor (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newSyntheticMonitorsUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a synthetic monitor from a JSON or YAML file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.UpdateSyntheticMonitorsByIdJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.UpdateSyntheticMonitorsByIdWithResponse(cmd.Context(), instance, args[0], body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, syntheticMonitorListColumns())
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the updated synthetic monitor (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newSyntheticMonitorsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a synthetic monitor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			if !confirmAction(fmt.Sprintf("Delete synthetic monitor %q?", args[0]), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteSyntheticMonitorsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted synthetic monitor %s\n", args[0])
			return nil
		},
	}
}

func newSyntheticMonitorsRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <id>",
		Short: "Trigger an on-demand run of a synthetic monitor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.CreateRunByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			// Run results are complex; default to JSON unless caller explicitly
			// asked for another format.
			if format == output.FormatTable {
				format = output.FormatJSON
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
}
