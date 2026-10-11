package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// genaiTraceWindowPad widens the window of a read by ID. The store
// finds spans by their start time, so a long agent run that started
// before --start is missing spans, or is not found. The ID selects
// the trace, so a wider window does not add other traces.
const genaiTraceWindowPad = 2 * time.Hour

// genaiStep is one step of a GenAI trace: an agent run, a model call
// or a tool call.
type genaiStep struct {
	Step         int     `json:"step"`
	Type         string  `json:"type"`
	Operation    string  `json:"operation"`
	Name         string  `json:"name"`
	StartTime    string  `json:"start_time,omitempty"`
	DurationMs   float64 `json:"duration_ms"`
	Service      string  `json:"service,omitempty"`
	Agent        string  `json:"agent,omitempty"`
	Model        string  `json:"model,omitempty"`
	Tool         string  `json:"tool,omitempty"`
	Error        bool    `json:"error,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	Signal       string  `json:"signal,omitempty"`
	InputTokens  int64   `json:"input_tokens,omitempty"`
	CacheTokens  int64   `json:"cache_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	CostDollars  float64 `json:"cost_dollars,omitempty"`
	Input        string  `json:"input,omitempty"`
	Output       string  `json:"output,omitempty"`

	startUs int64
}

// buildGenAISteps returns the GenAI steps of a trace in time order.
// Spans without a GenAI operation (HTTP, database and framework spans)
// are left out, because they make an agent trace hard to read. A
// failed one stays, so that each error in the summary has a step.
func buildGenAISteps(tr genaiTrace, previewChars int) []genaiStep {
	var steps []genaiStep
	for _, s := range tr.Spans {
		t := tagsOf(s)
		op := spanOperation(t, s)
		if evalOperations[op] || evalOperations[s.OperationName] {
			continue
		}
		isGenAI := t.str("gen_ai.operation.name") != "" || t.str("langfuse.observation.type") != ""
		if !isGenAI && !t.hasError() {
			continue
		}
		st := genaiStep{
			Type:       "SPAN",
			Operation:  op,
			Name:       s.OperationName,
			StartTime:  formatUTC(s.StartTime),
			DurationMs: float64(s.Duration) / 1000,
			startUs:    s.StartTime,
		}
		if p, ok := tr.Processes[s.ProcessID]; ok {
			st.Service = p.ServiceName
		}
		if t.hasError() {
			st.Error = true
			st.ErrorMessage = preview(t.firstStr("error.message", "exception.message"), previewChars)
		}
		if isGenAI {
			st.Type = stepType(op)
			st.Agent = t.str("gen_ai.agent.name")
			st.Model = t.str("gen_ai.request.model")
			st.Tool = t.str("gen_ai.tool.name")
			st.Signal = t.str("oodle.signal.execution")
			if isLeafLLMCall(op) {
				u := usageOf(t)
				st.InputTokens, st.CacheTokens, st.OutputTokens = u.Input, u.CacheRead+u.CacheWrite, u.Output
				st.CostDollars, _ = reportedCost(t)
			}
			if previewChars > 0 {
				if st.Type == "TOOL" {
					st.Input = preview(t.str("gen_ai.tool.call.arguments"), previewChars)
					st.Output = preview(t.str("gen_ai.tool.call.result"), previewChars)
				} else {
					st.Input = previewTail(t.str("gen_ai.input.messages"), previewChars)
					st.Output = preview(t.str("gen_ai.output.messages"), previewChars)
				}
			}
		}
		steps = append(steps, st)
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].startUs < steps[j].startUs })
	for i := range steps {
		steps[i].Step = i + 1
	}
	return steps
}

// preview cuts s to n characters and tells how long the value was.
func preview(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("... (%d chars)", len(r))
}

// previewTail keeps the last n characters. The newest messages of a
// prompt are at its end, and they are usually the part to read.
func previewTail(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return fmt.Sprintf("(%d chars) ...", len(r)) + string(r[len(r)-n:])
}

type genaiStepTableRow struct {
	Step, Offset, Type, Name, Target, Duration, Tokens, Cost, Status string
}

var genaiStepColumns = []output.Column{
	{Header: "STEP", Field: "Step"},
	{Header: "AT", Field: "Offset"},
	{Header: "TYPE", Field: "Type"},
	{Header: "NAME", Field: "Name"},
	{Header: "AGENT/MODEL/TOOL", Field: "Target"},
	{Header: "DURATION", Field: "Duration"},
	{Header: "TOKENS IN/OUT", Field: "Tokens"},
	{Header: "COST", Field: "Cost"},
	{Header: "STATUS", Field: "Status"},
}

