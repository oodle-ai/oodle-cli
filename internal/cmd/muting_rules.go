package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// mutingRuleListColumns describes the table layout for `muting-rules list`.
var mutingRuleListColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "STARTS AT", Field: "StartsAt"},
	{Header: "ENDS AT", Field: "EndsAt"},
	{Header: "COMMENT", Field: "Comment"},
}

// newMutingRulesCmd builds the `oodle muting-rules` command tree.
func newMutingRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "muting-rules",
		Aliases: []string{"mr", "muting-rule"},
		Short:   "Manage muting rules",
	}

	cmd.AddCommand(newMutingRulesListCmd())
	cmd.AddCommand(newMutingRulesGetCmd())
	cmd.AddCommand(newMutingRulesCreateCmd())
	cmd.AddCommand(newMutingRulesDeleteCmd())

	return cmd
}

func newMutingRulesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List muting rules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListMutingRulesWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, mutingRuleListColumns)
		},
	}
}

func newMutingRulesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a muting rule by ID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetMutingRulesByIdWithResponse(cmd.Context(), instance, args[0])
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
				return output.Print(cmd.OutOrStdout(), format, []client.MutingRule{*resp.JSON200}, mutingRuleListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, mutingRuleListColumns)
		},
	}
}

func newMutingRulesCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a muting rule from a JSON/YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.CreateMutingRulesJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}

			resp, err := c.Inner.CreateMutingRulesWithResponse(cmd.Context(), instance, body)
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
				return output.Print(cmd.OutOrStdout(), format, []client.MutingRule{*resp.JSON200}, mutingRuleListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, mutingRuleListColumns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with muting rule definition")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newMutingRulesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a muting rule",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			id := args[0]
			if !confirmAction("Delete muting rule "+id+"?", forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteMutingRulesByIdWithResponse(cmd.Context(), instance, id)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted muting rule %s\n", id)
			return nil
		},
	}
}
