package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var starterColumns = []output.Column{
	{Header: "ID", Field: "Id"},
	{Header: "NAME", Field: "Name"},
	{Header: "CATEGORY", Field: "Category"},
	{Header: "BUILT-IN", Field: "BuiltIn"},
	{Header: "SETTINGS", Field: "Settings"},
}

// starterRow is the table form of a starter. The settings are
// the reason to pick one starter over another, so the table
// names them; the description and the source stay in -o yaml.
type starterRow struct {
	Id       string
	Name     string
	Category string
	BuiltIn  string
	Settings string
}

// managedCodeCheckID is the id of the managed template that
// serves a built-in starter with no code. The server makes the
// id the same way.
func managedCodeCheckID(starterID string) string {
	return "oodle-managed-code-" + starterID + "-v1"
}

func newGenAITemplatesStartersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "starters [starter-id]",
		Short: "List code evaluator starters, or print one as a template",
		Long: `List the code evaluator starters: ready-made Python checks
such as JSON validity, tone, PII leaks and conversation
degeneration, and examples that combine checks with your own
scores. The SETTINGS column names the settings of each one.
The code reads them as ctx.params.

A starter marked BUILT-IN is also a managed template with the id
oodle-managed-code-<starter-id>-v1. To use it with no code,
make an evaluator with that template id and set the settings in
the evaluator's "params".

With a starter id, print a template file for it. The file
has the settings under "params" and the source as a YAML block,
so it reads and edits as Python. Change the defaults or the
code, then create the template:

  oodle genai templates starters pii-leak > pii.yaml
  $EDITOR pii.yaml
  oodle genai templates create -f pii.yaml

A starter is not a template: nothing runs until you create one
and connect it to an evaluator with ` + "`oodle genai evaluators create`" + `.`,
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
				if !isTabular(cmd) {
					return printPlain(cmd, starters)
				}
				rows := make([]starterRow, len(starters))
				for i, s := range starters {
					names := make([]string, 0)
					for _, p := range deref(s.Params) {
						names = append(names, p.Name)
					}
					builtIn := ""
					if s.BuiltIn {
						builtIn = "yes"
					}
					rows[i] = starterRow{
						Id:       s.Id,
						Name:     s.Name,
						Category: s.Category,
						BuiltIn:  builtIn,
						Settings: strings.Join(names, ", "),
					}
				}
				return printGenAI(cmd, rows, starterColumns)
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
// field order is the order a reader wants: what it is, the
// settings, then the source to edit.
type starterTemplate struct {
	Name               string         `yaml:"name" json:"name"`
	Type               string         `yaml:"type" json:"type"`
	SourceCodeLanguage string         `yaml:"sourceCodeLanguage" json:"sourceCodeLanguage"`
	ScoreType          string         `yaml:"scoreType,omitempty" json:"scoreType,omitempty"`
	HigherIsBetter     *bool          `yaml:"higherIsBetter,omitempty" json:"higherIsBetter,omitempty"`
	Params             []starterParam `yaml:"params,omitempty" json:"params,omitempty"`
	SourceCode         string         `yaml:"sourceCode" json:"sourceCode"`
}

// starterParam is a ParamSpec with yaml tags. Without them
// yaml.v3 writes a null for each unset field, and the file
// holds noise that the user must read past.
type starterParam struct {
	Name        string   `yaml:"name" json:"name"`
	Type        string   `yaml:"type" json:"type"`
	Label       string   `yaml:"label,omitempty" json:"label,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Default     any      `yaml:"default,omitempty" json:"default,omitempty"`
	Required    bool     `yaml:"required,omitempty" json:"required,omitempty"`
	Options     []string `yaml:"options,omitempty" json:"options,omitempty"`
}

// writeStarterTemplate prints a starter as a file that
// `templates create -f` reads. yaml.v3 writes a multi-line
// string as a literal block, so the Python keeps its lines and
// indentation instead of arriving as one escaped JSON string.
func writeStarterTemplate(cmd *cobra.Command, s client.CodeEvalStarter) error {
	tpl := starterTemplate{
		Name:               s.Name,
		Type:               "code",
		SourceCodeLanguage: "python",
		SourceCode:         s.SourceCode,
		ScoreType:          starterScoreType(s),
		HigherIsBetter:     s.PrimaryHigherIsBetter,
	}
	for _, p := range deref(s.Params) {
		tpl.Params = append(tpl.Params, starterParam{
			Name:        p.Name,
			Type:        p.Type,
			Label:       deref(p.Label),
			Description: deref(p.Description),
			Default:     p.Default,
			Required:    deref(p.Required),
			Options:     deref(p.Options),
		})
	}
	switch format := textOutputFormat(cmd); format {
	case output.FormatJSON:
		return printGenAI(cmd, tpl, nil)
	case output.FormatYAML, output.FormatTable:
	default:
		return fmt.Errorf(
			"a starter prints as a template file: use -o yaml "+
				"(the default) or -o json, not %q", format,
		)
	}
	out, err := output.MarshalYAML(tpl)
	if err != nil {
		return fmt.Errorf("encoding the template: %w", err)
	}
	w := cmd.OutOrStdout()
	if s.BuiltIn {
		// A comment, so that the file stays a valid create body.
		// It tells the user that no template is necessary.
		if _, err := fmt.Fprintf(w,
			"# Built-in check: an evaluator can use the template\n"+
				"# %s with no code. Set its\n"+
				"# settings in the evaluator's params.\n",
			managedCodeCheckID(s.Id),
		); err != nil {
			return err
		}
	}
	_, err = w.Write(out)
	return err
}

// starterScoreType gives the template's scoreType for the
// starter's first score. A template made from a starter then has
// the type and direction of the check it runs, as the managed
// check has: a check whose verdict is bad when true (refused,
// degenerated) is not shown as a pass.
func starterScoreType(s client.CodeEvalStarter) string {
	return strings.ToLower(deref(s.PrimaryScoreType))
}