func genaiStepTable(steps []genaiStep) []genaiStepTableRow {
	out := make([]genaiStepTableRow, 0, len(steps))
	var first int64
	if len(steps) > 0 {
		first = steps[0].startUs
	}
	for _, s := range steps {
		target := s.Tool
		if target == "" {
			target = s.Model
		}
		if target == "" {
			target = s.Agent
		}
		tokens := ""
		if s.InputTokens+s.CacheTokens+s.OutputTokens > 0 {
			tokens = fmt.Sprintf("%d/%d", s.InputTokens+s.CacheTokens, s.OutputTokens)
		}
		status := "ok"
		if s.Error {
			status = "error"
		}
		if s.Signal != "" {
			status += " " + s.Signal
		}
		out = append(out, genaiStepTableRow{
			Step:     strconv.Itoa(s.Step),
			Offset:   "+" + formatDurationMs(float64(s.startUs-first)/1000),
			Type:     s.Type,
			Name:     s.Name,
			Target:   target,
			Duration: formatDurationMs(s.DurationMs),
			Tokens:   tokens,
			Cost:     formatCost(s.CostDollars),
			Status:   status,
		})
	}
	return out
}

func newGenAITraceCmd() *cobra.Command {
	var (
		// parseRange is set after cmd exists, because the flags need cmd.
		parseRange   func() (start, end int64, err error)
		messages     bool
		previewChars int
		limit        int
	)
	cmd := &cobra.Command{
		Use:   "trace <trace_id>",
		Short: "Show one LLM or agent trace as a list of steps",
		Long: `Show one LLM or agent trace as a list of steps in time order: agent
runs, model calls and tool calls, each with its duration, tokens, cost
and errors. Spans that are not GenAI spans are left out, except the
spans that failed. JSON output also has the summary of the trace.

Add --messages to include a preview of the prompts and completions,
and of the tool arguments and results. A prompt preview keeps the end
of the prompt, where the newest messages are.

The trace must start in the time range (default: the last 24 hours).
The range is widened by 2 hours on each side to catch long runs.
For the raw span tree with all attributes, use 'oodle traces get'.`,
		Example: `  oodle genai trace 4bf92f3577b34da6a3ce929d0e0e4736
  oodle genai trace 4bf92f3577b34da6a3ce929d0e0e4736 --start -7d
  oodle genai trace 4bf92f3577b34da6a3ce929d0e0e4736 --messages --preview-chars 300 -o yaml`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 {
				return fmt.Errorf("--limit must be 1 or more, got %d", limit)
			}
			start, end, err := parseRange()
			if err != nil {
				return err
			}
			pad := genaiTraceWindowPad.Microseconds()
			params := url.Values{}
			params.Set("start", strconv.FormatInt(start-pad, 10))
			params.Set("end", strconv.FormatInt(end+pad, 10))
			if !messages {
				// Leave out the large values, which are most of the
				// size of a long session.
				params.Set("omitLarge", "1")
			}
			id := args[0]
			notFound := fmt.Errorf(
				"no trace %q between %s and %s; the trace must start in the time range, so widen it (for example --start -7d) before you decide that the trace does not exist",
				id, time.UnixMicro(start).UTC().Format(time.RFC3339), time.UnixMicro(end).UTC().Format(time.RFC3339))
			body, err := apiCall{
				method:   http.MethodGet,
				path:     instancePath(cmd, "traces/traces/"+url.PathEscape(id)),
				params:   params,
				notFound: notFound,
			}.do(cmd)
			if err != nil {
				return err
			}
			var resp genaiTracesResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			if len(resp.Data) == 0 {
				return notFound
			}
			tr := resp.Data[0]
			chars := 0
			if messages {
				chars = previewChars
			}
			steps := buildGenAISteps(tr, chars)
			total := len(steps)
			if len(steps) > limit {
				steps = steps[:limit]
			}
			if isTabular(cmd) {
				err = output.Print(cmd.OutOrStdout(), getOutputFormat(cmd), genaiStepTable(steps), genaiStepColumns)
			} else {
				err = printShaped(cmd, struct {
					TraceID string        `json:"trace_id"`
					Summary genaiTraceRow `json:"summary"`
					Steps   []genaiStep   `json:"steps"`
				}{tr.TraceID, summarizeGenAITrace(tr), steps})
			}
			if err != nil {
				return err
			}
			if total > len(steps) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing the first %d of %d steps. Raise --limit to see more.\n", len(steps), total)
			}
			if total == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "Trace %s has no GenAI spans. Use 'oodle traces get' to see all its spans.\n", id)
			}
			return nil
		},
	}
	parseRange = addRangeFlags(cmd, "-24h", parseTimeFlag)
	cmd.Flags().BoolVar(&messages, "messages", false, "Include previews of prompts, completions, tool arguments and tool results")
	cmd.Flags().IntVar(&previewChars, "preview-chars", 1000, "Maximum characters in each preview (with --messages)")
	cmd.Flags().IntVar(&limit, "limit", 500, "Maximum number of steps to show")
	return cmd
}
