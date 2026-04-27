package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// notifierListColumns describes the table layout for `notifiers list`.
var notifierListColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "TYPE", Field: "Type"},
}

// newNotifiersCmd builds the `oodle notifiers` command tree.
func newNotifiersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "notifiers",
		Aliases: []string{"notifier"},
		Short:   "Manage notifiers",
	}

	cmd.AddCommand(newNotifiersListCmd())
	cmd.AddCommand(newNotifiersGetCmd())
	cmd.AddCommand(newNotifiersCreateCmd())
	cmd.AddCommand(newNotifiersUpdateCmd())
	cmd.AddCommand(newNotifiersDeleteCmd())

	return cmd
}

func newNotifiersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List notifiers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListNotifiersWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, notifierListColumns)
		},
	}
}

func newNotifiersGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a notifier by ID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetNotifiersByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			if format == output.FormatTable || format == output.FormatCSV {
				return output.Print(cmd.OutOrStdout(), format, []client.Notifier{*resp.JSON200}, notifierListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, notifierListColumns)
		},
	}
}

func newNotifiersCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a notifier from a JSON/YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.CreateNotifiersJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}

			resp, err := c.Inner.CreateNotifiersWithResponse(cmd.Context(), instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			if format == output.FormatTable || format == output.FormatCSV {
				return output.Print(cmd.OutOrStdout(), format, []client.Notifier{*resp.JSON200}, notifierListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, notifierListColumns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with notifier definition")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newNotifiersUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a notifier from a JSON/YAML file",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.UpdateNotifiersByIdJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}

			resp, err := c.Inner.UpdateNotifiersByIdWithResponse(cmd.Context(), instance, args[0], body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			if format == output.FormatTable || format == output.FormatCSV {
				return output.Print(cmd.OutOrStdout(), format, []client.Notifier{*resp.JSON200}, notifierListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, notifierListColumns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with notifier definition")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newNotifiersDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a notifier",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			id := args[0]
			if !confirmAction("Delete notifier "+id+"?", forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteNotifiersByIdWithResponse(cmd.Context(), instance, id)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted notifier %s\n", id)
			return nil
		},
	}
}
