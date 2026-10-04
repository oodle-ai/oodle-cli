package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// errTestRunFailed is returned after a run whose code failed, so
// that a script or an agent sees the failure in the exit status.
var errTestRunFailed = errors.New("the test run failed")

var testRunScoreColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "VALUE", Field: "Value"},
	{Header: "TYPE", Field: "Type"},
	{Header: "COMMENT", Field: "Comment"},
}

type testRunScoreRow struct {
	Name    string
	Value   string
	Type    string
	Comment string
}

func newGenAITemplatesTestCmd() *cobra.Command {
	var (
		span       string
		trace      string
		spanData   string
		paramsFile string
		scoresFile string
		startStr   string
		endStr     string
	)
	cmd := &cobra.Command{
		Use:   "test <dir>",
		Short: "Run a local template directory on one span",
		Long: `Run the code of a directory that ` + "`templates pull`" + ` wrote on one
span, and save nothing. The run uses evaluate.py, the params
and libraryPins of template.yaml, and each file in shared/ that
is new or changed since the pull, in place of the stored
library. It prints the scores, the error and the logs of the
code.

Pick the span with one of:

  --span <trace-id>:<span-id>   a span of your traffic
  --trace <trace-id>            the root span of a trace
  --span-file <file>            a span in a JSON or YAML file,
                                captured earlier or written by
                                hand, for a run with no traffic:
                                {span_id, trace_id, tags: {...}}

  oodle genai templates test ./refund-check --trace 4bf92f35...
  oodle genai templates test . --span 4bf92f35...:00f067aa... \
    --params params.yaml --scores scores.yaml

--params gives the values of the settings, by name. --scores
gives the scores of other evaluators that the code reads through
ctx.scores, by evaluator name:

  Helpfulness:
    - {name: Helpfulness, value: 0.8, data_type: NUMERIC}

--start and --end set the time window to find the span in (the
server default is the last 24 hours; --trace uses the last 7
days). The command exits with an error when the code fails.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace(args[0])
			if err != nil {
				return err
			}
			chosen := 0
			for _, v := range []string{span, trace, spanData} {
				if v != "" {
					chosen++
				}
			}
			if chosen != 1 {
				return fmt.Errorf("give one of --span, --trace or --span-file")
			}

			for _, warning := range ws.pinWarnings() {
				fmt.Fprintln(cmd.ErrOrStderr(), "Warning: "+warning)
			}
			// Settings and pins are always sent, empty when
			// template.yaml has none. An absent field would let
			// the stored template's values fill in, and the run
			// would not test the local files.
			body := map[string]any{
				"sourceCode":  ws.source,
				"paramSpecs":  []any{},
				"libraryPins": map[string]any{},
			}
			if p, ok := ws.fields["params"]; ok {
				body["paramSpecs"] = p
			}
			if p, ok := ws.fields["libraryPins"]; ok {
				body["libraryPins"] = p
			}
			created, changed := ws.changedLibraries()
			if drafts := append(created, changed...); len(drafts) > 0 {
				libs := map[string]string{}
				for _, name := range drafts {
					libs[name] = ws.shared[name]
				}
				body["libraries"] = libs
			}
			if paramsFile != "" {
				var v map[string]any
				if err := readInputFile(paramsFile, &v); err != nil {
					return err
				}
				body["params"] = v
			}
			if scoresFile != "" {
				var v map[string]any
				if err := readInputFile(scoresFile, &v); err != nil {
					return err
				}
				body["scores"] = v
			}
			if err := setWindow(body, startStr, endStr); err != nil {
				return err
			}

			switch {
			case span != "":
				traceID, spanID, ok := splitSpanRef(span)
				if !ok {
					return fmt.Errorf("--span must be <trace-id>:<span-id>")
				}
				body["traceId"], body["spanId"] = traceID, spanID
			case trace != "":
				if startStr == "" {
					if err := setWindow(body, "-7d", endStr); err != nil {
						return err
					}
				}
				spanID, err := rootSpanID(cmd, trace, body)
				if err != nil {
					return err
				}
				body["traceId"], body["spanId"] = trace, spanID
			default:
				var v map[string]any
				if err := readInputFile(spanData, &v); err != nil {
					return err
				}
				body["spanData"] = v
			}

			// A stored template fills only what the body leaves
			// out; "test" is the id for a draft with none.
			id := ws.meta.ID
			if id == "" {
				id = "test"
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				return err
			}
			resp, err := getClient(cmd).Inner.TestRunGenaiEvaluatorWithBodyWithResponse(
				cmd.Context(), getInstance(cmd), id,
				"application/json", bytes.NewReader(encoded),
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(resp.StatusCode(), resp.HTTPResponse, resp.Body); err != nil {
				return err
			}
			// The body is decoded by hand: the generated type
			// cannot hold an error object, and a failed run is a
			// 200 that carries one.
			var result map[string]any
			if err := json.Unmarshal(resp.Body, &result); err != nil {
				return fmt.Errorf("decoding the test run: %w", err)
			}
			if explicitStructuredOutput(cmd) {
				if err := printPlain(cmd, result); err != nil {
					return err
				}
			} else if err := writeTestRun(cmd, result); err != nil {
				return err
			}
			if result["error"] != nil {
				cmd.SilenceUsage = true
				return errTestRunFailed
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&span, "span", "", "Span to run on: <trace-id>:<span-id>")
	f.StringVar(&trace, "trace", "", "Run on the root span of this trace")
	f.StringVar(&spanData, "span-file", "", "JSON or YAML file with a span to run on (sent as spanData)")
	f.StringVar(&paramsFile, "params", "", "JSON or YAML file with the setting values")
	f.StringVar(&scoresFile, "scores", "", "JSON or YAML file with other evaluators' scores")
	f.StringVar(&startStr, "start", "", "Start of the window to find the span in (e.g. -24h)")
	f.StringVar(&endStr, "end", "", "End of the window to find the span in (default now)")
	return cmd
}

// setWindow puts the span search window in the body, in epoch
// microseconds.
func setWindow(body map[string]any, startStr, endStr string) error {
	if startStr == "" && endStr == "" {
		return nil
	}
	end := time.Now().UnixMicro()
	if endStr != "" {
		v, err := parseTimeFlag(endStr)
		if err != nil {
			return fmt.Errorf("--end: %w", err)
		}
		end = v
	}
	start := end - (24 * time.Hour).Microseconds()
	if startStr != "" {
		v, err := parseTimeFlag(startStr)
		if err != nil {
			return fmt.Errorf("--start: %w", err)
		}
		start = v
	}
	body["startTimeUs"], body["endTimeUs"] = start, end
	return nil
}

// rootSpanID finds the span with no parent in the trace, the
// earliest one when there are more.
func rootSpanID(cmd *cobra.Command, traceID string, body map[string]any) (string, error) {
	start, _ := body["startTimeUs"].(int64)
	end, _ := body["endTimeUs"].(int64)
	resp, err := getClient(cmd).Inner.GetTracesByIdWithResponse(
		cmd.Context(), getInstance(cmd), traceID,
		&client.GetTracesByIdParams{Start: start, End: end},
	)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	if err := genaiCheck(resp.StatusCode(), resp.HTTPResponse, resp.Body); err != nil {
		return "", err
	}
	var spans []client.TraceSpan
	if resp.JSON200 != nil {
		for _, t := range deref(resp.JSON200.Data) {
			spans = append(spans, deref(t.Spans)...)
		}
	}
	ids := map[string]bool{}
	for _, s := range spans {
		ids[s.SpanID] = true
	}
	var roots []client.TraceSpan
	for _, s := range spans {
		if s.ParentSpanID == "" || !ids[s.ParentSpanID] {
			roots = append(roots, s)
		}
	}
	if len(roots) == 0 {
		return "", fmt.Errorf(
			"trace %s has no spans in the window; set --start or use --span",
			traceID,
		)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].StartTime < roots[j].StartTime })
	return roots[0].SpanID, nil
}

func writeTestRun(cmd *cobra.Command, result map[string]any) error {
	w := cmd.OutOrStdout()
	if e := result["error"]; e != nil {
		switch v := e.(type) {
		case map[string]any:
			fmt.Fprintf(w, "Error: %v: %v\n", v["code"], v["message"])
		default:
			fmt.Fprintf(w, "Error: %v\n", v)
		}
	}
	if scores, ok := result["scores"].([]any); ok && len(scores) > 0 {
		rows := make([]testRunScoreRow, 0, len(scores))
		for _, s := range scores {
			m, _ := s.(map[string]any)
			value, _ := json.Marshal(m["value"])
			row := testRunScoreRow{Value: string(value)}
			row.Name, _ = m["name"].(string)
			row.Type, _ = m["data_type"].(string)
			row.Comment, _ = m["comment"].(string)
			rows = append(rows, row)
		}
		if err := output.Print(w, output.FormatTable, rows, testRunScoreColumns); err != nil {
			return err
		}
	} else if result["error"] == nil {
		fmt.Fprintln(w, "The code returned no scores.")
	}
	if d, ok := result["duration_ms"].(float64); ok {
		fmt.Fprintf(w, "Duration: %.0f ms\n", d)
	}
	if logs, _ := result["logs"].(string); strings.TrimSpace(logs) != "" {
		fmt.Fprintf(w, "Logs:\n%s", withNewline(logs))
	}
	return nil
}

// splitSpanRef splits "<trace-id>:<span-id>" at the last colon. A
// trace id can itself contain a colon; a span id never does, so the
// first colon would cut the trace id short and the span would not be
// found.
func splitSpanRef(ref string) (traceID, spanID string, ok bool) {
	i := strings.LastIndex(ref, ":")
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}
