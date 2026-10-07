package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
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

			resp, err := c.Inner.ListSyntheticMonitorsOpWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil || resp.JSON200.Monitors == nil {
				return fmt.Errorf("unexpected empty response")
			}
			// Print the list from the body, so JSON output has every field.
			var envelope struct {
				Monitors json.RawMessage `json:"monitors"`
			}
			if err := json.Unmarshal(resp.Body, &envelope); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			return printBodyOrTable(cmd, envelope.Monitors, *resp.JSON200.Monitors, syntheticMonitorListColumns())
		},
	}
}

func newSyntheticMonitorsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a synthetic monitor by ID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

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
			return printBodyOrTable(cmd, resp.Body, resp.JSON200, syntheticMonitorListColumns())
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

			body, err := readInputFileJSON(file)
			if err != nil {
				return err
			}

			resp, err := c.Inner.CreateSyntheticMonitorsWithBodyWithResponse(cmd.Context(), instance, "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printBodyOrTable(cmd, resp.Body, resp.JSON200, syntheticMonitorListColumns())
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
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			body, err := readInputFileJSON(file)
			if err != nil {
				return err
			}

			resp, err := c.Inner.UpdateSyntheticMonitorsByIdWithBodyWithResponse(cmd.Context(), instance, args[0], "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printBodyOrTable(cmd, resp.Body, resp.JSON200, syntheticMonitorListColumns())
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
		Args:  exactArgs(1),
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
		Args:  exactArgs(1),
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
