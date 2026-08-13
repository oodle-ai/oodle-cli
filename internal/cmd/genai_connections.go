package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var connectionColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "PROVIDER", Field: "Provider"},
	{Header: "DEFAULT MODEL", Field: "DefaultModel"},
	{Header: "DEFAULT", Field: "IsDefault"},
	{Header: "UPDATED", Field: "UpdatedAt"},
}

func newGenAIConnectionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connections",
		Aliases: []string{"connection", "conn"},
		Short:   "Manage the LLM connections evaluators and experiments use",
		Long: `Manage LLM connections — provider credentials.

An LLM-as-judge evaluator and an experiment both need a
connection to call a model through. Keys are encrypted at rest
and never returned by list or get, so an update that omits
--api-key leaves the stored key in place.`,
	}

	cmd.AddCommand(newGenAIConnectionsListCmd())
	cmd.AddCommand(newGenAIConnectionsCreateCmd())
	cmd.AddCommand(newGenAIConnectionsUpdateCmd())
	cmd.AddCommand(newGenAIConnectionsDeleteCmd())

	return cmd
}

func newGenAIConnectionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List LLM connections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.ListGenaiLlmConnectionsWithResponse(
				cmd.Context(), getInstance(cmd),
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printGenAI(
				cmd, deref(resp.JSON200.Data), connectionColumns,
			)
		},
	}
}

func newGenAIConnectionsCreateCmd() *cobra.Command {
	var (
		file         string
		name         string
		provider     string
		apiKey       string
		baseURL      string
		defaultModel string
		isDefault    bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an LLM connection",
		Long: `Create an LLM connection.

  oodle genai connections create --name openai \
    --provider openai --api-key "$OPENAI_API_KEY" \
    --default-model gpt-4o --default

Passing the key on the command line puts it in your shell
history; prefer a --file, or read it from the environment as
above.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiLlmConnectionJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			} else {
				if name == "" || provider == "" {
					return fmt.Errorf(
						"--name and --provider are required " +
							"unless --file is given",
					)
				}
				body.Name = name
				body.Provider = provider
				body.ApiKey = optStr(apiKey)
				body.BaseUrl = optStr(baseURL)
				body.DefaultModel = optStr(defaultModel)
				if isDefault {
					body.IsDefault = &isDefault
				}
			}

			resp, err := c.Inner.CreateGenaiLlmConnectionWithResponse(
				cmd.Context(), getInstance(cmd), body,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return errEmptyResponse
			}
			return printGenAI(
				cmd, resp.JSON201, connectionColumns,
			)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the connection",
	)
	cmd.Flags().StringVar(
		&name, "name", "", "Connection name",
	)
	cmd.Flags().StringVar(
		&provider, "provider", "",
		"Provider, e.g. openai, anthropic, google, bedrock",
	)
	cmd.Flags().StringVar(
		&apiKey, "api-key", "", "Provider API key",
	)
	cmd.Flags().StringVar(
		&baseURL, "base-url", "",
		"Override the provider's base URL",
	)
	cmd.Flags().StringVar(
		&defaultModel, "default-model", "",
		"Model used when a caller names none",
	)
	cmd.Flags().BoolVar(
		&isDefault, "default", false,
		"Make this the instance's default connection",
	)
	return cmd
}

func newGenAIConnectionsUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <connection-id>",
		Short: "Update an LLM connection from a JSON or YAML file",
		Long: `Update an LLM connection.

Omitting apiKey keeps the stored key; sending an empty string
does too. Note that baseUrl and isDefault are replaced by
whatever the file says, so include them if you want them kept.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.UpdateGenaiLlmConnectionJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.UpdateGenaiLlmConnectionWithResponse(
				cmd.Context(), getInstance(cmd), args[0], body,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printGenAI(
				cmd, resp.JSON200, connectionColumns,
			)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the updates (required)",
	)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newGenAIConnectionsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <connection-id>",
		Short: "Delete an LLM connection",
		Long: `Delete an LLM connection.

Evaluation rules and experiments that reference it will fail on
their next run, so check ` + "`oodle genai eval-rules list`" + ` first.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if !confirmAction(fmt.Sprintf(
				"Delete LLM connection %q?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiLlmConnectionWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"Deleted LLM connection %s\n", args[0],
			)
			return nil
		},
	}
}
