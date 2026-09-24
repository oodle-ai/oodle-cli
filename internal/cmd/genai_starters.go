package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var starterColumns = []output.Column{
	{Header: "ID", Field: "Id"},
	{Header: "NAME", Field: "Name"},
	{Header: "CATEGORY", Field: "Category"},
	{Header: "DESCRIPTION", Field: "Description"},
}

func newGenAITemplatesStartersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "starters [starter-id]",
		Short: "List code evaluator starters, or print one as a template",
		Long: `List the code evaluator starters: ready-made Python checks
such as JSON validity, tone, PII leaks and conversation
degeneration. Each has its settings in UPPER_CASE names at
the top of the source.

With a starter id, print a template file for it. The source is
a YAML block, so it reads and edits as Python. Change the
settings, then create the template:

  oodle genai templates starters pii-leak > pii.yaml
  $EDITOR pii.yaml
  oodle genai templates create -f pii.yaml

A starter is not a template: nothing runs until you create one
and wire it up with ` + "`oodle genai evaluators create`" + `.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.ListGenaiCodeEvalStartersWithResponse(
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
			starters := deref(resp.JSON200.Data)
			if len(args) == 0 {
				return printGenAI(cmd, starters, starterColumns)
			}
			for _, s := range starters {
				if s.Id == args[0] {
					return writeStarterTemplate(cmd, s)
				}
			}
			return fmt.Errorf(
				"no starter %q; run `oodle genai templates starters` "+
					"to list them", args[0],
			)
		},
	}
}

// starterTemplate is a create body for a code template. The
// field order is the order a reader wants: what it is, then the
// source to edit.
type starterTemplate struct {
	Name               string `yaml:"name"`
	Type               string `yaml:"type"`
	SourceCodeLanguage string `yaml:"sourceCodeLanguage"`
	SourceCode         string `yaml:"sourceCode"`
}

// writeStarterTemplate prints a starter as a file that
// `templates create -f` reads. yaml.v3 writes a multi-line
// string as a literal block, so the Python keeps its lines and
// indentation instead of arriving as one escaped JSON string.
func writeStarterTemplate(cmd *cobra.Command, s client.CodeEvalStarter) error {
	out, err := yaml.Marshal(starterTemplate{
		Name:               s.Name,
		Type:               "code",
		SourceCodeLanguage: "python",
		SourceCode:         s.SourceCode,
	})
	if err != nil {
		return fmt.Errorf("encoding the template: %w", err)
	}
	_, err = cmd.OutOrStdout().Write(out)
	return err
}
