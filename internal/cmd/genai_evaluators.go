package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var templateColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "TYPE", Field: "Type"},
	{Header: "VERSION", Field: "Version"},
	{Header: "VARS", Field: "Vars"},
	{Header: "CREATED", Field: "CreatedAt"},
}

var evaluatorColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "TEMPLATE", Field: "EvaluatorId"},
	{Header: "TARGET", Field: "TargetType"},
	{Header: "SAMPLING", Field: "SamplingRate"},
	{Header: "MAX/HR", Field: "MaxInvocationsPerHour"},
	{Header: "ENABLED", Field: "Enabled"},
}

func newGenAITemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "templates",
		Aliases: []string{"template", "library"},
		Short:   "Manage evaluator templates (the Library)",
		Long: `Manage evaluator templates — the judges that produce scores.

These are what the Oodle UI lists under Evaluations > Library,
where "New Template" creates one. The HTTP API calls them
eval-templates. To make one actually run against traffic, wire
it up with ` + "`oodle genai evaluators`" + `.

Two kinds:

  llm    a judge prompt with {{var}} placeholders, run by a
         model against a span's fields
  code   a Python function scored against a span, for checks a
         model should not be guessing at (JSON validity, exact
         match, latency budgets)

The list also includes Oodle-managed templates (ids beginning
"oodle-managed-"). Those are read-only: reference them from an
evaluator, but update and delete are refused.`,
	}

	cmd.AddCommand(newGenAITemplatesListCmd())
	cmd.AddCommand(newGenAITemplatesGetCmd())
	cmd.AddCommand(newGenAITemplatesCreateCmd())
	cmd.AddCommand(newGenAITemplatesUpdateCmd())
	cmd.AddCommand(newGenAITemplatesDeleteCmd())

	return cmd
}

func newGenAITemplatesListCmd() *cobra.Command {
	var page pageFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			limit, pageNum := page.values()

			resp, err := c.Inner.ListGenaiEvaluatorsWithResponse(
				cmd.Context(), getInstance(cmd),
				&client.ListGenaiEvaluatorsParams{
					Limit: limit,
					Page:  pageNum,
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
				cmd, deref(resp.JSON200.Data), templateColumns,
			)
		},
	}
	page.addTo(cmd)
	return cmd
}

func newGenAITemplatesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <template-id>",
		Short: "Get a template",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.GetGenaiEvaluatorWithResponse(
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
				cmd, resp.JSON200, templateColumns,
			)
		},
	}
}

func newGenAITemplatesCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a template from a JSON or YAML file",
		Long: `Create an evaluator template from a JSON or YAML file.

An LLM judge:

  {
    "name": "Answer relevance",
    "type": "llm",
    "vars": ["query", "generation"],
    "prompt": "Rate 0-1 how well the answer addresses the question.\n\nQuestion: {{query}}\nAnswer: {{generation}}",
    "outputSchema": {"score": "Numeric 0-1", "reasoning": "Why"}
  }

A code template (source is capped at 256 KB and must be
Python; needs the per-instance code-eval flag and an enterprise
plan, on create and update alike — 400 means the flag is off,
403 means the plan does not cover it):

  {
    "name": "Valid JSON",
    "type": "code",
    "sourceCodeLanguage": "python",
    "sourceCode": "def evaluate(span):\n    ..."
  }

Only "code" is special-cased. Any other type is stored as given
and runs as an LLM judge, so a typo in "type" fails quietly.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiEvaluatorJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.CreateGenaiEvaluatorWithResponse(
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
				cmd, resp.JSON201, templateColumns,
			)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the template (required)",
	)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newGenAITemplatesUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <template-id>",
		Short: "Update a template from a JSON or YAML file",
		Long: `Update a template. Fields left out are untouched.

