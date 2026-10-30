package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
	cmd.AddCommand(newIntegrationsListSetupSpecsCmd())
	cmd.AddCommand(newAwsCmd())
	cmd.AddCommand(newAzureCmd())
	cmd.AddCommand(newS3LogsCmd())
	cmd.AddCommand(newIntegrationsImportAssetsCmd())
	cmd.AddCommand(newIntegrationsRemoveAssetsCmd())
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
	var (
		method  string
		summary bool
	)
	cmd := &cobra.Command{
		Use:   "get-setup-spec <integration-type>",
		Short: "Get the setup specification for an integration type",
		Long: `Returns the structured setup specification for the given integration type.

Contains requirements, parameters, setup methods with step-by-step instructions,
config templates, and validation hints.

This endpoint does not require authentication. When you are logged in, the
command fills in the values of your instance: {{instanceId}}, {{apiUrl}},
{{collectorDomain}}, {{logsCollectorDomain}}, {{otlpCollectorDomain}} and
{{apiDomain}}. The API key is a secret, so {{apiKey}} stays as it is. The
"filledValues" field names the values that were filled.

Use --summary to see only the setup methods (label, recommended, whenToUse),
then --method to get the steps of the method you use.`,
		Example: `  oodle integrations get-setup-spec ecs --summary
  oodle integrations get-setup-spec ecs --method firelens_oodle_config -o json`,
		Args: exactArgs(1),
		Annotations: map[string]string{
			skipConfigAnnotation: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if method != "" && summary {
				return fmt.Errorf("use --method or --summary, not both")
			}
			format := getOutputFormat(cmd)
			c, cfg, err := loadSetupSpecClient(cmd)
			if err != nil {
				return err
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

			switch {
			case summary:
				summarizeSetupSpec(spec)
			case method != "":
				if err := selectSetupMethod(spec, method); err != nil {
					return err
				}
			}

			// Without a login the spec keeps its placeholders, which is
			// still correct: each one names the source of its value.
			// A failure to get them is not a reason to withhold the spec.
			if cfg != nil {
				values, err := instanceSetupValues(cmd, c, cfg)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"Note: the values of instance %s are not filled in: %v\n",
						cfg.Instance, err)
				} else {
					spec["filledValues"] = fillSetupSpec(spec, values)
					if missing := missingDomainValues(values); len(missing) > 0 {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"Note: instance %s has no integration record, so %s stay as placeholders. "+
								"Copy them from any integration card in the Oodle UI.\n",
							cfg.Instance, strings.Join(missing, ", "))
					}
				}
			}
			return output.Print(cmd.OutOrStdout(), format, spec, nil)
		},
	}
	cmd.Flags().StringVar(&method, "method", "", "Show only this setup method")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show the setup methods without their steps")
	return cmd
}

func newIntegrationsListSetupSpecsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-setup-specs",
		Short: "List all available integration setup specs",
		Long: `Returns a list of all available integration types that have a setup spec.
Each entry contains the type slug, the description, and the discovery fields:
signals (the data the integration gives), detect (what in a repo or an
environment shows that it applies) and related (other specs to read instead,
or as well). Includes aliases.

This endpoint does not require authentication.`,
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			skipConfigAnnotation: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			format := getOutputFormat(cmd)

			if c == nil {
				var err error
				c, err = newUnauthenticatedClient(cmd)
				if err != nil {
					return err
				}
			}

			resp, err := c.Inner.ListIntegrationSetupSpecs(cmd.Context())
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

			var items []map[string]interface{}
			if err := json.Unmarshal(body, &items); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			return output.Print(cmd.OutOrStdout(), format, items, setupSpecColumns())
		},
	}
}

func setupSpecColumns() []output.Column {
	return []output.Column{
		{Header: "TYPE", Field: "type"},
		{Header: "SIGNALS", Field: "signals"},
		{Header: "DESCRIPTION", Field: "description"},
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
