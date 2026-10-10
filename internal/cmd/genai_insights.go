package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// --- agent-graph ---

// graphValue is one statistic of the agent graph. The server sends
// the value for the time range and for a comparison range; only the
// first is asked for.
type graphValue struct {
	Current float64 `json:"current"`
}

type graphStats struct {
	RequestCount     graphValue `json:"requestCount"`
	ErrorCount       graphValue `json:"errorCount"`
	AvgDurationNs    graphValue `json:"avgDurationNs"`
	MaxDurationNs    graphValue `json:"maxDurationNs"`
	TotalCostDollars graphValue `json:"totalCostDollars"`
	TotalTokens      graphValue `json:"totalTokens"`
	TraceCount       graphValue `json:"traceCount"`
}

type graphOwner struct {
	Operation string `json:"operation"`
	Type      string `json:"type"`
}

type genaiGraphResponse struct {
	Nodes []struct {
		graphOwner
		Stats *graphStats `json:"stats"`
	} `json:"nodes"`
	Edges []struct {
		Source graphOwner  `json:"source"`
		Target graphOwner  `json:"target"`
		Stats  *graphStats `json:"stats"`
	} `json:"edges"`
}

// graphRow is one node or edge of the agent graph.
type graphRow struct {
	Operation     string  `json:"operation,omitempty"`
	Type          string  `json:"type,omitempty"`
	From          string  `json:"from,omitempty"`
	FromType      string  `json:"from_type,omitempty"`
	To            string  `json:"to,omitempty"`
	ToType        string  `json:"to_type,omitempty"`
	Requests      int64   `json:"requests"`
	Errors        int64   `json:"errors"`
	ErrorRate     float64 `json:"error_rate"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
	MaxDurationMs float64 `json:"max_duration_ms"`
	Traces        int64   `json:"traces"`
	CallsPerTrace float64 `json:"calls_per_trace,omitempty"`
	Tokens        int64   `json:"tokens,omitempty"`
	CostDollars   float64 `json:"cost_dollars,omitempty"`
}

func graphStatsRow(s *graphStats) graphRow {
	if s == nil {
		return graphRow{}
	}
	r := graphRow{
		Requests:      int64(s.RequestCount.Current),
		Errors:        int64(s.ErrorCount.Current),
		AvgDurationMs: s.AvgDurationNs.Current / 1e6,
		MaxDurationMs: s.MaxDurationNs.Current / 1e6,
		Traces:        int64(s.TraceCount.Current),
		Tokens:        int64(s.TotalTokens.Current),
		CostDollars:   s.TotalCostDollars.Current,
	}
	if r.Requests > 0 {
		r.ErrorRate = float64(r.Errors) / float64(r.Requests)
	}
	// Many calls of one node in each trace is the sign of an agent
	// that retries in a loop.
	if r.Traces > 0 {
		r.CallsPerTrace = float64(r.Requests) / float64(r.Traces)
	}
	return r
}

// topRows keeps the n rows with the most requests.
func topRows(rows []graphRow, n int) []graphRow {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Requests > rows[j].Requests })
	if len(rows) > n {
		rows = rows[:n]
	}
	return rows
}

type graphTableRow struct {
	Name, Type, Requests, Errors, ErrorRate, Avg, Max, PerTrace, Tokens, Cost string
}

func graphTable(rows []graphRow, edges bool) []graphTableRow {
	out := make([]graphTableRow, 0, len(rows))
	for _, r := range rows {
		name, typ := r.Operation, r.Type
		if edges {
			name = r.From + " -> " + r.To
			typ = r.FromType + " -> " + r.ToType
		}
		perTrace := ""
		if r.CallsPerTrace > 0 {
			perTrace = strconv.FormatFloat(r.CallsPerTrace, 'f', 1, 64)
		}
		tokens := ""
		if r.Tokens > 0 {
			tokens = strconv.FormatInt(r.Tokens, 10)
		}
		out = append(out, graphTableRow{
			Name:      name,
			Type:      typ,
			Requests:  strconv.FormatInt(r.Requests, 10),
			Errors:    strconv.FormatInt(r.Errors, 10),
			ErrorRate: formatPercent(r.ErrorRate * 100),
			Avg:       formatDurationMs(r.AvgDurationMs),
			Max:       formatDurationMs(r.MaxDurationMs),
			PerTrace:  perTrace,
			Tokens:    tokens,
			Cost:      formatCost(r.CostDollars),
		})
	}
	return out
}

func graphColumns(edges bool) []output.Column {
	name := "OPERATION"
	if edges {
		name = "FROM -> TO"
	}
	return []output.Column{
		{Header: name, Field: "Name"},
		{Header: "TYPE", Field: "Type"},
		{Header: "REQUESTS", Field: "Requests"},
		{Header: "ERRORS", Field: "Errors"},
		{Header: "ERROR %", Field: "ErrorRate"},
		{Header: "AVG", Field: "Avg"},
		{Header: "MAX", Field: "Max"},
		{Header: "CALLS/TRACE", Field: "PerTrace"},
		{Header: "TOKENS", Field: "Tokens"},
		{Header: "COST", Field: "Cost"},
	}
}

func newGenAIAgentGraphCmd() *cobra.Command {
	var (
		// parseRange is set after cmd exists, because the flags need cmd.
		parseRange func() (start, end int64, err error)
		env        string
		service    string
		edges      bool
		limit      int
	)
	cmd := &cobra.Command{
		Use:     "agent-graph",
		Aliases: []string{"graph"},
		Short:   "Show the agents, tools and model calls, and the calls between them",
		Long: `Show the call graph of your AI agents: the nodes (agents, tools and
model calls) and the edges (which node calls which), each with
requests, errors, average and maximum duration, tokens and cost.

The graph is made from GenAI metrics, not from a sample of traces, so
it covers all the time range (default: the last 24 hours). A high
CALLS/TRACE value shows a node that runs many times in one trace, for
example an agent that retries in a loop.

The table shows the nodes; add --edges to show the edges. JSON and
YAML output have both.`,
		Example: `  oodle genai agent-graph
  oodle genai agent-graph --service support-agent --start -7d
  oodle genai agent-graph --edges --limit 20`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 {
				return fmt.Errorf("--limit must be 1 or more, got %d", limit)
			}
			start, end, err := parseRange()
			if err != nil {
				return err
			}
			body, err := instancePostJSON(cmd, "graph/generate-genai-graph", map[string]any{
				"startTimeEpochMs": start,
				"endTimeEpochMs":   end,
				"environment":      env,
				"serviceName":      service,
			})
			if err != nil {
				return err
			}
			var resp genaiGraphResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			nodes := make([]graphRow, 0, len(resp.Nodes))
			for _, n := range resp.Nodes {
				r := graphStatsRow(n.Stats)
				r.Operation, r.Type = n.Operation, n.Type
				nodes = append(nodes, r)
			}
			edgeRows := make([]graphRow, 0, len(resp.Edges))
			for _, e := range resp.Edges {
				r := graphStatsRow(e.Stats)
				r.From, r.FromType, r.To, r.ToType = e.Source.Operation, e.Source.Type, e.Target.Operation, e.Target.Type
				edgeRows = append(edgeRows, r)
			}
			totalNodes, totalEdges := len(nodes), len(edgeRows)
			nodes, edgeRows = topRows(nodes, limit), topRows(edgeRows, limit)

			if isTabular(cmd) {
				rows := nodes
				if edges {
					rows = edgeRows
				}
				err = output.Print(cmd.OutOrStdout(), getOutputFormat(cmd), graphTable(rows, edges), graphColumns(edges))
			} else {
				err = printShaped(cmd, struct {
					StartTime  string     `json:"start_time"`
					EndTime    string     `json:"end_time"`
					Nodes      []graphRow `json:"nodes"`
					TotalNodes int        `json:"total_nodes"`
					Edges      []graphRow `json:"edges"`
					TotalEdges int        `json:"total_edges"`
				}{
					time.UnixMilli(start).UTC().Format(time.RFC3339),
					time.UnixMilli(end).UTC().Format(time.RFC3339),
					nodes, totalNodes, edgeRows, totalEdges,
				})
			}
			if err != nil {
				return err
			}
			if totalNodes > len(nodes) || totalEdges > len(edgeRows) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing the top %d of %d nodes and %d edges by requests. Raise --limit to see more.\n",
					limit, totalNodes, totalEdges)
			}
			if totalNodes == 0 {
				hintNoData(cmd, "agent graph nodes", time.UnixMilli(start), time.UnixMilli(end))
				if env != "" || service != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "Run 'oodle genai values service env' to check the --service and --env values.")
				}
			}
			return nil
		},
	}
	parseRange = addRangeFlags(cmd, "-24h", parseTimeFlagMs)
	cmd.Flags().StringVar(&env, "env", "", "Only this environment")
	cmd.Flags().StringVar(&service, "service", "", "Only this service")
	cmd.Flags().BoolVar(&edges, "edges", false, "Show the edges instead of the nodes in the table")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum number of nodes and of edges, by requests")
	return cmd
}

// --- recommendations ---

type genaiRecommendation struct {
	ID            string `json:"id"`
	Severity      string `json:"severity"`
	Status        string `json:"status"`
	Category      string `json:"category"`
	CategoryGroup string `json:"category_group"`
	Title         string `json:"title"`
	ServiceName   string `json:"service_name"`
	ToolName      string `json:"tool_name"`
}

type genaiRecommendationsResponse struct {
	GeneratedAtEpochMs int64             `json:"generated_at_epoch_ms"`
	TracesAnalyzed     int64             `json:"traces_analyzed"`
	TimeRangeStart     string            `json:"time_range_start"`
	TimeRangeEnd       string            `json:"time_range_end"`
	Recommendations    []json.RawMessage `json:"recommendations"`
}

var genaiSeverities = []string{"critical", "high", "medium", "low"}
var genaiRecStatuses = []string{"new", "recurring", "stale"}

func newGenAIRecommendationsCmd() *cobra.Command {
	var (
		severity string
		category string
		status   string
		limit    int
	)
	cmd := &cobra.Command{
		Use:     "recommendations",
		Aliases: []string{"recs", "insights"},
		Short:   "List findings about the quality, cost and speed of your agents",
		Long: `List the latest recommendations for your GenAI workloads: findings
about quality, efficiency, cost and performance, such as tools that
return errors, repeated tool calls, slow calls and unhappy users.

Oodle makes the recommendations from a periodic analysis of your GenAI
traces. This command reads the latest analysis and does not start a
new one. JSON output has each recommendation in full, with its
description, impact and the traces that show the problem.

Filter with --severity (critical, high, medium, low), --status (new,
recurring, stale) and --category (text in the category or its group).`,
		Example: `  oodle genai recommendations
  oodle genai recommendations --severity high --status new
  oodle genai recommendations --category tool -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 {
				return fmt.Errorf("--limit must be 1 or more, got %d", limit)
			}
			severity, status = strings.ToLower(severity), strings.ToLower(status)
			if severity != "" && !slices.Contains(genaiSeverities, severity) {
				return fmt.Errorf("--severity must be one of %s, got %q", strings.Join(genaiSeverities, ", "), severity)
			}
			if status != "" && !slices.Contains(genaiRecStatuses, status) {
				return fmt.Errorf("--status must be one of %s, got %q", strings.Join(genaiRecStatuses, ", "), status)
			}
			body, err := instanceGet(cmd, "genai/recommendations", nil)
			if err != nil {
				return err
			}
			var resp genaiRecommendationsResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			cat := strings.ToLower(category)
			var rows []genaiRecommendation
			var full []json.RawMessage
			for _, raw := range resp.Recommendations {
				var r genaiRecommendation
				if err := json.Unmarshal(raw, &r); err != nil {
					return fmt.Errorf("parsing response: %w", err)
				}
				if severity != "" && strings.ToLower(r.Severity) != severity ||
					status != "" && strings.ToLower(r.Status) != status ||
					cat != "" && !strings.Contains(strings.ToLower(r.Category), cat) &&
						!strings.Contains(strings.ToLower(r.CategoryGroup), cat) {
					continue
				}
				rows = append(rows, r)
				full = append(full, raw)
			}
			matched := len(rows)
			if len(rows) > limit {
				rows, full = rows[:limit], full[:limit]
			}

			if isTabular(cmd) {
				err = output.Print(cmd.OutOrStdout(), getOutputFormat(cmd), rows, []output.Column{
					{Header: "SEVERITY", Field: "Severity"},
					{Header: "STATUS", Field: "Status"},
					{Header: "CATEGORY", Field: "Category"},
					{Header: "SERVICE", Field: "ServiceName"},
					{Header: "TITLE", Field: "Title"},
				})
			} else {
				if full == nil {
					full = []json.RawMessage{}
				}
				var generated string
				if resp.GeneratedAtEpochMs > 0 {
					generated = time.UnixMilli(resp.GeneratedAtEpochMs).UTC().Format(time.RFC3339)
				}
				err = printShaped(cmd, struct {
					GeneratedAt          string            `json:"generated_at,omitempty"`
					TracesAnalyzed       int64             `json:"traces_analyzed"`
					TimeRangeStart       string            `json:"time_range_start,omitempty"`
					TimeRangeEnd         string            `json:"time_range_end,omitempty"`
					TotalRecommendations int               `json:"total_recommendations"`
					Returned             int               `json:"returned"`
					Recommendations      []json.RawMessage `json:"recommendations"`
				}{generated, resp.TracesAnalyzed, resp.TimeRangeStart, resp.TimeRangeEnd,
					len(resp.Recommendations), len(full), full})
			}
			if err != nil {
				return err
			}
			if matched > len(rows) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d of %d recommendations. Raise --limit to see more.\n", len(rows), matched)
			}
			if len(resp.Recommendations) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No recommendations yet. The analysis runs periodically over your GenAI traces; an instance without GenAI traces gets none.")
			} else if matched == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "None of the %d recommendations match the filters.\n", len(resp.Recommendations))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&severity, "severity", "", "Only this severity: critical, high, medium or low")
	cmd.Flags().StringVar(&status, "status", "", "Only this status: new, recurring or stale")
	cmd.Flags().StringVar(&category, "category", "", "Only categories that contain this text")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum number of recommendations")
	return cmd
}
