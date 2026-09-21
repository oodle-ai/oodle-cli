package cmd

import (
	"fmt"
	"unicode"

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
			for _, title := range nonASCIITitles(body.Dashboard) {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Warning: title %q has a non-ASCII character; Grafana sends titles as HTTP headers with every panel query and the edge proxy blocks them, so the panel shows no data. Use printable ASCII.\n",
					title)
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

// nonASCIITitles returns the dashboard title and every panel title, rows
// and their collapsed panels included, that hold a character outside
// printable ASCII. Grafana sends these titles as the X-Dashboard-Title
// and X-Panel-Title headers on each panel query; a non-ASCII byte in a
// header makes the edge proxy block the query, while the save succeeds.
func nonASCIITitles(dashboard map[string]interface{}) []string {
	var found []string
	var walk func(obj map[string]interface{})
	walk = func(obj map[string]interface{}) {
		if title, ok := obj["title"].(string); ok && !isPrintableASCII(title) {
			found = append(found, title)
		}
		panels, _ := obj["panels"].([]interface{})
		for _, p := range panels {
			if panel, ok := p.(map[string]interface{}); ok {
				walk(panel)
			}
		}
	}
	if dashboard != nil {
		walk(dashboard)
	}
	return found
}

func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
