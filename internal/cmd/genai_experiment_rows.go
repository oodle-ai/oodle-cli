package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var experimentRowColumns = []output.Column{
	{Header: "ROW", Field: "Id"},
	{Header: "TRACE", Field: "TraceId"},
	{Header: "SCORES", Field: "Scores"},
	{Header: "BASE", Field: "Base"},
	{Header: "ERROR", Field: "Error"},
}

// experimentRowLine is one row of the table. The API sends the
// scores as a list and the base scores as a map. A table cell
// holds one string, so both are written as "name=value" pairs.
type experimentRowLine struct {
	Id      string
	TraceId string
	Scores  string
	Base    string
	Error   string
}

func newGenAIExperimentsRowsCmd() *cobra.Command {
	var (
		limit     int
		cursor    string
		compareTo string
		moved     string
		movedKind string
	)
	cmd := &cobra.Command{
		Use:   "rows <experiment-id>",
		Short: "List an experiment's rows, compared with a base when given",
		Long: `List an experiment's rows with the newest score of each name.

With --compare-to, each row also shows the base experiment's
scores for the same input. With --moved, only the rows whose
score moved that way are listed; these are the rows behind the
"better" and "worse" counts of the summary. With --moved-kind
metric, --moved names a row metric such as duration, where lower
is better.

  oodle genai experiments rows <id>
  oodle genai experiments rows <id> --compare-to <base-id>
  oodle genai experiments rows <id> --compare-to <base-id> \
    --moved accuracy:worse
  oodle genai experiments rows <id> --compare-to <base-id> \
    --moved duration:worse --moved-kind metric`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if moved != "" {
				if compareTo == "" {
					return fmt.Errorf("--moved needs --compare-to")
				}
				if err := checkMoved(moved); err != nil {
					return err
				}
			}
			switch movedKind {
			case "", "score", "metric":
			default:
				return fmt.Errorf("--moved-kind must be score or metric, not %q", movedKind)
			}
			if movedKind != "" && moved == "" {
				return fmt.Errorf("--moved-kind needs --moved")
			}
			params := &client.ListGenaiExperimentRowsParams{
				Cursor:    optStr(cursor),
				CompareTo: optStr(compareTo),
				Moved:     optStr(moved),
				MovedKind: optStr(movedKind),
			}
			if limit > 0 {
				params.Limit = &limit
			}
			c := getClient(cmd)
			resp, err := c.Inner.ListGenaiExperimentRowsWithResponse(
				cmd.Context(), getInstance(cmd), args[0], params,
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
			// JSON and YAML keep the envelope: the cursor and the
			// moved count are not in the rows.
			if !isTabular(cmd) {
				return printPlain(cmd, resp.JSON200)
			}
			rows := deref(resp.JSON200.Data)
			lines := make([]experimentRowLine, 0, len(rows))
			for _, r := range rows {
				lines = append(lines, rowLine(r))
			}
			if err := printGenAI(cmd, lines, experimentRowColumns); err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			if n := resp.JSON200.MovedTotal; n != nil {
				fmt.Fprintf(errOut, "%d inputs moved %s\n",
					*n, moved[strings.LastIndex(moved, ":")+1:])
			}
			if next := deref(resp.JSON200.Cursor); next != "" {
				fmt.Fprintf(errOut, "Next page: --cursor %s\n", next)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0,
		"Results per page (server default 50, max 200)")
	cmd.Flags().StringVar(&cursor, "cursor", "",
		"The cursor that the previous page printed")
	cmd.Flags().StringVar(&compareTo, "compare-to", "",
		"The id of a base experiment to compare each row with")
	cmd.Flags().StringVar(&moved, "moved", "",
		"<score-name>:better or <score-name>:worse; needs --compare-to")
	cmd.Flags().StringVar(&movedKind, "moved-kind", "",
		"What --moved names: score (the default) or metric, such as duration")
	return cmd
}

// checkMoved refuses a value the API would refuse, so the user
// sees the expected form before a request is sent. The name can
// hold a colon, so the direction is after the last one.
func checkMoved(v string) error {
	i := strings.LastIndex(v, ":")
	if i <= 0 {
		return fmt.Errorf("--moved must be <score-name>:better or <score-name>:worse")
	}
	switch v[i+1:] {
	case "better", "worse":
		return nil
	}
	return fmt.Errorf("--moved must end in :better or :worse, not %q", v[i+1:])
}

func rowLine(r client.ExperimentRowView) experimentRowLine {
	line := experimentRowLine{
		Id:      r.Id,
		TraceId: r.TraceId,
		Error:   deref(r.ErrorMessage),
	}
	var scores []string
	for _, s := range deref(r.Scores) {
		switch {
		case s.Value != nil:
			scores = append(scores, s.Name+"="+formatScore(float64(*s.Value)))
		case s.StringValue != nil:
			scores = append(scores, s.Name+"="+*s.StringValue)
		}
	}
	sort.Strings(scores)
	line.Scores = strings.Join(scores, " ")
	if cmp := r.Comparison; cmp != nil {
		if !cmp.Matched {
			line.Base = "(no match)"
		} else {
			base := deref(cmp.BaseScores)
			names := make([]string, 0, len(base))
			for name := range base {
				names = append(names, name)
			}
			sort.Strings(names)
			parts := make([]string, 0, len(names))
			for _, name := range names {
				parts = append(parts, name+"="+formatScore(float64(base[name])))
			}
			line.Base = strings.Join(parts, " ")
		}
	}
	return line
}

func formatScore(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 32)
}