Round-tripping is the safe way to edit one:

  oodle genai templates get <id> -o yaml > eval.yaml
  $EDITOR eval.yaml
  oodle genai templates update <id> -f eval.yaml`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.UpdateGenaiEvaluatorJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.UpdateGenaiEvaluatorWithResponse(
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
				cmd, resp.JSON200, templateColumns,
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

func newGenAITemplatesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <template-id>",
		Short: "Delete a template",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if !confirmAction(fmt.Sprintf(
				"Delete template %q?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiEvaluatorWithResponse(
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
				"Deleted template %s\n", args[0],
			)
			return nil
		},
	}
}

// --- Evaluation rules ---

func newGenAIEvaluatorsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "evaluators",
		Aliases: []string{"evaluator", "eval-rules", "rules"},
		Short:   "Run templates over live traffic",
		Long: `Manage evaluators.

An evaluator is what makes a template actually run: it says
which spans to score, how often, and how span fields map onto
the template's variables. These are what the Oodle UI lists
under Evaluations > Evaluators; the HTTP API calls them
evaluation-rules.

An LLM template needs --file with an "llmConnectionId" — the
server rejects an evaluator that has no model to call, because
it would be skipped at run time and score nothing.

Sampling and the hourly cap are the cost controls — an
unsampled evaluator with no cap runs a model call per matching
span, so set both before enabling one on a busy service.`,
	}

	cmd.AddCommand(newGenAIEvaluatorsListCmd())
	cmd.AddCommand(newGenAIEvaluatorsCreateCmd())
	cmd.AddCommand(newGenAIEvaluatorsUpdateCmd())
	cmd.AddCommand(newGenAIEvaluatorsDeleteCmd())

	return cmd
}

func newGenAIEvaluatorsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List evaluators",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.ListGenaiEvaluationRulesWithResponse(
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
				cmd, deref(resp.JSON200.Data), evaluatorColumns,
			)
		},
	}
}

func newGenAIEvaluatorsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an evaluator from a JSON or YAML file",
		Long: `Create an evaluator from a JSON or YAML file.

  {
    "name": "Score support answers",
    "evaluatorId": "<template-id>",
    "targetType": "trace",
    "samplingRate": 0.1,
    "maxInvocationsPerHour": 200,
    "llmConnectionId": "<connection-id>",
    "variableMapping": {
      "query": "input",
      "generation": "output"
    },
    "enabled": true
  }

dependsOnRuleIds gates this rule on other rules' scores. Cycles
and self-references are rejected, and a rule cannot be deleted
while another depends on it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiEvaluationRuleJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.CreateGenaiEvaluationRuleWithResponse(
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
				cmd, resp.JSON201, evaluatorColumns,
			)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the evaluator (required)",
	)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newGenAIEvaluatorsUpdateCmd() *cobra.Command {
	var (
		file    string
		enable  bool
		disable bool
	)
	cmd := &cobra.Command{
		Use:   "update <evaluator-id>",
		Short: "Update an evaluator",
		Long: `Update an evaluator. Fields left out are untouched.

--enable / --disable are the common case and need no file:

  oodle genai evaluators update <id> --disable`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if enable && disable {
				return fmt.Errorf(
					"--enable and --disable are mutually exclusive",
				)
			}

			var body client.UpdateGenaiEvaluationRuleJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			}
			switch {
			case enable:
				on := true
				body.Enabled = &on
			case disable:
				off := false
				body.Enabled = &off
			case file == "":
				return fmt.Errorf(
					"one of --file, --enable, or --disable is required",
				)
			}

			resp, err := c.Inner.UpdateGenaiEvaluationRuleWithResponse(
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
				cmd, resp.JSON200, evaluatorColumns,
			)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the updates",
	)
	cmd.Flags().BoolVar(
		&enable, "enable", false, "Enable the evaluator",
	)
	cmd.Flags().BoolVar(
		&disable, "disable", false, "Disable the evaluator",
	)
	return cmd
}

func newGenAIEvaluatorsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <evaluator-id>",
		Short: "Delete an evaluator",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if !confirmAction(fmt.Sprintf(
				"Delete evaluator %q?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiEvaluationRuleWithResponse(
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
				"Deleted evaluator %s\n", args[0],
			)
			return nil
		},
	}
}
