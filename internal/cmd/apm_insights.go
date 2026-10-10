package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	// apmInsightsMaxWindow is the longest range that the server reads. The
	// CLI checks it, so that a longer range fails with a clear message and
	// does not return fewer rows without a sign.
	apmInsightsMaxWindow = 7 * 24 * time.Hour

	apmInsightsDefaultStart = "-24h"
	apmInsightsDefaultLimit = 20
)

// apmInsightPatterns are the patterns that --pattern accepts.
var apmInsightPatterns = []string{"n_plus_one_query", "sequential_calls"}

// apmInsightNextSteps give the usual fix for each pattern.
var apmInsightNextSteps = map[string]string{
	"n_plus_one_query": "Batch the calls into one query, for example WHERE id IN (...), MGET or a pipeline. " +
		"Or load the related rows with a join or an eager load.",
	"sequential_calls": "Run the independent calls in parallel, or use a batch API.",
}

type apmInsightsSearchRequest struct {
	EndTimeEpochMs  int64    `json:"endTimeEpochMs"`
	TrendLookbackMs int64    `json:"trendLookbackMs"`
	Services        []string `json:"services"`
	Envs            []string `json:"envs"`
	Patterns        []string `json:"patterns"`
}

type apmInsightKey struct {
	Pattern            string `json:"pattern"`
	Service            string `json:"service"`
	Env                string `json:"env"`
	Resource           string `json:"resource"`
	EntryResource      string `json:"entryResource"`
	DownstreamService  string `json:"downstreamService"`
	DownstreamResource string `json:"downstreamResource"`
}

type apmInsight struct {
	apmInsightKey
	ID                string  `json:"id"`
	Priority          string  `json:"priority"`
	Score             float64 `json:"score"`
	Detections        float64 `json:"detections"`
	EstimatedRequests float64 `json:"estimatedRequests"`
	AffectedShare     float64 `json:"affectedShare"`
	TimeShare         float64 `json:"timeShare"`
	AvgRunMs          float64 `json:"avgRunMs"`
	AvgEntryLatencyMs float64 `json:"avgEntryLatencyMs"`
}

type apmInsightsSearchResponse struct {
	Insights []json.RawMessage `json:"insights"`
}

type apmInsightRow struct {
	Priority   string
	Pattern    string
	Service    string
	Entry      string
	Downstream string
	Detections string
	Requests   string
	TimeShare  string
	ID         string
}

var apmInsightColumns = []output.Column{
	{Header: "PRIORITY", Field: "Priority"},
	{Header: "PATTERN", Field: "Pattern"},
	{Header: "SERVICE", Field: "Service"},
	{Header: "ENTRY RESOURCE", Field: "Entry"},
	{Header: "DOWNSTREAM", Field: "Downstream"},
	{Header: "DETECTIONS", Field: "Detections"},
	{Header: "EST. REQUESTS", Field: "Requests"},
	{Header: "TIME SHARE", Field: "TimeShare"},
	{Header: "ID", Field: "ID"},
}

func apmInsightRows(insights []apmInsight) []apmInsightRow {
	rows := make([]apmInsightRow, 0, len(insights))
	for _, r := range insights {
		down := r.DownstreamService
		if r.DownstreamResource != "" {
			if down != "" {
				down += ": "
			}
			down += r.DownstreamResource
		}
		rows = append(rows, apmInsightRow{
			Priority:   r.Priority,
			Pattern:    r.Pattern,
			Service:    r.Service,
			Entry:      r.EntryResource,
			Downstream: down,
			Detections: strconv.FormatFloat(r.Detections, 'f', 0, 64),
			Requests:   strconv.FormatFloat(r.EstimatedRequests, 'f', 0, 64),
			TimeShare:  formatPercent(r.TimeShare * 100),
			ID:         r.ID,
		})
	}
	return rows
}

// apmInsightTraceQL returns a TraceQL filter that finds sample traces of an
// insight. The detector writes the pattern and the downstream resource as
// attributes on the parent span of the repeated calls.
func apmInsightTraceQL(k apmInsightKey) string {
	parts := []string{
		"resource.service.name=" + strconv.Quote(k.Service),
		"span.oodle.apm.pattern=" + strconv.Quote(k.Pattern),
	}
	if k.DownstreamResource != "" {
		parts = append(parts, "span.oodle.apm.pattern.downstream_resource="+strconv.Quote(k.DownstreamResource))
	}
	return "{ " + strings.Join(parts, " && ") + " }"
}

