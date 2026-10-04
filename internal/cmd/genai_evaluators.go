package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var templateColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "TYPE", Field: "Type"},
	{Header: "CATEGORY", Field: "Category"},
	{Header: "VERSION", Field: "Version"},
	{Header: "VARS", Field: "Vars"},
	{Header: "CREATED", Field: "CreatedAt"},
}

var evaluatorColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "TEMPLATE", Field: "EvaluatorId"},
	{Header: "KIND", Field: "EvaluatorType"},
	{Header: "TARGET", Field: "TargetType"},
	{Header: "SAMPLING", Field: "SamplingRate"},
	{Header: "MAX/HR", Field: "MaxInvocationsPerHour"},
	{Header: "ENABLED", Field: "Enabled"},
}

func newGenAITemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "templates",
		Aliases: []string{"template"},
		Short:   "Manage evaluator templates (Evaluations > Library in the UI)",
		Long: `Manage evaluator templates — the judges that produce scores.

These are what the Oodle UI lists under Evaluations > Library,
where "New Template" creates one. The HTTP API calls them
eval-templates. To make one actually run against traffic, wire
it up with ` + "`oodle genai evaluators`" + `.

Three kinds:

  llm              a judge prompt with {{var}} placeholders, run
                   by a model against a span's fields
  code             a Python function scored against a span, for
                   checks a model should not be guessing at
                   (JSON validity, exact match, latency budgets)
  output_comparer  a judge prompt that scores the output against
                   a dataset item's expected output, using
                   {{output}} and {{expected_output}}

An output comparer only has ground truth inside an experiment,
so it never runs against live traffic and goes in that run's
--output-comparer-id rather than --evaluator-id. An item with no
expected output is skipped rather than scored zero.

The list also includes Oodle-managed templates (ids beginning
"oodle-managed-"). Those are read-only: reference them from an
evaluator, but update and delete are refused. The built-in code
checks are managed code templates (ids beginning
"oodle-managed-code-"): they run with no code, and each
evaluator sets their settings in its "params".`,
	}

	cmd.AddCommand(newGenAITemplatesListCmd())
	cmd.AddCommand(newGenAITemplatesGetCmd())
	cmd.AddCommand(newGenAITemplatesCreateCmd())
	cmd.AddCommand(newGenAITemplatesUpdateCmd())
	cmd.AddCommand(newGenAITemplatesDeleteCmd())
	cmd.AddCommand(newGenAITemplatesStartersCmd())
	cmd.AddCommand(newGenAITemplatesValidateCmd())
	cmd.AddCommand(newGenAITemplatesPullCmd())
	cmd.AddCommand(newGenAITemplatesPushCmd())
	cmd.AddCommand(newGenAITemplatesTestCmd())

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
			return printGenAIObject(
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
			return printGenAIObject(
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

A code template can declare settings in "params". Each
evaluator made from the template sets its own values, and the
code reads them as ctx.params. "libraryPins" sets the version
of a shared library that the code runs; a library with no pin
runs at its latest version:

  name: Mentions refund
  type: code
  sourceCodeLanguage: python
  params:
    - name: required
      type: string_list
      default: [refund]
  libraryPins:
    acme_text: 3
  sourceCode: |
    from oodle_eval.v1 import metrics

    def evaluate(ctx):
        return EvaluationResult(
            scores=metrics.keyword_check(ctx, **ctx.params))

A setting type is one of string, text, number, integer,
boolean, string_list, enum (with "options") or json.

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
			return printGenAIObject(
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
  oodle genai templates update <id> -f eval.yaml

A change of a code template's params that makes the settings
of an existing evaluator not valid is refused (409). The error
names those evaluators. Change their params first, or keep the
setting compatible.`,
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
			return printGenAIObject(
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

// evaluatorDetailColumns add the fields of one rule that the
// list leaves out for width.
var evaluatorDetailColumns = append(
	append([]output.Column{}, evaluatorColumns...),
	output.Column{Header: "PARAMS", Field: "Params"},
	output.Column{Header: "READS SCORES FROM", Field: "ReadsScoresFrom"},
)

// evaluatorDetailRow is the table form of one rule.
type evaluatorDetailRow struct {
	client.EvaluationRuleResponse
	Params          string
	ReadsScoresFrom string
}

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
	cmd.AddCommand(newGenAIEvaluatorsGetCmd())
	cmd.AddCommand(newGenAIEvaluatorsCreateCmd())
	cmd.AddCommand(newGenAIEvaluatorsUpdateCmd())
	cmd.AddCommand(newGenAIEvaluatorsDeleteCmd())

	return cmd
}

func newGenAIEvaluatorsListCmd() *cobra.Command {
	var evaluatorType string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List evaluators",
		Long: `List evaluators.

The KIND column is the type of the template each one runs, so an
output comparer can be told from an ordinary judge without
fetching the template. Narrow the list with --type.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.ListGenaiEvaluationRulesWithResponse(
				cmd.Context(), getInstance(cmd),
				&client.ListGenaiEvaluationRulesParams{
					EvaluatorType: optStr(evaluatorType),
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
			return printGenAIObject(
				cmd, deref(resp.JSON200.Data), evaluatorColumns,
			)
		},
	}
	cmd.Flags().StringVar(
		&evaluatorType, "type", "",
		"Only evaluators whose template is of this type: "+
			"llm, code or output_comparer",
	)
	return cmd
}

func newGenAIEvaluatorsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <evaluator>",
		Short: "Get an evaluator by id or name",
		Long: `Get an evaluator by id or name, with its settings (params) and
scoreInputRuleIds: the evaluators whose scores its code reads.

To edit one, write it to a file, change it, then update:

  oodle genai evaluators get "Refund mentioned" -o yaml > rule.yaml
  $EDITOR rule.yaml
  oodle genai evaluators update <id> -f rule.yaml`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			// An id goes straight to the single-rule route. Rule
			// ids have no fixed format, so a 404 there means the
			// argument can be a name, which only the list resolves.
			one, err := c.Inner.GetGenaiEvaluationRuleWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			var found *client.EvaluationRuleResponse
			switch {
			case one.StatusCode() == http.StatusNotFound:
			case one.StatusCode() >= 300:
				return genaiCheck(one.StatusCode(), one.HTTPResponse, one.Body)
			case one.JSON200 == nil:
				return errEmptyResponse
			default:
				found = one.JSON200
			}

			// The list resolves a name, and gives the names of the
			// rules whose scores this rule reads. It is not paged.
			var rules []client.EvaluationRuleResponse
			needList := found == nil || (isTabular(cmd) &&
				len(deref(found.ScoreInputRuleIds)) > 0)
			if needList {
				resp, err := c.Inner.ListGenaiEvaluationRulesWithResponse(
					cmd.Context(), getInstance(cmd),
					&client.ListGenaiEvaluationRulesParams{},
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
				rules = deref(resp.JSON200.Data)
			}
			if found == nil {
				for i := range rules {
					if rules[i].Name == args[0] {
						if found != nil {
							return fmt.Errorf(
								"more than one evaluator is named %q; "+
									"use the id", args[0],
							)
						}
						found = &rules[i]
					}
				}
			}
			if found == nil {
				return fmt.Errorf(
					"no evaluator %q; run `oodle genai evaluators list` "+
						"to list them", args[0],
				)
			}
			if !isTabular(cmd) {
				return printPlain(cmd, found)
			}
			return printGenAI(cmd, evaluatorDetailRow{
				EvaluationRuleResponse: *found,
				Params:                 formatParams(deref(found.Params)),
				ReadsScoresFrom:        ruleNames(rules, deref(found.ScoreInputRuleIds)),
			}, evaluatorDetailColumns)
		},
	}
}

