package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newApiKeysCmd returns the `oodle api-keys` command tree.
func newApiKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "api-keys",
		Aliases: []string{"ak", "api-key"},
		Short:   "Manage API keys",
	}
	cmd.AddCommand(newApiKeysListCmd())
	cmd.AddCommand(newApiKeysGetCmd())
	cmd.AddCommand(newApiKeysCreateCmd())
	cmd.AddCommand(newApiKeysDeleteCmd())
	return cmd
}

func apiKeyColumns() []output.Column {
	return []output.Column{
		{Header: "NAME", Field: "Name"},
		{Header: "ID", Field: "Id"},
		{Header: "SCOPES", Field: "Scopes"},
		{Header: "EXPIRES_AT", Field: "ExpiresAtEpochMillis"},
		{Header: "LAST_USED_AT", Field: "LastUsedAtEpochMillis"},
		{Header: "PRIMARY", Field: "IsPrimary"},
	}
}

func newApiKeysListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List API keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListApiKeysWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, apiKeyColumns())
		},
	}
}

func newApiKeysGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get an API key by ID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetApiKeysByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, apiKeyColumns())
		},
	}
}

func newApiKeysCreateCmd() *cobra.Command {
	var (
		name       string
		scopesFlag string
		rolesFlag  string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an API key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			scopes := splitAndTrim(scopesFlag)
			body := client.CreateApiKeysJSONRequestBody{
				Name:   name,
				Scopes: &scopes,
			}
			if cmd.Flags().Changed("roles") {
				roles := splitAndTrim(rolesFlag)
				body.Roles = &roles
			}

			resp, err := c.Inner.CreateApiKeysWithResponse(cmd.Context(), instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, apiKeyColumns())
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Name of the API key (required)")
	cmd.Flags().StringVar(&scopesFlag, "scopes", "", "Comma-separated list of scopes (required)")
	cmd.Flags().StringVar(&rolesFlag, "roles", "", "Comma-separated list of roles")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("scopes")
	return cmd
}

func newApiKeysDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an API key",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			if !confirmAction(fmt.Sprintf("Delete API key %q?", args[0]), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteApiKeysByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted API key %s\n", args[0])
				return nil
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
}

// splitAndTrim splits a comma-separated string and trims whitespace from
// each entry. Empty entries are dropped.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
