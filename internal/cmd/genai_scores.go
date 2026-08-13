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

var scoreConfigColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "TYPE", Field: "DataType"},
	{Header: "MIN", Field: "MinValue"},
	{Header: "MAX", Field: "MaxValue"},
	{Header: "DESCRIPTION", Field: "Description"},
}

func newGenAIScoresCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scores",
		Aliases: []string{"score"},
		Short:   "Read evaluator output and record your own scores",
		Long: `Read evaluator output and record your own scores.

Scores are read out of the trace store, so ` + "`list`" + ` defaults to the
last 15 minutes. Pass --start for anything older — an empty
result over the default window is not evidence that nothing was
scored.`,
	}

	cmd.AddCommand(newGenAIScoresListCmd())
	cmd.AddCommand(newGenAIScoresGetCmd())
	cmd.AddCommand(newGenAIScoresCreateCmd())
	cmd.AddCommand(newGenAIScoreConfigsCmd())

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

func newGenAIScoresCreateCmd() *cobra.Command {
	var (
		file          string
		traceID       string
		observationID string
		name          string
		value         float32
		stringValue   string
		dataType      string
		comment       string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Attach a score to a trace or observation",
		Long: `Attach a score to a trace, or to one observation within it.

  oodle genai scores create --trace-id <id> \
    --name thumbs --value 1 --comment "user approved"

  oodle genai scores create --trace-id <id> \
    --name sentiment --string-value positive \
    --data-type CATEGORICAL

Numeric and boolean scores use --value; categorical ones use
--string-value.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiScoreJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			} else {
				if traceID == "" || name == "" {
					return fmt.Errorf(
						"--trace-id and --name are required " +
							"unless --file is given",
					)
				}
				body.TraceId = traceID
				body.Name = name
				body.ObservationId = optStr(observationID)
				body.StringValue = optStr(stringValue)
				body.DataType = optStr(dataType)
				body.Comment = optStr(comment)
				if cmd.Flags().Changed("value") {
					body.Value = &value
				}
			}

			resp, err := c.Inner.CreateGenaiScoreWithResponse(
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
			return printGenAI(cmd, resp.JSON201, scoreColumns)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the score",
	)
	cmd.Flags().StringVar(
		&traceID, "trace-id", "", "Trace to score",
	)
	cmd.Flags().StringVar(
		&observationID, "observation-id", "",
		"Score one observation rather than the whole trace",
	)
	cmd.Flags().StringVar(
		&name, "name", "", "Score name",
	)
	cmd.Flags().Float32Var(
		&value, "value", 0,
		"Numeric or boolean value",
	)
	cmd.Flags().StringVar(
		&stringValue, "string-value", "",
		"Categorical value",
	)
	cmd.Flags().StringVar(
		&dataType, "data-type", "",
		"NUMERIC, BOOLEAN, or CATEGORICAL (default NUMERIC)",
	)
	cmd.Flags().StringVar(
		&comment, "comment", "",
		"Free-text note stored with the score",
	)
	return cmd
}

// newGenAIScoreConfigsCmd returns `oodle genai scores configs`,
// which declares the shape a named score must take.
func newGenAIScoreConfigsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "configs",
		Aliases: []string{"config"},
		Short:   "Declare the shape a named score must take",
		Long: `Manage score configs.

A config pins a score name to a data type and range, so the UI
and every evaluator writing that name agree on what a value
means.`,
	}
	cmd.AddCommand(newGenAIScoreConfigsListCmd())
	cmd.AddCommand(newGenAIScoreConfigsCreateCmd())
	return cmd
}

func newGenAIScoreConfigsListCmd() *cobra.Command {
	var page pageFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List score configs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			limit, pageNum := page.values()

			resp, err := c.Inner.ListGenaiScoreConfigsWithResponse(
				cmd.Context(), getInstance(cmd),
				&client.ListGenaiScoreConfigsParams{
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
				cmd, deref(resp.JSON200.Data), scoreConfigColumns,
			)
		},
	}
	page.addTo(cmd)
	return cmd
}

func newGenAIScoreConfigsCreateCmd() *cobra.Command {
	var (
		file        string
		name        string
		dataType    string
		minValue    float32
		maxValue    float32
		description string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a score config",
		Long: `Create a score config.

  oodle genai scores configs create --name helpfulness \
    --data-type NUMERIC --min 0 --max 1

Categorical configs need a category list, which only the file
form can express:

  {
    "name": "sentiment",
    "dataType": "CATEGORICAL",
    "categories": [
      {"label": "positive", "value": 1},
      {"label": "negative", "value": 0}
    ]
  }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiScoreConfigJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			} else {
				if name == "" || dataType == "" {
					return fmt.Errorf(
						"--name and --data-type are required " +
							"unless --file is given",
					)
				}
				body.Name = name
				body.DataType = dataType
				body.Description = optStr(description)
				if cmd.Flags().Changed("min") {
					body.MinValue = &minValue
				}
				if cmd.Flags().Changed("max") {
					body.MaxValue = &maxValue
				}
			}

			resp, err := c.Inner.CreateGenaiScoreConfigWithResponse(
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
				cmd, resp.JSON201, scoreConfigColumns,
			)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the score config",
	)
	cmd.Flags().StringVar(
		&name, "name", "", "Score name this config governs",
	)
	cmd.Flags().StringVar(
		&dataType, "data-type", "",
		"NUMERIC, BOOLEAN, or CATEGORICAL",
	)
	cmd.Flags().Float32Var(
		&minValue, "min", 0, "Minimum allowed value",
	)
	cmd.Flags().Float32Var(
		&maxValue, "max", 0, "Maximum allowed value",
	)
	cmd.Flags().StringVar(
		&description, "description", "",
		"What this score measures",
	)
	return cmd
}