// formatParams writes settings as name=value pairs, with each
// value in its JSON form, in name order.
func formatParams(params map[string]any) string {
	names := make([]string, 0, len(params))
	for k := range params {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, k := range names {
		v, err := json.Marshal(params[k])
		if err != nil {
			v = []byte(fmt.Sprint(params[k]))
		}
		parts[i] = k + "=" + string(v)
	}
	return strings.Join(parts, " ")
}

// ruleNames gives each id the name of its rule, because a list
// of ids does not tell a reader which evaluators they are.
func ruleNames(rules []client.EvaluationRuleResponse, ids []string) string {
	byID := make(map[string]string, len(rules))
	for _, r := range rules {
		byID[r.Id] = r.Name
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		if name, ok := byID[id]; ok {
			out[i] = name
		} else {
			out[i] = id
		}
	}
	return strings.Join(out, ", ")
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
while another depends on it.

"filters" limits the spans that the evaluator scores. Each entry
names a span field or attribute, an operator and a value:

  "filters": [
    {"name": "span::gen_ai.operation.name", "type": "eq", "value": "chat"},
    {"name": "resource::service.name", "type": "re", "value": "support-.*"}
  ]

An attribute name has its kind as a prefix: "span::" for a span
attribute, "resource::" for a resource attribute. A dotted name
with no prefix, such as "gen_ai.operation.name", is refused with
400, because the server cannot tell which kind it is.

"type" is one of:

  eq, neq, re, nre      (or =, !=, =~, !~)  stored as 0 to 3
  oneof, not_oneof                          stored as 4 and 5
  gt, gte, lt, lte                          stored as GT, GTE, LT, LTE

oneof and not_oneof take a list in "multi_value" in place of
"value":

  {"name": "span::gen_ai.request.model", "type": "oneof",
   "multi_value": ["gpt-4o", "claude-sonnet-4"]}

The server stores the operator in the form that the trace store
matches, so a get shows the stored form in place of the word. A
number outside 0 to 5, or an unknown word, is refused with 400.

For a code template that declares settings, "params" sets this
evaluator's values. A setting left out takes the template's
default. The server checks the values against the template:

  {
    "name": "Refund mentioned",
    "evaluatorId": "oodle-managed-code-keyword-check-v1",
    "params": {"required": ["refund", "return"], "mode": "any"},
    "enabled": true
  }

A code evaluator that reads other evaluators' scores
(ctx.scores["Helpfulness"]) runs after them. The server finds
those evaluators in the code and shows them in
scoreInputRuleIds; you do not set that field.`,
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
			return printGenAIObject(
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

  oodle genai evaluators update <id> --disable

"filters" in the file replaces the evaluator's filters. The
format is the one that ` + "`evaluators create --help`" + ` shows: for
example {"name": "span::gen_ai.operation.name", "type": "eq",
"value": "chat"}, or a "multi_value" list for oneof and
not_oneof. A dotted name with no "span::" or "resource::"
prefix is refused with 400.`,
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
			return printGenAIObject(
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
