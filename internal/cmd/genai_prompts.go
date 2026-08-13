package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// promptListColumns describes the per-name rollup the list
// endpoint returns — one row per prompt, not per version.
var promptListColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "TYPE", Field: "Type"},
	{Header: "VERSIONS", Field: "Versions"},
	{Header: "LABELS", Field: "Labels"},
	{Header: "UPDATED", Field: "LastUpdatedAt"},
}

// promptColumns describes a single prompt version.
var promptColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "VERSION", Field: "Version"},
	{Header: "TYPE", Field: "Type"},
	{Header: "LABELS", Field: "Labels"},
	{Header: "COMMIT", Field: "CommitMessage"},
	{Header: "UPDATED", Field: "UpdatedAt"},
}

func newGenAIPromptsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prompts",
		Aliases: []string{"prompt"},
		Short:   "Manage versioned prompts",
		Long: `Manage versioned prompts.

Every create adds a *version* rather than replacing one, so a
prompt's history is never lost. Applications resolve a prompt by
label, not by number — "production" is the default, and moving
that label is how a new version is rolled out.`,
	}

	cmd.AddCommand(newGenAIPromptsListCmd())
	cmd.AddCommand(newGenAIPromptsGetCmd())
	cmd.AddCommand(newGenAIPromptsVersionsCmd())
	cmd.AddCommand(newGenAIPromptsCreateCmd())
	cmd.AddCommand(newGenAIPromptsLabelCmd())
	cmd.AddCommand(newGenAIPromptsDeleteCmd())

	return cmd
}

func newGenAIPromptsListCmd() *cobra.Command {
	var (
		page        pageFlags
		search      string
		searchField string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List prompts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			limit, pageNum := page.values()

			resp, err := c.Inner.ListGenaiPromptsWithResponse(
				cmd.Context(), getInstance(cmd),
				&client.ListGenaiPromptsParams{
					Limit:       limit,
					Page:        pageNum,
					Search:      optStr(search),
					SearchField: optStr(searchField),
				},
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
				cmd, deref(resp.JSON200.Data), promptListColumns,
			)
		},
	}
	page.addTo(cmd)
	cmd.Flags().StringVar(
		&search, "search", "",
		"Substring match on the prompt name",
	)
	cmd.Flags().StringVar(
		&searchField, "search-field", "",
		"Field --search applies to (defaults to the name)",
	)
	return cmd
}

func newGenAIPromptsGetCmd() *cobra.Command {
	var (
		version int
		label   string
		raw     bool
	)
	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Get a prompt by name",
		Long: `Get a prompt by name.

Without --version or --label this resolves the "production"
label, which is what the SDKs do. Prompts that reference other
prompts come back fully expanded; pass --raw to see the
dependency tags instead.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			params := &client.GetGenaiPromptParams{
				Label: optStr(label),
			}
			if version > 0 {
				params.Version = &version
			}
			if raw {
				no := "false"
				params.Resolve = &no
			}

			resp, err := c.Inner.GetGenaiPromptWithResponse(
				cmd.Context(), getInstance(cmd),
				args[0], params,
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
			return printGenAI(cmd, resp.JSON200, promptColumns)
		},
	}
	cmd.Flags().IntVar(
		&version, "version", 0,
		"Exact version to fetch (takes precedence over --label)",
	)
	cmd.Flags().StringVar(
		&label, "label", "",
		"Label to resolve (default: production)",
	)
	cmd.Flags().BoolVar(
		&raw, "raw", false,
		"Return the prompt without expanding its dependencies",
	)
	return cmd
}

func newGenAIPromptsVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions <name>",
		Short: "List a prompt's versions",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.ListGenaiPromptVersionsWithResponse(
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
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printGenAI(
				cmd, deref(resp.JSON200.Data), promptColumns,
			)
		},
	}
}

func newGenAIPromptsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a prompt version from a JSON or YAML file",
		Long: `Create a prompt version from a JSON or YAML file.

The version number is assigned by the server. Posting a name
that already exists appends a version; it never overwrites one.

  {
    "name": "support-reply",
    "type": "chat",
    "prompt": [
      {"role": "system", "content": "You are a support agent."},
      {"role": "user", "content": "{{question}}"}
    ],
    "labels": ["production"],
    "commitMessage": "soften the tone"
  }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiPromptJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.CreateGenaiPromptWithResponse(
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
			return printGenAI(cmd, resp.JSON201, promptColumns)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the prompt (required)",
	)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newGenAIPromptsLabelCmd() *cobra.Command {
	var (
		version int
		labels  []string
		replace bool
	)
	cmd := &cobra.Command{
		Use:   "label <name>",
		Short: "Add or replace the labels on a prompt version",
		Long: `Add or replace the labels on a prompt version.

Moving a label to another version is how a rollout happens:
applications resolve by label, so the next request picks up the
new version with no deploy.

By default the given labels are merged onto the version. Pass
--replace to make the version's label set exactly what is
given, which is also the only way to remove one.

"latest" is managed by the server and cannot be set by hand.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			if replace {
				resp, err := c.Inner.UpdateGenaiPromptLabelsWithResponse(
					cmd.Context(), instance, args[0],
					client.UpdateGenaiPromptLabelsJSONRequestBody{
						Version: version,
						Labels:  &labels,
					},
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
					"Set labels on %s v%d to %v\n",
					args[0], version, labels,
				)
				return nil
			}

			resp, err := c.Inner.UpdateGenaiPromptVersionLabelsWithResponse(
				cmd.Context(), instance, args[0], version,
				client.UpdateGenaiPromptVersionLabelsJSONRequestBody{
					NewLabels: &labels,
				},
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
			return printGenAI(cmd, resp.JSON200, promptColumns)
		},
	}
	cmd.Flags().IntVar(
		&version, "version", 0,
		"Prompt version to label (required)",
	)
	cmd.Flags().StringSliceVar(
		&labels, "labels", nil,
		"Labels to apply, e.g. --labels production,staging (required)",
	)
	cmd.Flags().BoolVar(
		&replace, "replace", false,
		"Replace the version's labels instead of merging",
	)
	_ = cmd.MarkFlagRequired("version")
	_ = cmd.MarkFlagRequired("labels")
	return cmd
}

func newGenAIPromptsDeleteCmd() *cobra.Command {
	var version int
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a prompt, or one of its versions",
		Long: `Delete a prompt and every version of it.

Pass --version to delete just that one version. Either form is
refused with a 409 while another prompt still references this
one.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			force := forceFlag(cmd)

			if version > 0 {
				if !confirmAction(fmt.Sprintf(
					"Delete prompt %q version %d?",
					args[0], version,
				), force) {
					return fmt.Errorf("aborted")
				}
				resp, err := c.Inner.DeleteGenaiPromptVersionWithResponse(
					cmd.Context(), instance, args[0], version,
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
					"Deleted prompt %s version %d\n",
					args[0], version,
				)
				return nil
			}

			if !confirmAction(fmt.Sprintf(
				"Delete prompt %q and ALL its versions?",
				args[0],
			), force) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiPromptWithResponse(
				cmd.Context(), instance, args[0],
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
				cmd.OutOrStdout(), "Deleted prompt %s\n", args[0],
			)
			return nil
		},
	}
	cmd.Flags().IntVar(
		&version, "version", 0,
		"Delete only this version instead of the whole prompt",
	)
	return cmd
}