type apmInsightDetailRequest struct {
	EndTimeEpochMs  int64         `json:"endTimeEpochMs"`
	TrendLookbackMs int64         `json:"trendLookbackMs"`
	Key             apmInsightKey `json:"key"`
}

type apmInsightDetailResponse struct {
	StepSec    int64 `json:"stepSec"`
	Detections []struct {
		Ts    int64    `json:"ts"`
		Value *float64 `json:"value"`
	} `json:"detections"`
}

type apmInsightTrendRow struct {
	Time       string
	Detections string
}

var apmInsightTrendColumns = []output.Column{
	{Header: "TIME (UTC)", Field: "Time"},
	{Header: "DETECTIONS", Field: "Detections"},
}

// epochToTime reads a timestamp in seconds or milliseconds.
func epochToTime(ts int64) time.Time {
	return time.UnixMilli(epochToUnit(ts, 1e3)).UTC()
}

func newTracesAPMInsightsCmd() *cobra.Command {
	var (
		services []string
		envs     []string
		pattern  string
		limit    int
		id       string
	)
	cmd := &cobra.Command{
		Use:     "apm-insights",
		Aliases: []string{"insights"},
		Short:   "List N+1 queries and sequential calls found in traces",
		Long: `List APM insights: N+1 queries and sequential calls found in traces.

  n_plus_one_query  A database or cache call repeated once per item, where
                    one batched query would do.
  sequential_calls  HTTP, RPC or messaging calls made one after the other
                    that could run in parallel.

Each row names the service, the entry resource (the endpoint), and the
repeated downstream call. DETECTIONS is the number of sampled traces with
the pattern. EST. REQUESTS scales the sampled share by the unsampled
request count of the entry resource. TIME SHARE is the share of the request
time spent in the repeated calls. Rows are sorted by priority score.

Detection runs on Datadog traces only. A service that sends only OTLP
traces has no rows. The longest range is 7 days.

Give --id (from the ID column) to show one insight with its detection trend,
the usual fix, and a TraceQL filter that finds sample traces.`,
		Example: `  # Insights of the last 24 hours
  oodle traces apm-insights

  # N+1 queries of one service in the last 7 days
  oodle traces apm-insights --service checkout --pattern n_plus_one_query --start -7d

  # One insight with its trend and sample trace filter
  oodle traces apm-insights --id 3f2a9c1e`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlags(cmd, apmInsightsDefaultStart, parseTimeFlagSec)
	cmd.Flags().StringSliceVar(&services, "service", nil, "Only show these services (comma-separated or repeated)")
	cmd.Flags().StringSliceVar(&envs, "env", nil, "Only show these environments (comma-separated or repeated)")
	cmd.Flags().StringVar(&pattern, "pattern", "", "Only show this pattern: "+strings.Join(apmInsightPatterns, ", "))
	cmd.Flags().IntVar(&limit, "limit", apmInsightsDefaultLimit, "Maximum number of rows to show")
	cmd.Flags().StringVar(&id, "id", "", "Show one insight by its ID, with its trend")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if pattern != "" && !slices.Contains(apmInsightPatterns, pattern) {
			return fmt.Errorf("--pattern must be one of %s", strings.Join(apmInsightPatterns, ", "))
		}
		if limit < 1 {
			return fmt.Errorf("--limit must be at least 1")
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		window := time.Duration(end-start) * time.Second
		if window > apmInsightsMaxWindow {
			return fmt.Errorf("the range is longer than 7 days; use a shorter range, for example --start -7d")
		}
		req := apmInsightsSearchRequest{
			EndTimeEpochMs:  end * 1000,
			TrendLookbackMs: max(window.Milliseconds(), time.Minute.Milliseconds()),
			Services:        nonEmpty(services),
			Envs:            nonEmpty(envs),
			Patterns:        nonEmpty([]string{pattern}),
		}
		body, err := instancePostJSON(cmd, "apm/insights/search", req)
		if err != nil {
			return err
		}
		var raw apmInsightsSearchResponse
		if err := json.Unmarshal(body, &raw); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
		insights := make([]apmInsight, len(raw.Insights))
		for i, r := range raw.Insights {
			if err := json.Unmarshal(r, &insights[i]); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
		}

		if id != "" {
			idx := slices.IndexFunc(insights, func(r apmInsight) bool { return r.ID == id })
			if idx < 0 {
				return fmt.Errorf("no insight with ID %s in this range and these filters; list the insights again with the same flags", id)
			}
			return printAPMInsightDetail(cmd, req, insights[idx], raw.Insights[idx])
		}

		format := getOutputFormat(cmd)
		switch format {
		case output.FormatJSON, output.FormatYAML, "":
			// The rows are the server objects without change, cut to
			// --limit.
			shown := raw.Insights
			if len(shown) > limit {
				shown = shown[:limit]
			}
			var counts struct {
				CountsByPattern json.RawMessage `json:"countsByPattern"`
			}
			_ = json.Unmarshal(body, &counts)
			out := map[string]any{
				"total":           len(raw.Insights),
				"returned":        len(shown),
				"countsByPattern": counts.CountsByPattern,
				"insights":        shown,
			}
			data, err := json.Marshal(out)
			if err != nil {
				return err
			}
			if err := printResponseBody(cmd, format, data); err != nil {
				return err
			}
		default:
			shown := insights
			if len(shown) > limit {
				shown = shown[:limit]
			}
			if err := output.Print(cmd.OutOrStdout(), format, apmInsightRows(shown), apmInsightColumns); err != nil {
				return err
			}
			if len(insights) > limit {
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d of %d insights. Use --limit to show more.\n", limit, len(insights))
			}
		}
		if len(insights) == 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"No APM insights between %s and %s. Detection runs on Datadog traces only, and the longest range is 7 days.\n",
				time.Unix(start, 0).UTC().Format(time.RFC3339), time.Unix(end, 0).UTC().Format(time.RFC3339))
		}
		return nil
	}
	return cmd
}

