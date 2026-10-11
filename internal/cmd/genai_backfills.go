package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var backfillColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "STATUS", Field: "Status"},
	{Header: "START", Field: "Start"},
	{Header: "END", Field: "End"},
	{Header: "EVALUATORS", Field: "Evaluators"},
	{Header: "BUCKETS", Field: "Buckets"},
	{Header: "CREATED", Field: "CreatedAt"},
}

// backfillRow is the table form of a run. The window and the
// evaluators are in the run's config, which a table cannot show
// as a nested object.
type backfillRow struct {
	Name       string
	Id         string
	Status     string
	Start      string
	End        string
	Evaluators string
	Buckets    int
	CreatedAt  string
}

func toBackfillRow(r client.BackfillRun) backfillRow {
	status := r.Status
	if e := deref(r.Error); e != "" {
		status += ": " + e
	}
	return backfillRow{
		Name:       r.Config.Name,
		Id:         r.Id,
		Status:     status,
		Start:      microsToRFC3339(int64(r.Config.StartTime)),
		End:        microsToRFC3339(int64(r.Config.EndTime)),
		Evaluators: strings.Join(deref(r.Config.EvaluatorIds), ", "),
		Buckets:    r.TotalBuckets,
		CreatedAt:  r.CreatedAt,
	}
}

func microsToRFC3339(us int64) string {
	if us <= 0 {
		return ""
	}
	return time.UnixMicro(us).UTC().Format(time.RFC3339)
}

func newGenAIBackfillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backfills",
		Aliases: []string{"backfill"},
		Short:   "Run evaluators over past traffic",
		Long: `Run evaluators over traces that already arrived, and follow
the runs. A backfill reads the spans of a past time window and
scores them with the evaluators that you give.

  oodle genai backfills create --name "Refund check, last week" \
    --evaluator-id <evaluator-id> --start -7d
  oodle genai backfills list
  oodle genai backfills get <backfill-id>
  oodle genai backfills cancel <backfill-id>

When an evaluator of the run reads the scores of other
evaluators, the run adds those evaluators too. The create
result names them in "alsoRuns".`,
	}

	cmd.AddCommand(newGenAIBackfillsListCmd())
	cmd.AddCommand(newGenAIBackfillsGetCmd())
	cmd.AddCommand(newGenAIBackfillsCreateCmd())
	cmd.AddCommand(newGenAIBackfillsCancelCmd())
	cmd.AddCommand(newGenAIBackfillsDeleteCmd())

	return cmd
}

// printBackfill prints one run as a table row, or the full run
// for JSON and YAML.
func printBackfill(cmd *cobra.Command, r *client.BackfillRun) error {
	if isTabular(cmd) {
		return printGenAI(cmd, toBackfillRow(*r), backfillColumns)
	}
	return printPlain(cmd, r)
}

func newGenAIBackfillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List backfill runs",
		Long: `List the backfill runs of the instance, with their status.

For table output, the number of active runs and the limit of
runs at the same time go to standard error. -o json and -o yaml
print them as "active" and "maxConcurrent".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient(cmd).Inner.ListGenaiBackfillsWithResponse(
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
			if !isTabular(cmd) {
				return printPlain(cmd, resp.JSON200)
			}
			runs := deref(resp.JSON200.Data)
			rows := make([]backfillRow, len(runs))
			for i, r := range runs {
				rows[i] = toBackfillRow(r)
			}
			if err := printGenAI(cmd, rows, backfillColumns); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%d of at most %d runs active\n",
				resp.JSON200.Active, resp.JSON200.MaxConcurrent)
			return nil
		},
	}
}

func newGenAIBackfillsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <backfill-id>",
		Short: "Get a backfill run",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient(cmd).Inner.GetGenaiBackfillWithResponse(
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
			return printBackfill(cmd, resp.JSON200)
		},
	}
}

func newGenAIBackfillsCreateCmd() *cobra.Command {
	var (
		file         string
		name         string
		evaluatorIDs []string
		startStr     string
		endStr       string
		sampleRate   float64
		bucket       time.Duration
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Start a backfill run",
		Long: `Start a backfill run: score the spans of a past time window
with one or more evaluators.

  oodle genai backfills create --name "Refund check, last week" \
    --evaluator-id <evaluator-id> --start -7d --sample-rate 0.1

