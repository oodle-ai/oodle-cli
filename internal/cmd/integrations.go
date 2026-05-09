package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/config"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newIntegrationsCmd returns the `oodle integrations` command tree.
func newIntegrationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "integrations",
		Aliases: []string{"integration", "integ"},
		Short:   "Manage integrations",
	}
	cmd.AddCommand(newIntegrationsListCmd())
	cmd.AddCommand(newIntegrationsGetSetupSpecCmd())
	return cmd
}

func newIntegrationsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List integrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListIntegrations(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("reading response: %w", err)
			}
			if resp.StatusCode >= 300 {
				return api.CheckResponse(resp, body)
			}

			// The response is a JSON array of dynamic objects. Parse into
			// []map[string]interface{} so output.Print can render table
			// columns from map keys.
			var items []map[string]interface{}
			if err := json.Unmarshal(body, &items); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			return output.Print(cmd.OutOrStdout(), format, items, integrationColumns())
		},
	}
}

func integrationColumns() []output.Column {
	return []output.Column{
		{Header: "NAME", Field: "name"},
		{Header: "TYPE", Field: "type"},
		{Header: "STATUS", Field: "status"},
		{Header: "CATEGORIES", Field: "categories"},
	}
}

func newIntegrationsGetSetupSpecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-setup-spec <integration-type>",
		Short: "Get the setup specification for an integration type",
		Long: `Returns the structured setup specification for the given integration type.

Contains requirements, parameters, setup methods with step-by-step instructions,
config templates, and validation hints.

This endpoint does not require authentication.`,
		Args: exactArgs(1),
		Annotations: map[string]string{
			skipConfigAnnotation: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			format := getOutputFormat(cmd)

			// When running without auth/config (the normal case for this
			// unauthenticated endpoint), build a minimal client from
			// --api-url / env / default URL.
			if c == nil {
				var err error
				c, err = newUnauthenticatedClient(cmd)
				if err != nil {
					return err
				}
			}

			resp, err := c.Inner.GetIntegrationSetupSpec(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("reading response: %w", err)
			}
			if resp.StatusCode >= 300 {
				return api.CheckResponse(resp, body)
			}

			// The response is a dynamic JSON object. Parse into
			// map[string]interface{} for flexible output.
			var spec map[string]interface{}
			if err := json.Unmarshal(body, &spec); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			return output.Print(cmd.OutOrStdout(), format, spec, nil)
		},
	}
}

// newUnauthenticatedClient builds a minimal API client that does not require
// authentication credentials. It resolves the API URL from the --api-url flag,
// environment variables (OODLE_API_URL, OODLE_DEPLOYMENT, OODLE_URL), or the
// default. This is used by endpoints marked security: [] in the OpenAPI spec.
func newUnauthenticatedClient(cmd *cobra.Command) (*api.Client, error) {
	apiURL, _ := cmd.Flags().GetString("api-url")
	if apiURL == "" {
		apiURL = config.ResolveAPIURL()
	}
	gen, err := client.NewClientWithResponses(apiURL)
	if err != nil {
		return nil, fmt.Errorf("creating API client: %w", err)
	}
	return &api.Client{Inner: gen}, nil
}
