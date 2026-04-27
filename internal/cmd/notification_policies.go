package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// notificationPolicyListColumns describes the table layout for `notification-policies list`.
var notificationPolicyListColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
}

// newNotificationPoliciesCmd builds the `oodle notification-policies` command tree.
func newNotificationPoliciesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "notification-policies",
		Aliases: []string{"np", "notification-policy"},
		Short:   "Manage notification policies",
	}

	cmd.AddCommand(newNotificationPoliciesListCmd())
	cmd.AddCommand(newNotificationPoliciesGetCmd())
	cmd.AddCommand(newNotificationPoliciesCreateCmd())
	cmd.AddCommand(newNotificationPoliciesUpdateCmd())
	cmd.AddCommand(newNotificationPoliciesDeleteCmd())

	return cmd
}

func newNotificationPoliciesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List notification policies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListNotificationPoliciesWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, notificationPolicyListColumns)
		},
	}
}

func newNotificationPoliciesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a notification policy by ID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetNotificationPoliciesByIdWithResponse(cmd.Context(), instance, args[0])
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
				return output.Print(cmd.OutOrStdout(), format, []client.NotificationPolicy{*resp.JSON200}, notificationPolicyListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, notificationPolicyListColumns)
		},
	}
}

func newNotificationPoliciesCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a notification policy from a JSON/YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.CreateNotificationPoliciesJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}

			resp, err := c.Inner.CreateNotificationPoliciesWithResponse(cmd.Context(), instance, body)
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
				return output.Print(cmd.OutOrStdout(), format, []client.NotificationPolicy{*resp.JSON200}, notificationPolicyListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, notificationPolicyListColumns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with notification policy definition")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newNotificationPoliciesUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a notification policy from a JSON/YAML file",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.UpdateNotificationPoliciesByIdJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}

			resp, err := c.Inner.UpdateNotificationPoliciesByIdWithResponse(cmd.Context(), instance, args[0], body)
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
				return output.Print(cmd.OutOrStdout(), format, []client.NotificationPolicy{*resp.JSON200}, notificationPolicyListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, notificationPolicyListColumns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with notification policy definition")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newNotificationPoliciesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a notification policy",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			id := args[0]
			if !confirmAction("Delete notification policy "+id+"?", forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteNotificationPoliciesByIdWithResponse(cmd.Context(), instance, id)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted notification policy %s\n", id)
			return nil
		},
	}
}