// printAPMInsightDetail gets the trend of one insight and prints it with
// the insight.
func printAPMInsightDetail(cmd *cobra.Command, search apmInsightsSearchRequest, rec apmInsight, recRaw json.RawMessage) error {
	body, err := instancePostJSON(cmd, "apm/insights/detail", apmInsightDetailRequest{
		EndTimeEpochMs:  search.EndTimeEpochMs,
		TrendLookbackMs: search.TrendLookbackMs,
		Key:             rec.apmInsightKey,
	})
	if err != nil {
		return err
	}
	var detail apmInsightDetailResponse
	if err := json.Unmarshal(body, &detail); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	traceQL := apmInsightTraceQL(rec.apmInsightKey)
	format := getOutputFormat(cmd)
	switch format {
	case output.FormatJSON, output.FormatYAML, "":
		out := map[string]any{
			"insight":             recRaw,
			"trend":               json.RawMessage(body),
			"nextStep":            apmInsightNextSteps[rec.Pattern],
			"sampleTracesTraceQL": traceQL,
		}
		data, err := json.Marshal(out)
		if err != nil {
			return err
		}
		return printResponseBody(cmd, format, data)
	}

	if err := output.Print(cmd.OutOrStdout(), format, apmInsightRows([]apmInsight{rec}), apmInsightColumns); err != nil {
		return err
	}
	rows := make([]apmInsightTrendRow, 0, len(detail.Detections))
	for _, p := range detail.Detections {
		v := ""
		if p.Value != nil {
			v = strconv.FormatFloat(*p.Value, 'f', -1, 64)
		}
		rows = append(rows, apmInsightTrendRow{
			Time:       epochToTime(p.Ts).Format("2006-01-02 15:04"),
			Detections: v,
		})
	}
	if format == output.FormatTable {
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "\nParent resource: %s\n", rec.Resource)
		if next := apmInsightNextSteps[rec.Pattern]; next != "" {
			fmt.Fprintf(w, "Next step: %s\n", next)
		}
		fmt.Fprintf(w, "Sample traces: oodle traces traceql search '%s'\n", traceQL)
		if len(rows) == 0 {
			return nil
		}
		fmt.Fprintf(w, "\nDetections per %s:\n", (time.Duration(detail.StepSec) * time.Second).String())
	}
	return output.Print(cmd.OutOrStdout(), format, rows, apmInsightTrendColumns)
}

// nonEmpty returns the values that are not empty, and an empty list (not
// nil) when there are none, so that the request has [] and not null.
func nonEmpty(values []string) []string {
	out := []string{}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