--start and --end take a relative time (-7d, -12h), "now" or
an epoch (s, ms, µs or ns). --end is "now" when you do not set it. The
window cannot end in the future and is at most 90 days.

--sample-rate reads that fraction of the spans (default 1, all
of them). --bucket sets how often the run reports its progress
(default and minimum 1h); the window must be at least one bucket.

-f gives the request as a JSON or YAML file. The flags replace
the values from the file. Use the file for "filters", extra
span filters in the shape of the trace query APIs:

  name: Refund check, last week
  evaluatorIds: [<evaluator-id>]
  startTime: 1759000000000000
  endTime: 1759600000000000
  sampleRate: 0.1

The run is queued and returns at once. Follow it with
` + "`oodle genai backfills get <backfill-id>`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var body client.CreateGenaiBackfillJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			}
			if name != "" {
				body.Name = name
			}
			if len(evaluatorIDs) > 0 {
				ids := append([]string{}, evaluatorIDs...)
				body.EvaluatorIds = &ids
			}
			if startStr != "" {
				v, err := parseTimeFlag(startStr)
				if err != nil {
					return fmt.Errorf("--start: %w", err)
				}
				body.StartTime = int(v)
			}
			if endStr != "" {
				v, err := parseTimeFlag(endStr)
				if err != nil {
					return fmt.Errorf("--end: %w", err)
				}
				body.EndTime = int(v)
			} else if body.EndTime == 0 {
				body.EndTime = int(time.Now().UnixMicro())
			}
			if cmd.Flags().Changed("sample-rate") {
				r := float32(sampleRate)
				body.SampleRate = &r
			}
			if cmd.Flags().Changed("bucket") {
				s := int(bucket / time.Second)
				body.BucketSeconds = &s
			}

			switch {
			case body.Name == "":
				return fmt.Errorf("the run needs a name: set --name or name in the file")
			case len(deref(body.EvaluatorIds)) == 0:
				return fmt.Errorf(
					"give at least one evaluator: --evaluator-id or evaluatorIds in the file",
				)
			case body.StartTime == 0:
				return fmt.Errorf("the run needs a start: set --start or startTime in the file")
			}

			resp, err := getClient(cmd).Inner.CreateGenaiBackfillWithResponse(
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
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			if isTabular(cmd) {
				for _, a := range deref(resp.JSON200.AlsoRuns) {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"Also runs %s (%s): %s reads its scores.\n",
						a.Name, a.Id, strings.Join(deref(a.ReadBy), ", "))
				}
			}
			return printBackfill(cmd, resp.JSON200)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&file, "file", "f", "", "JSON or YAML file with the request")
	f.StringVar(&name, "name", "", "Name of the run")
	f.StringSliceVar(&evaluatorIDs, "evaluator-id", nil, "Evaluator to run (repeatable)")
	f.StringVar(&startStr, "start", "", "Start of the window (e.g. -7d, RFC3339, or epoch s/ms/µs/ns)")
	f.StringVar(&endStr, "end", "", "End of the window (default now)")
	f.Float64Var(&sampleRate, "sample-rate", 1, "Fraction of the spans to read, above 0 and at most 1")
	f.DurationVar(&bucket, "bucket", time.Hour, "How often the run reports progress, at least 1h")
	return cmd
}

func newGenAIBackfillsCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <backfill-id>",
		Short: "Cancel a queued or running backfill",
		Long: `Cancel a queued or running backfill. A run that is finished
cannot be cancelled (400).`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient(cmd).Inner.CancelGenaiBackfillWithResponse(
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
			return printBackfill(cmd, resp.JSON200)
		},
	}
}

func newGenAIBackfillsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <backfill-id>",
		Short: "Delete a finished backfill run from the list",
		Long: `Delete a finished backfill run from the list. A run that is
queued or running cannot be deleted (400): cancel it first.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirmAction(fmt.Sprintf(
				"Delete backfill run %s?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := getClient(cmd).Inner.DeleteGenaiBackfillWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				// The server refuses a run that is still going.
				// Its message does not say what to do instead.
				var apiErr *api.APIError
				if resp.StatusCode() == http.StatusBadRequest &&
					errors.As(err, &apiErr) && apiErr.Remedy == "" {
					apiErr.Remedy = "a queued or running run cannot be " +
						"deleted: cancel it first with `oodle genai " +
						"backfills cancel " + args[0] + "`"
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted backfill run %s\n", args[0])
			return nil
		},
	}
}
