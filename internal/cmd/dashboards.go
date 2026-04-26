package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newDashboardsCmd returns the `oodle dashboards` command tree.
func newDashboardsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "dashboards",
		Aliases: []string{"dashboard", "dash"},
		Short:   "Manage dashboards",
	}
	cmd.AddCommand(newDashboardsListCmd())
	cmd.AddCommand(newDashboardsGetCmd())
	cmd.AddCommand(newDashboardsCreateCmd())
	cmd.AddCommand(newDashboardsDeleteCmd())
	return cmd
}

func newDashboardsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List dashboards",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListDashboardsWithResponse(cmd.Context(), instance)
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
				{Header: "TITLE", Field: "Title"},
				{Header: "UID", Field: "Uid"},
				{Header: "TYPE", Field: "Type"},
				{Header: "FOLDER", Field: "FolderTitle"},
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, columns)
		},
	}
}

func newDashboardsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <uid>",
		Short: "Get a dashboard by UID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetDashboardsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			// Dashboards are complex nested objects; tables don't make sense
			// here. Override the table format to JSON.
			if format == output.FormatTable {
				format = output.FormatJSON
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
}

func newDashboardsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create or update a dashboard from a JSON or YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.CreateDashboardsJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.CreateDashboardsWithResponse(cmd.Context(), instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			if format == output.FormatTable {
				format = output.FormatJSON
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the dashboard (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newDashboardsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <uid>",
		Short: "Delete a dashboard",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			if !confirmAction(fmt.Sprintf("Delete dashboard %q?", args[0]), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteDashboardsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted dashboard %s\n", args[0])
			return nil
		},
	}
}
