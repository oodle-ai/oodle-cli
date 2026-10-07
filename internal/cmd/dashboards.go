package cmd

import (
	"bytes"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
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
			// here, so every format except YAML prints JSON. The body is
			// printed without change, so no field is lost.
			return printResponseBody(cmd, format, resp.Body)
		},
	}
}

func newDashboardsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create or update a dashboard from a JSON or YAML file",
		Long: `Create or update a dashboard from a JSON or YAML file.

The file is a save request, not the output of 'dashboards get':
  {"dashboard": {...}, "folderUid": "<folder>", "overwrite": true}

"dashboard" is Grafana dashboard JSON. Without "folderUid" the dashboard
is saved in the root folder. With "overwrite": true, the dashboard with the
same uid is replaced as a whole: panels that are not in the file are removed.

To change a dashboard:
  oodle dashboards get <uid> -o json > current.json
  jq '{dashboard: .dashboard, folderUid: .meta.folderUid, overwrite: true}' \
    current.json > save.json
  # edit save.json
  oodle dashboards create -f save.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			body, err := readInputFileJSON(file)
			if err != nil {
				return err
			}
			resp, err := c.Inner.CreateDashboardsWithBodyWithResponse(cmd.Context(), instance, "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printResponseBody(cmd, format, resp.Body)
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
