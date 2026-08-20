package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var experimentColumns = []output.Column{
	{Header: "RUN", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "STATUS", Field: "LatestJobStatus"},
	{Header: "DATASET", Field: "DatasetId"},
	{Header: "CREATED", Field: "CreatedAt"},
}

var experimentItemColumns = []output.Column{
	{Header: "ITEM", Field: "DatasetItemId"},
	{Header: "STATUS", Field: "Status"},
	{Header: "TRACE", Field: "TraceId"},
	{Header: "ERROR", Field: "ErrorMessage"},
	{Header: "CREATED", Field: "CreatedAt"},
}

var jobColumns = []output.Column{
	{Header: "ID", Field: "Id"},
	{Header: "TYPE", Field: "Type"},
	{Header: "STATUS", Field: "Status"},
	{Header: "RUN", Field: "DatasetRunId"},
	{Header: "ERROR", Field: "Error"},
	{Header: "CREATED", Field: "CreatedAt"},
}

// jobTypeLLMExperiment is the job type that runs a prompt over
// a dataset and scores the output. It matches the constant in
// api-server/apps/llmops/handlers/jobs.go.
const jobTypeLLMExperiment = "llm-experiment"

func newGenAIExperimentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "experiments",
		Aliases: []string{"experiment", "runs", "exp"},
		Short:   "Run a prompt over a dataset and score the result",
		Long: `Run a prompt over a dataset and score the result.

An experiment is a dataset run: every item is sent through the
prompt, the output is traced, and any evaluators you name score
it. Runs over the same dataset are directly comparable, which is
how a prompt change is judged before it ships.

  oodle genai experiments run --dataset-id <id> \
    --prompt-name support-reply --connection-id <id> \
    --model gpt-4o --evaluator-id oodle-managed-hallucination-v1

  oodle genai experiments list <dataset-name>
  oodle genai experiments items <run-id>`,
	}

	cmd.AddCommand(newGenAIExperimentsListCmd())
	cmd.AddCommand(newGenAIExperimentsItemsCmd())
	cmd.AddCommand(newGenAIExperimentsRunCmd())
	cmd.AddCommand(newGenAIExperimentsStatusCmd())
	cmd.AddCommand(newGenAIExperimentsCancelCmd())
	cmd.AddCommand(newGenAIExperimentsJobsCmd())

	return cmd
}

func newGenAIExperimentsListCmd() *cobra.Command {
	var page pageFlags
	cmd := &cobra.Command{
		Use:   "list <dataset-name>",
		Short: "List a dataset's experiment runs",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			limit, pageNum := page.values()

			resp, err := c.Inner.ListGenaiExperimentsWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
				&client.ListGenaiExperimentsParams{
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
				cmd, deref(resp.JSON200.Data), experimentColumns,
			)
		},
	}
	page.addTo(cmd)
	return cmd
}

func newGenAIExperimentsItemsCmd() *cobra.Command {
	var page pageFlags
	cmd := &cobra.Command{
		Use:   "items <run-id>",
		Short: "List an experiment run's per-item results",
		Long: `List an experiment run's per-item results.

Each row joins the dataset item's input and expected output with
the trace the run produced and the scores recorded against it,
so a failing item can be read end to end without a second
lookup. Use -o json to see those nested fields.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			limit, pageNum := page.values()

			resp, err := c.Inner.ListGenaiExperimentItemsWithResponse(
				cmd.Context(), getInstance(cmd),
				&client.ListGenaiExperimentItemsParams{
					DatasetRunId: args[0],
					Limit:        limit,
					Page:         pageNum,
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
				cmd, deref(resp.JSON200.Data),
				experimentItemColumns,
			)
		},
	}
	page.addTo(cmd)
	return cmd
}

func newGenAIExperimentsRunCmd() *cobra.Command {
	var (
		file string
		cfg  experimentConfigFlags
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start an experiment run",
		Long: `Start an experiment run over a dataset.

The run is queued and picked up by a worker, so this returns as
soon as the job is created. Poll it with:

  oodle genai experiments status <job-id>

The prompt comes from either --prompt-name (resolved through
--prompt-version or --prompt-label, defaulting to "production")
or a literal --prompt-template. The run name is assigned
automatically when --run-name is omitted.

Evaluators come in two kinds and each id goes in its own flag.
An ordinary judge scores the generation on its own merits and
goes in --evaluator-id. An output comparer scores it against
the dataset item's expected output and goes in
--output-comparer-id; an item with no expected output is
skipped rather than scored zero. The server rejects an id put
in the wrong one, so ` + "`oodle genai evaluators list`" + ` reports each
evaluator's kind.

Anything the flags cannot express — per-evaluator model
overrides, extra model params — goes in a --file config, which
is passed through to the job verbatim:

  {
    "datasetId": "<id>",
    "llmConnectionId": "<id>",
    "model": "gpt-4o",
    "promptName": "support-reply",
    "evaluatorIds": ["<id>"],
    "evaluatorRules": [
      {"templateId": "<id>", "ruleName": "relevance",
       "model": "gpt-4o-mini"}
    ],
    "outputComparerIds": ["<id>"],
    "outputComparerRules": [
      {"templateId": "<id>", "ruleName": "output match",
       "model": "gpt-4o-mini"}
    ],
    "evalConnectionId": "<id>"
  }

An evaluator judges with the model its template names, falling
back to the eval connection's default model.
--evaluator-model overrides that for every id given by flag,
which is how a run judges with a cheaper model than it
generates with; the rules above override it per evaluator.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			config := map[string]any{}
			if file != "" {
				if err := readInputFile(file, &config); err != nil {
					return err
				}
			}
			cfg.applyTo(config)
			if err := validateExperimentConfig(config); err != nil {
				return err
			}

			resp, err := c.Inner.CreateGenaiJobWithResponse(
				cmd.Context(), getInstance(cmd),
				client.CreateGenaiJobJSONRequestBody{
					Type:   jobTypeLLMExperiment,
					Config: config,
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
			if resp.JSON201 == nil {
				return errEmptyResponse
			}
			return printGenAI(cmd, resp.JSON201, jobColumns)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the job config",
	)
	cfg.addTo(cmd, true)
	return cmd
}

func newGenAIExperimentsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <job-id>",
		Short: "Get an experiment job's status",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.GetGenaiJobWithResponse(
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
			return printGenAI(cmd, resp.JSON200, jobColumns)
		},
	}
}

func newGenAIExperimentsCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel a queued or running experiment job",
		Long: `Cancel a queued or running experiment job.

Cancellation is the only status transition the API accepts;
every other one belongs to the worker.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if !confirmAction(fmt.Sprintf(
				"Cancel job %q?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.UpdateGenaiJobWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
				client.UpdateGenaiJobJSONRequestBody{
					Status: "cancelled",
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
			return printGenAI(cmd, resp.JSON200, jobColumns)
		},
	}
}

func newGenAIExperimentsJobsCmd() *cobra.Command {
	var runID string
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "List experiment jobs",
		Long: `List experiment jobs.

Without --run-id this lists the jobs still pending, which is
what a queue check wants. Pass --run-id for the history of one
run.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.ListGenaiJobsWithResponse(
				cmd.Context(), getInstance(cmd),
				&client.ListGenaiJobsParams{
					DatasetRunId: optStr(runID),
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
				cmd, deref(resp.JSON200.Data), jobColumns,
			)
		},
	}
	cmd.Flags().StringVar(
		&runID, "run-id", "",
		"List the jobs for one run instead of pending jobs",
	)
	return cmd
}
