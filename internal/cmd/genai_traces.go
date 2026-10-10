package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	genaiTracesDefaultLimit = 50
	genaiTracesMaxLimit     = 500
)

// genaiTraceRow is the summary of one GenAI trace.
type genaiTraceRow struct {
	TraceID                string       `json:"trace_id"`
	StartTime              string       `json:"start_time,omitempty"`
	DurationMs             float64      `json:"duration_ms"`
	Generations            int          `json:"generations"`
	ToolCalls              int          `json:"tool_calls"`
	Errors                 int          `json:"errors"`
	InputTokens            int64        `json:"input_tokens"`
	CacheReadTokens        int64        `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens       int64        `json:"cache_write_tokens,omitempty"`
	OutputTokens           int64        `json:"output_tokens"`
	TotalTokens            int64        `json:"total_tokens"`
	CostDollars            float64      `json:"cost_dollars,omitempty"`
	GenerationsWithoutCost int          `json:"generations_without_cost,omitempty"`
	Services               []string     `json:"services,omitempty"`
	Agents                 []string     `json:"agents,omitempty"`
	Models                 []string     `json:"models,omitempty"`
	Tools                  []string     `json:"tools,omitempty"`
	Signals                []string     `json:"signals,omitempty"`
	SessionID              string       `json:"session_id,omitempty"`
	UserID                 string       `json:"user_id,omitempty"`
	Scores                 []genaiScore `json:"scores,omitempty"`

	startUs int64
}

// summarizeGenAITrace makes the summary row of a trace. Tokens and
// cost come only from leaf model calls (see isLeafLLMCall). Spans of
// the evaluation pipeline give only their scores.
func summarizeGenAITrace(tr genaiTrace) genaiTraceRow {
	row := genaiTraceRow{TraceID: tr.TraceID}
	var usage tokenUsage
	sets := map[string]map[string]bool{
		"services": {}, "agents": {}, "models": {}, "tools": {}, "signals": {},
	}
	var first, last int64
	for _, s := range tr.Spans {
		t := tagsOf(s)
		op := spanOperation(t, s)
		if evalOperations[op] || evalOperations[s.OperationName] {
			if sc, ok := scoreOf(t); ok {
				row.Scores = append(row.Scores, sc)
			}
			continue
		}
		if s.StartTime > 0 {
			if first == 0 || s.StartTime < first {
				first = s.StartTime
			}
			last = max(last, s.StartTime+s.Duration)
		}
		for key, v := range map[string]string{
			"agents":  t.str("gen_ai.agent.name"),
			"models":  t.str("gen_ai.request.model"),
			"tools":   t.str("gen_ai.tool.name"),
			"signals": t.str("oodle.signal.execution"),
		} {
			if v != "" {
				sets[key][v] = true
			}
		}
		if p, ok := tr.Processes[s.ProcessID]; ok && p.ServiceName != "" {
			sets["services"][p.ServiceName] = true
		}
		if row.SessionID == "" {
			row.SessionID = t.firstStr("gen_ai.conversation.id", "gen_ai.session.id")
		}
		if row.UserID == "" {
			row.UserID = t.str("user.id")
		}
		if t.hasError() {
			row.Errors++
		}
		if stepType(op) == "TOOL" {
			row.ToolCalls++
		}
		if isLeafLLMCall(op) {
			row.Generations++
			usage.add(usageOf(t))
			if c, ok := reportedCost(t); ok {
				row.CostDollars += c
			} else {
				row.GenerationsWithoutCost++
			}
		}
	}
	row.startUs = first
	row.StartTime = formatUTC(first)
	if first > 0 {
		row.DurationMs = float64(last-first) / 1000
	}
	row.InputTokens, row.CacheReadTokens, row.CacheWriteTokens, row.OutputTokens =
		usage.Input, usage.CacheRead, usage.CacheWrite, usage.Output
	row.TotalTokens = usage.total()
	row.Services = sortedKeys(sets["services"])
	row.Agents = sortedKeys(sets["agents"])
	row.Models = sortedKeys(sets["models"])
	row.Tools = sortedKeys(sets["tools"])
	row.Signals = sortedKeys(sets["signals"])
	return row
}

// genaiGroupRow is the sum of the traces of one session or user.
type genaiGroupRow struct {
	Key         string   `json:"-"`
	SessionID   string   `json:"session_id,omitempty"`
	UserID      string   `json:"user_id,omitempty"`
	Traces      int      `json:"traces"`
	TraceIDs    []string `json:"trace_ids"`
	FirstSeen   string   `json:"first_seen,omitempty"`
	LastSeen    string   `json:"last_seen,omitempty"`
	Generations int      `json:"generations"`
	ToolCalls   int      `json:"tool_calls"`
	Errors      int      `json:"errors"`
	TotalTokens int64    `json:"total_tokens"`
	CostDollars float64  `json:"cost_dollars,omitempty"`
	Agents      []string `json:"agents,omitempty"`
	Models      []string `json:"models,omitempty"`
	Tools       []string `json:"tools,omitempty"`

	lastUs int64
}

// groupGenAIRows adds up trace rows by session or user. A trace with
// no ID for the key is left out: an "unknown" group of unrelated
// traces reads as one real conversation.
func groupGenAIRows(rows []genaiTraceRow, by string) []genaiGroupRow {
	groups := map[string]*genaiGroupRow{}
	sets := map[string]map[string]map[string]bool{}
	var order []string
	for _, r := range rows {
		key := r.SessionID
		if by == "user" {
			key = r.UserID
		}
		if key == "" {
			continue
		}
		g, ok := groups[key]
		if !ok {
			g = &genaiGroupRow{Key: key}
			if by == "user" {
				g.UserID = key
			} else {
				g.SessionID = key
			}
			groups[key] = g
			sets[key] = map[string]map[string]bool{"agents": {}, "models": {}, "tools": {}}
			order = append(order, key)
		}
		g.Traces++
		g.TraceIDs = append(g.TraceIDs, r.TraceID)
		g.Generations += r.Generations
		g.ToolCalls += r.ToolCalls
		g.Errors += r.Errors
		g.TotalTokens += r.TotalTokens
		g.CostDollars += r.CostDollars
		if by == "session" && g.UserID == "" {
			g.UserID = r.UserID
		}
		for name, vs := range map[string][]string{"agents": r.Agents, "models": r.Models, "tools": r.Tools} {
			for _, v := range vs {
				sets[key][name][v] = true
			}
		}
		if r.StartTime != "" {
			if g.FirstSeen == "" || r.StartTime < g.FirstSeen {
				g.FirstSeen = r.StartTime
			}
			if r.StartTime > g.LastSeen {
				g.LastSeen = r.StartTime
				g.lastUs = r.startUs
			}
		}
	}
	out := make([]genaiGroupRow, 0, len(order))
	for _, key := range order {
		g := groups[key]
		g.Agents = sortedKeys(sets[key]["agents"])
		g.Models = sortedKeys(sets[key]["models"])
		g.Tools = sortedKeys(sets[key]["tools"])
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

// genaiTraceTableRow is a trace row as table cells.
type genaiTraceTableRow struct {
	TraceID, Start, Duration, Agents, Models, Calls, Tools, Tokens, Cost, Errors string
}

var genaiTraceColumns = []output.Column{
	{Header: "TRACE ID", Field: "TraceID"},
	{Header: "START (UTC)", Field: "Start"},
	{Header: "DURATION", Field: "Duration"},
	{Header: "AGENTS", Field: "Agents"},
	{Header: "MODELS", Field: "Models"},
	{Header: "LLM CALLS", Field: "Calls"},
	{Header: "TOOL CALLS", Field: "Tools"},
	{Header: "TOKENS", Field: "Tokens"},
	{Header: "COST", Field: "Cost"},
	{Header: "ERRORS", Field: "Errors"},
}

func genaiTraceTable(rows []genaiTraceRow) []genaiTraceTableRow {
	out := make([]genaiTraceTableRow, 0, len(rows))
	for _, r := range rows {
		start := r.StartTime
		if r.startUs > 0 {
			start = time.UnixMicro(r.startUs).UTC().Format("2006-01-02 15:04:05")
		}
		out = append(out, genaiTraceTableRow{
			TraceID:  r.TraceID,
			Start:    start,
			Duration: formatDurationMs(r.DurationMs),
			Agents:   joinCell(r.Agents),
			Models:   joinCell(r.Models),
			Calls:    strconv.Itoa(r.Generations),
			Tools:    strconv.Itoa(r.ToolCalls),
			Tokens:   strconv.FormatInt(r.TotalTokens, 10),
			Cost:     formatCost(r.CostDollars),
			Errors:   strconv.Itoa(r.Errors),
		})
	}
	return out
}

// genaiGroupTableRow is a session or user row as table cells.
type genaiGroupTableRow struct {
	Key, User, Traces, LastSeen, Agents, Calls, Tokens, Cost, Errors string
}

func genaiGroupTable(rows []genaiGroupRow) []genaiGroupTableRow {
	out := make([]genaiGroupTableRow, 0, len(rows))
	for _, g := range rows {
		last := g.LastSeen
		if g.lastUs > 0 {
			last = time.UnixMicro(g.lastUs).UTC().Format("2006-01-02 15:04:05")
		}
		user := ""
		if g.SessionID != "" {
			user = g.UserID
		}
		out = append(out, genaiGroupTableRow{
			Key:      g.Key,
			User:     user,
			Traces:   strconv.Itoa(g.Traces),
			LastSeen: last,
			Agents:   joinCell(g.Agents),
			Calls:    strconv.Itoa(g.Generations),
			Tokens:   strconv.FormatInt(g.TotalTokens, 10),
			Cost:     formatCost(g.CostDollars),
			Errors:   strconv.Itoa(g.Errors),
		})
	}
	return out
}

func genaiGroupColumns(by string) []output.Column {
	cols := []output.Column{{Header: strings.ToUpper(by) + " ID", Field: "Key"}}
	if by == "session" {
		cols = append(cols, output.Column{Header: "USER", Field: "User"})
	}
	return append(cols,
		output.Column{Header: "TRACES", Field: "Traces"},
		output.Column{Header: "LAST SEEN (UTC)", Field: "LastSeen"},
		output.Column{Header: "AGENTS", Field: "Agents"},
		output.Column{Header: "LLM CALLS", Field: "Calls"},
		output.Column{Header: "TOKENS", Field: "Tokens"},
		output.Column{Header: "COST", Field: "Cost"},
		output.Column{Header: "ERRORS", Field: "Errors"},
	)
}

func newGenAITracesCmd() *cobra.Command {
	var (
		filters     genaiFilterFlags
		startStr    string
		endStr      string
		errorsOnly  bool
		search      string
		minDuration string
		maxDuration string
		minTokens   int
		maxTokens   int
		groupBy     string
		limit       int
	)
	cmd := &cobra.Command{
		Use:   "traces",
		Short: "Search LLM and agent traces",
		Long: `Search LLM and agent traces. Each row is the summary of one trace:
agents, models, model calls, tool calls, tokens, cost and errors.

Only traces with GenAI spans (spans with gen_ai.operation.name) are
read. Tokens and cost come only from the model calls, not from the
agent spans that repeat the counts of their children. COST is the
cost that your instrumentation reported; the JSON field
generations_without_cost counts the model calls that reported none.

The default time range is the last 6 hours. Each filter flag can be
given more than once, and a trace matches when a span has any of the
values. Run 'oodle genai values' to find the names to filter by: a
wrong name and an idle agent both give an empty result.

Use --group-by session or --group-by user to add up the traces of
each conversation or each end user.

For the full span tree of a trace, use 'oodle genai trace <id>' or
'oodle traces get <id>'.`,
		Example: `  oodle genai traces
  oodle genai traces --agent planner --start -24h --limit 20
  oodle genai traces --model gpt-4o --model gpt-4o-mini --errors
  oodle genai traces --signal failure.tool_error --signal failure.invalid_args
  oodle genai traces --attr tenant=acme --min-tokens 50000
  oodle genai traces --group-by session --start -7d -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > genaiTracesMaxLimit {
				return fmt.Errorf("--limit must be between 1 and %d, got %d", genaiTracesMaxLimit, limit)
			}
			groupBy = strings.ToLower(groupBy)
			switch groupBy {
			case "trace", "session", "user":
			default:
				return fmt.Errorf("--group-by must be trace, session or user, got %q", groupBy)
			}
			start, err := parseTimeFlag(startStr)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			end, err := parseTimeFlag(endStr)
			if err != nil {
				return fmt.Errorf("--end: %w", err)
			}
			matchers, err := filters.matchers()
			if err != nil {
				return err
			}
			if errorsOnly {
				matchers = append(matchers, traceMatcher{Name: "span_status", Type: 0, Value: "Error"})
			}
			encoded, err := json.Marshal(matchers)
			if err != nil {
				return fmt.Errorf("encoding filters: %w", err)
			}

			params := url.Values{}
			params.Set("start", strconv.FormatInt(start, 10))
			params.Set("end", strconv.FormatInt(end, 10))
			// One more trace than the limit tells if more traces match.
			params.Set("limit", strconv.Itoa(limit+1))
			params.Set("extraFields", genaiExtraFields)
			params.Set("filters", string(encoded))
			if search != "" {
				params.Set("search", search)
			}
			for flag, param := range map[string]string{
				"min-duration": "minDuration", "max-duration": "maxDuration",
				"min-tokens": "minTokens", "max-tokens": "maxTokens",
			} {
				if cmd.Flags().Changed(flag) {
					params.Set(param, cmd.Flags().Lookup(flag).Value.String())
				}
			}

			body, err := genaiGet(cmd, "traces/traces", params, nil)
			if err != nil {
				return err
			}
			var resp genaiTracesResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}

			more := len(resp.Data) > limit
			if more {
				resp.Data = resp.Data[:limit]
			}
			rows := make([]genaiTraceRow, 0, len(resp.Data))
			for _, tr := range resp.Data {
				rows = append(rows, summarizeGenAITrace(tr))
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].startUs > rows[j].startUs })

			startT, endT := time.UnixMicro(start), time.UnixMicro(end)
			result := map[string]any{
				"start_time": startT.UTC().Format(time.RFC3339),
				"end_time":   endT.UTC().Format(time.RFC3339),
				"group_by":   groupBy,
			}
			format := getOutputFormat(cmd)
			returned := len(rows)
			if groupBy == "trace" {
				result["traces"] = rows
				if isTabular(cmd) {
					err = output.Print(cmd.OutOrStdout(), format, genaiTraceTable(rows), genaiTraceColumns)
				}
			} else {
				groups := groupGenAIRows(rows, groupBy)
				returned = len(groups)
				result[groupBy+"s"] = groups
				result["traces_scanned"] = len(rows)
				if isTabular(cmd) {
					err = output.Print(cmd.OutOrStdout(), format, genaiGroupTable(groups), genaiGroupColumns(groupBy))
				}
			}
			if !isTabular(cmd) {
				err = printShaped(cmd, result)
			}
			if err != nil {
				return err
			}

			stderr := cmd.ErrOrStderr()
			if returned == 0 {
				what := "GenAI traces"
				if groupBy != "trace" {
					what = "GenAI traces with a " + groupBy + " ID"
				}
				hintNoData(cmd, what, startT, endT)
				fmt.Fprintln(stderr, "Run 'oodle genai values <field>' to check the names in your filters: a wrong name and an idle agent look the same.")
			}
			if more {
				fmt.Fprintf(stderr, "Showing %d traces; more traces match. Raise --limit (max %d) or use a smaller time range to see more.\n",
					len(rows), genaiTracesMaxLimit)
			}
			if resp.RowLimit > 0 && resp.RowsRead >= resp.RowLimit {
				fmt.Fprintf(stderr, "The read stopped at %d span rows, so some traces can be incomplete and their token and cost totals too low. Use a smaller time range or more filters.\n", resp.RowLimit)
			}
			return nil
		},
	}
	filters.addTo(cmd)
	cmd.Flags().StringVar(&startStr, "start", "-6h", "Start of the time range (relative like -1h, 'now', RFC3339, or epoch s/ms/µs/ns)")
	cmd.Flags().StringVar(&endStr, "end", "now", "End of the time range (relative like -1h, 'now', RFC3339, or epoch s/ms/µs/ns)")
	cmd.Flags().BoolVar(&errorsOnly, "errors", false, "Only traces that have a span with error status")
	cmd.Flags().StringVar(&search, "search", "", "Text to find in service and span names")
	cmd.Flags().StringVar(&minDuration, "min-duration", "", "Minimum trace duration (e.g. 500ms, 2s)")
	cmd.Flags().StringVar(&maxDuration, "max-duration", "", "Maximum trace duration (e.g. 10s)")
	cmd.Flags().IntVar(&minTokens, "min-tokens", 0, "Minimum total tokens in the trace")
	cmd.Flags().IntVar(&maxTokens, "max-tokens", 0, "Maximum total tokens in the trace")
	cmd.Flags().StringVar(&groupBy, "group-by", "trace", "Row for each trace, session or user")
	cmd.Flags().IntVar(&limit, "limit", genaiTracesDefaultLimit, fmt.Sprintf("Maximum number of traces to read (1 to %d)", genaiTracesMaxLimit))
	return cmd
}
