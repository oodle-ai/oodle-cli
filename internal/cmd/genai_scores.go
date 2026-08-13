package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var scoreColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "VALUE", Field: "Value"},
	{Header: "STRING", Field: "StringValue"},
	{Header: "TYPE", Field: "DataType"},
	{Header: "SOURCE", Field: "Source"},
	{Header: "TRACE", Field: "TraceId"},
	{Header: "CREATED", Field: "CreatedAt"},
}

func newGenAIScoresCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scores",
		Aliases: []string{"score"},
		Short:   "Read evaluator output",
		Long: `Read the scores evaluators produce.

Read-only: scores are written by the evaluation pipeline, not by
hand. To make new scores appear, define a template and an
evaluator with ` + "`oodle genai templates`" + ` and
` + "`oodle genai evaluators`" + `.

Scores are read out of the trace store, so ` + "`list`" + ` defaults to the
last 15 minutes. Pass --start for anything older — an empty
result over the default window is not evidence that nothing was
scored.`,
	}

	cmd.AddCommand(newGenAIScoresListCmd())
	cmd.AddCommand(newGenAIScoresGetCmd())

	return cmd
}

func newGenAIScoresListCmd() *cobra.Command {
	var (
		page          pageFlags
		start         string
		name          string
		evaluatorName string
		traceID       string
		minValue      float32
		maxValue      float32
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List scores",
		Long: `List scores.

  # everything scored in the last day
  oodle genai scores list --start 2026-08-12T00:00:00Z

  # one evaluator's failing scores
  oodle genai scores list --name Hallucination --max 0.5`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			limit, pageNum := page.values()

			startAt, err := toRFC3339(start)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}

			params := &client.ListGenaiScoresParams{
				Limit:         limit,
				Page:          pageNum,
				Start:         optStr(startAt),
				Name:          optStr(name),
				EvaluatorName: optStr(evaluatorName),
				TraceId:       optStr(traceID),
			}
			if cmd.Flags().Changed("min") {
				params.ScoreValueMin = &minValue
			}
			if cmd.Flags().Changed("max") {
				params.ScoreValueMax = &maxValue
			}

			resp, err := c.Inner.ListGenaiScoresWithResponse(
				cmd.Context(), getInstance(cmd), params,
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
				cmd, deref(resp.JSON200.Data), scoreColumns,
			)
		},
	}
	page.addTo(cmd)
	cmd.Flags().StringVar(
		&start, "start", "",
		"Lower bound on score time: RFC3339, 'now', or a "+
			"relative duration like -24h (default: 15m ago)",
	)
	cmd.Flags().StringVar(
		&name, "name", "", "Exact score name",
	)
	cmd.Flags().StringVar(
		&evaluatorName, "evaluator-name", "",
		"Exact name of the evaluator that produced the score",
	)
	cmd.Flags().StringVar(
		&traceID, "trace-id", "",
		"Only scores on this trace",
	)
	cmd.Flags().Float32Var(
		&minValue, "min", 0, "Lower bound on the numeric value",
	)
	cmd.Flags().Float32Var(
		&maxValue, "max", 0, "Upper bound on the numeric value",
	)
	return cmd
}

func newGenAIScoresGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <score-id>",
		Short: "Get a score",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.GetGenaiScoreWithResponse(
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
			return printGenAI(cmd, resp.JSON200, scoreColumns)
		},
	}
}
