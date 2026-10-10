package cmd

import (
	"bytes"
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
			return printBodyOrTable(cmd, resp.Body, *resp.JSON200, notifierListColumns)
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
			return printBodyOrTable(cmd, resp.Body, []client.Notifier{*resp.JSON200}, notifierListColumns)
		},
	}
}

func newNotifiersCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a notifier from a JSON/YAML file",
		Long: `Create a notifier from a JSON/YAML file.

Set "name", "type" and the config object that matches the type:
  0 email_config       4 webhook_config
  1 pagerduty_config   5 googlechat_config
  2 slack_config       6 msteamsv2_config
  3 opsgenie_config    7 rootly_config

Run 'oodle notifiers get <id> -o json' on an existing notifier for a
template.`,
		Example: `  oodle notifiers create -f slack.yaml`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			body, err := readInputFileJSON(file)
			if err != nil {
				return err
			}

			resp, err := c.Inner.CreateNotifiersWithBodyWithResponse(cmd.Context(), instance, "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printBodyOrTable(cmd, resp.Body, []client.Notifier{*resp.JSON200}, notifierListColumns)
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
		Long: `Update a notifier from a JSON or YAML file.

The file replaces the notifier. It is sent to the server as it is (YAML is
converted to JSON). To change one field, run 'oodle notifiers get <id> -o json',
edit the output, and give it here.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			body, err := readInputFileJSON(file)
			if err != nil {
				return err
			}

			resp, err := c.Inner.UpdateNotifiersByIdWithBodyWithResponse(cmd.Context(), instance, args[0], "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printBodyOrTable(cmd, resp.Body, []client.Notifier{*resp.JSON200}, notifierListColumns)
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
