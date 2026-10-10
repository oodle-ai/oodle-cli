package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	// traceQLDefaultLimit is the number of traces that search returns when
	// --limit is not set. The server uses the same default.
	traceQLDefaultLimit = 20

	// traceQLMaxLimit is the largest --limit that search accepts. The CLI
	// checks it so that a large value fails with a clear message.
	traceQLMaxLimit = 1000

	// traceQLMaxPoints is the number of points per series that the default
	// metrics step aims for. Without a bound, a long range with a one-minute
	// step makes the server compute very many buckets.
	traceQLMaxPoints = 300
)

// traceQLMetricsFuncs are the TraceQL metrics functions that the server
// supports. The help text and the search/metrics query check use this list.
var traceQLMetricsFuncs = []string{
	"rate",
	"count_over_time",
	"avg_over_time",
	"min_over_time",
	"max_over_time",
	"sum_over_time",
	"histogram_over_time",
	"quantile_over_time",
}

// traceQLMetricsRe finds a metrics function in a pipeline stage, such as
// "| rate(" or "| quantile_over_time(".
var traceQLMetricsRe = regexp.MustCompile(
	`\|\s*(` + strings.Join(traceQLMetricsFuncs, "|") + `)\s*\(`,
)

// traceQLStringRe finds quoted string literals. They are removed before the
// metrics check, so that a value such as "| rate(" in a filter does not make a
// search query look like a metrics query.
var traceQLStringRe = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`")

// isTraceQLMetricsQuery reports whether q has a metrics function stage.
func isTraceQLMetricsQuery(q string) bool {
	return traceQLMetricsRe.MatchString(traceQLStringRe.ReplaceAllString(q, `""`))
}

const traceQLLong = `Run TraceQL queries against your traces.

  search      Find traces that have spans that match a filter.
  metrics     Compute time series from matching spans.
  tags        List the attribute names you can use in a query.
  tag-values  List the values of one attribute.

A filter is a spanset in braces. Attributes have a scope: resource. for
resource attributes, span. for span attributes. Intrinsics such as name,
status, kind and duration have no scope.

Examples:
  # Traces with an error span in the api service
  oodle traces traceql search '{ resource.service.name="api" && status=error }'

  # Error rate per route
  oodle traces traceql metrics \
    '{ resource.service.name="api" && status=error } | rate() by (span.http.route)'

  # Slow tool calls, counted per tool
  oodle traces traceql metrics \
    '{ name=~"execute_tool.*" && duration > 10s } | count_over_time() by (span.gen_ai.tool.name)'

  # p95 span duration
  oodle traces traceql metrics \
    '{ resource.service.name="api" } | quantile_over_time(duration, .95)'

Supported metrics functions:
  rate(), count_over_time(), avg_over_time(field), min_over_time(field),
  max_over_time(field), sum_over_time(field), histogram_over_time(field),
  quantile_over_time(field, q...). Add "by (field, ...)" to split the series.

Not supported yet:
  - Scalar filters, such as { ... } | count() > 2
  - "&&" between two spansets, such as {A} && {B}. Put both conditions in one
    filter: { A && B }. "||" between spansets is supported.
  - Structural operators between two spansets: >>, <<, >, <, ~, as in
    {A} > {B}. Comparisons inside one filter, such as { duration > 10s },
    work.
  - The parent. and link. scopes, and the rootName intrinsic
  - Existence checks such as { span.foo }. Use { span.foo != nil }.

Alerts:
  Monitors use PromQL. They cannot run TraceQL. To alert on traces, write a
  PromQL monitor on the oodle_trace_metrics metrics, or on the oodle_genai_*
  metrics for GenAI spans. See 'oodle monitors create --help'.

tags and tag-values read only the last hour. search and metrics take
--start and --end. Time flags accept 'now', a relative time such as -1h or -7d, or an epoch
timestamp in seconds. Epoch values in milliseconds, microseconds or
nanoseconds are also accepted and converted to seconds.`

// newTracesTraceQLCmd returns the `oodle traces traceql` command tree.
func newTracesTraceQLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "traceql",
		Aliases: []string{"tql"},
		Short:   "Search traces and compute metrics with TraceQL",
		Long:    traceQLLong,
	}
	cmd.AddCommand(newTraceQLSearchCmd())
	cmd.AddCommand(newTraceQLMetricsCmd())
	cmd.AddCommand(newTraceQLTagsCmd())
	cmd.AddCommand(newTraceQLTagValuesCmd())
	return cmd
}

// addTraceQLTimeFlags adds --start and --end to cmd. The returned function
// gives the range in whole epoch seconds, which is the unit the TraceQL API
// reads.
func addTraceQLTimeFlags(cmd *cobra.Command) func() (start, end int64, err error) {
	var startStr, endStr string
	cmd.Flags().StringVar(&startStr, "start", defaultStartOffset,
		"Start of the time range (relative like -1h, 'now', RFC3339, or epoch s/ms/µs/ns)")
	cmd.Flags().StringVar(&endStr, "end", defaultEndValue,
		"End of the time range (relative like -1h, 'now', RFC3339, or epoch s/ms/µs/ns)")
	return func() (int64, int64, error) {
		start, err := parseTraceQLTime(startStr)
		if err != nil {
			return 0, 0, fmt.Errorf("--start: %w", err)
		}
		end, err := parseTraceQLTime(endStr)
		if err != nil {
			return 0, 0, fmt.Errorf("--end: %w", err)
		}
		if start >= end {
			return 0, 0, fmt.Errorf("--start must be before --end")
		}
		return start, end, nil
	}
}

// parseTraceQLTime parses a time flag to epoch seconds. parseTimeFlagSec
// converts an epoch in ms, µs or ns to seconds by its magnitude.
func parseTraceQLTime(value string) (int64, error) {
	return parseTimeFlagSec(value)
}

// parseTraceQLStep parses a step such as 30s, 5m, 1h, 1d or a number of
// seconds, and returns whole seconds.
func parseTraceQLStep(value string) (int64, error) {
	v := strings.TrimSpace(value)
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("step must be greater than zero")
		}
		return n, nil
	}
	d, err := parseRelativeDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid step %q: use a duration such as 30s, 5m or 1h", value)
	}
	secs := int64(d / time.Second)
	if secs <= 0 {
		return 0, fmt.Errorf("step must be at least 1s")
	}
	return secs, nil
}

// defaultTraceQLStep returns one minute, or a larger whole number of minutes
// so that the range has at most traceQLMaxPoints points.
func defaultTraceQLStep(rangeSec int64) int64 {
	step := int64(60)
	if rangeSec/step <= traceQLMaxPoints {
		return step
	}
	perPoint := (rangeSec + traceQLMaxPoints - 1) / traceQLMaxPoints
	return (perPoint + 59) / 60 * 60
}

// traceQLGet sends a GET to a TraceQL route of the current instance and
// returns the body of a 2xx response.
func traceQLGet(cmd *cobra.Command, route string, params url.Values) ([]byte, error) {
	c := getClient(cmd)
	if c == nil || c.Config == nil {
		return nil, fmt.Errorf("no API client configured")
	}
	instance := getInstance(cmd)
	u := strings.TrimRight(c.Config.APIURL, "/") +
		"/v1/api/instance/" + url.PathEscape(instance) +
		"/traces/traceql/" + route
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.NewAuthedHTTPClient(0).Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if err := api.CheckResponse(resp, body); err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return body, nil
}

// printRawJSON writes the response body without change. JSON output passes
// the server response through, so scripts get every field.
func printRawJSON(cmd *cobra.Command, body []byte) error {
	w := cmd.OutOrStdout()
	if _, err := w.Write(body); err != nil {
		return err
	}
	if body[len(body)-1] != '\n' {
		_, err := io.WriteString(w, "\n")
		return err
	}
	return nil
}

// printTraceQLBody handles the output formats that do not need a table:
// JSON passes the body through and YAML converts it. It returns false for
// the other formats.
func printTraceQLBody(cmd *cobra.Command, format output.Format, body []byte) (bool, error) {
	switch format {
	case output.FormatJSON, "":
		return true, printRawJSON(cmd, body)
	case output.FormatYAML:
		parsed, err := decodeJSONForYAML(body)
		if err != nil {
			return true, fmt.Errorf("parsing response: %w", err)
		}
		return true, output.Print(cmd.OutOrStdout(), format, parsed, nil)
	}
	return false, nil
}

// printTraceQLResult prints body as JSON or YAML, or calls table for the
// other formats. decodeErr is the error from decoding body for the table;
// JSON and YAML output do not need the decode.
func printTraceQLResult(cmd *cobra.Command, body []byte, decodeErr error, table func(output.Format) error) error {
	format := getOutputFormat(cmd)
	if done, err := printTraceQLBody(cmd, format, body); done {
		return err
	}
	if decodeErr != nil {
		return fmt.Errorf("parsing response: %w", decodeErr)
	}
	return table(format)
}

// --- search ---

type traceQLSearchResponse struct {
	Traces []traceQLTrace `json:"traces"`
}

type traceQLSpanSet struct {
	Matched int `json:"matched"`
}

type traceQLTrace struct {
	TraceID           string           `json:"traceID"`
	RootServiceName   string           `json:"rootServiceName"`
	RootTraceName     string           `json:"rootTraceName"`
	StartTimeUnixNano string           `json:"startTimeUnixNano"`
	DurationMs        int64            `json:"durationMs"`
	SpanSet           *traceQLSpanSet  `json:"spanSet"`
	SpanSets          []traceQLSpanSet `json:"spanSets"`
}

// matchedSpans returns the number of matching spans in the trace. spanSet is
// an older field that repeats the first entry of spanSets, so adding both
// counts that entry twice. spanSet is used only when spanSets is empty.
func (t traceQLTrace) matchedSpans() int {
	if len(t.SpanSets) == 0 {
		if t.SpanSet != nil {
			return t.SpanSet.Matched
		}
		return 0
	}
	matched := 0
	for _, s := range t.SpanSets {
		matched += s.Matched
	}
	return matched
}

type traceQLSearchRow struct {
	TraceID     string
	RootService string
	RootName    string
	Start       string
	Duration    string
	Matched     string
}

var traceQLSearchColumns = []output.Column{
	{Header: "TRACE ID", Field: "TraceID"},
	{Header: "ROOT SERVICE", Field: "RootService"},
	{Header: "ROOT NAME", Field: "RootName"},
	{Header: "START (UTC)", Field: "Start"},
	{Header: "DURATION", Field: "Duration"},
	{Header: "MATCHED SPANS", Field: "Matched"},
}

func traceQLSearchRows(resp traceQLSearchResponse) []traceQLSearchRow {
	rows := make([]traceQLSearchRow, 0, len(resp.Traces))
	for _, t := range resp.Traces {
		start := t.StartTimeUnixNano
		if ns, err := strconv.ParseInt(t.StartTimeUnixNano, 10, 64); err == nil && ns > 0 {
			start = time.Unix(0, ns).UTC().Format("2006-01-02 15:04:05")
		}
		matched := t.matchedSpans()
		rows = append(rows, traceQLSearchRow{
			TraceID:     t.TraceID,
			RootService: t.RootServiceName,
			RootName:    t.RootTraceName,
			Start:       start,
			Duration:    (time.Duration(t.DurationMs) * time.Millisecond).String(),
			Matched:     strconv.Itoa(matched),
		})
	}
	return rows
}

func newTraceQLSearchCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find traces that match a TraceQL filter",
		Long: `Find traces that have spans that match a TraceQL filter.

The query must be a filter, without a metrics function. To compute rates,
counts or quantiles, use 'oodle traces traceql metrics'.

The table shows one row per trace. Use -o json to get the full response,
which includes the matching spans and their attributes.`,
		Example: `  oodle traces traceql search '{ resource.service.name="api" && status=error }'
  oodle traces traceql search '{ span.http.status_code >= 500 }' --start -6h --limit 50
  oodle traces traceql search '{ duration > 2s }' -o json`,
		Args: exactArgs(1),
	}
	parseRange := addTraceQLTimeFlags(cmd)
	cmd.Flags().IntVar(&limit, "limit", traceQLDefaultLimit, "Maximum number of traces to return (1 to 1000)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		query := strings.TrimSpace(args[0])
		if isTraceQLMetricsQuery(query) {
			return fmt.Errorf("this is a metrics query; run it with 'oodle traces traceql metrics'")
		}
		if limit < 1 || limit > traceQLMaxLimit {
			return fmt.Errorf("--limit must be between 1 and %d, got %d", traceQLMaxLimit, limit)
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		params := url.Values{}
		params.Set("q", query)
		params.Set("start", strconv.FormatInt(start, 10))
		params.Set("end", strconv.FormatInt(end, 10))
		params.Set("limit", strconv.Itoa(limit))
		body, err := traceQLGet(cmd, "search", params)
		if err != nil {
			return err
		}
		var resp traceQLSearchResponse
		decodeErr := json.Unmarshal(body, &resp)
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			return output.Print(cmd.OutOrStdout(), format, traceQLSearchRows(resp), traceQLSearchColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(resp.Traces) == 0 {
			hintNoData(cmd, "matching traces", time.Unix(start, 0), time.Unix(end, 0))
		}
		return nil
	}
	return cmd
}

// --- metrics ---

type traceQLAnyValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *string  `json:"intValue"`
	DoubleValue *float64 `json:"doubleValue"`
	BoolValue   *bool    `json:"boolValue"`
}

func (v traceQLAnyValue) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'g', -1, 64)
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	}
	return ""
}

type traceQLMetricsResponse struct {
	Series []struct {
		Labels []struct {
			Key   string          `json:"key"`
			Value traceQLAnyValue `json:"value"`
		} `json:"labels"`
		Samples []struct {
			TimestampMs int64   `json:"timestampMs"`
			Value       float64 `json:"value"`
		} `json:"samples"`
	} `json:"series"`
}

// toPromSeries converts the TraceQL series to the series type that the graph
// and stats output formats read.
func (r traceQLMetricsResponse) toPromSeries() []output.PromSeries {
	out := make([]output.PromSeries, 0, len(r.Series))
	for _, s := range r.Series {
		labels := make(map[string]string, len(s.Labels))
		for _, l := range s.Labels {
			labels[l.Key] = l.Value.String()
		}
		values := make([]output.PromSample, 0, len(s.Samples))
		for _, p := range s.Samples {
			values = append(values, output.PromSample{
				Timestamp: float64(p.TimestampMs) / 1000,
				Value:     p.Value,
			})
		}
		sort.Slice(values, func(i, j int) bool { return values[i].Timestamp < values[j].Timestamp })
		out = append(out, output.PromSeries{Labels: labels, Values: values})
	}
	return out
}

type traceQLMetricsRow struct {
	Series string
	Points string
	Last   string
	Min    string
	Max    string
	Avg    string
}

var traceQLMetricsColumns = []output.Column{
	{Header: "SERIES", Field: "Series"},
	{Header: "POINTS", Field: "Points"},
	{Header: "LAST", Field: "Last"},
	{Header: "MIN", Field: "Min"},
	{Header: "MAX", Field: "Max"},
	{Header: "AVG", Field: "Avg"},
}

func formatTraceQLLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%q", k, labels[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatTraceQLValue(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}

func traceQLMetricsRows(series []output.PromSeries) []traceQLMetricsRow {
	rows := make([]traceQLMetricsRow, 0, len(series))
	for _, s := range series {
		row := traceQLMetricsRow{
			Series: formatTraceQLLabels(s.Labels),
			Points: strconv.Itoa(len(s.Values)),
		}
		if len(s.Values) > 0 {
			lo, hi, sum := math.Inf(1), math.Inf(-1), 0.0
			for _, p := range s.Values {
				lo = math.Min(lo, p.Value)
				hi = math.Max(hi, p.Value)
				sum += p.Value
			}
			row.Last = formatTraceQLValue(s.Values[len(s.Values)-1].Value)
			row.Min = formatTraceQLValue(lo)
			row.Max = formatTraceQLValue(hi)
			row.Avg = formatTraceQLValue(sum / float64(len(s.Values)))
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Series < rows[j].Series })
	return rows
}

func newTraceQLMetricsCmd() *cobra.Command {
	var (
		stepStr   string
		exemplars int
	)
	cmd := &cobra.Command{
		Use:   "metrics <query>",
		Short: "Compute time series from spans with a TraceQL metrics query",
		Long: `Compute time series from spans with a TraceQL metrics query.

The query is a filter followed by one metrics function, for example
'{ status=error } | rate() by (resource.service.name)'.

Supported functions: rate, count_over_time, avg_over_time, min_over_time,
max_over_time, sum_over_time, histogram_over_time, quantile_over_time.
Use "by (field, ...)" to split the result into one series for each value.

The table shows one row per series with the number of points and the last,
minimum, maximum and average values. Use -o graph for a chart, -o stats for
a compact summary, or -o json for the full response.

When --step is not set, the step is 1m, or a larger whole number of minutes
that keeps the result at or below 300 points per series.`,
		Example: `  oodle traces traceql metrics '{ resource.service.name="api" && status=error } | rate() by (span.http.route)'
  oodle traces traceql metrics '{ name=~"execute_tool.*" && duration > 10s } | count_over_time() by (span.gen_ai.tool.name)' --start -24h
  oodle traces traceql metrics '{ resource.service.name="api" } | quantile_over_time(duration, .95)' --step 5m -o graph`,
		Args: exactArgs(1),
	}
	parseRange := addTraceQLTimeFlags(cmd)
	cmd.Flags().StringVar(&stepStr, "step", "", "Resolution step, such as 30s, 5m or 1h (default: 1m, larger for long ranges)")
	cmd.Flags().IntVar(&exemplars, "exemplars", 0, "Maximum number of exemplar traces to return (shown only in JSON and YAML output)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		query := strings.TrimSpace(args[0])
		if !isTraceQLMetricsQuery(query) {
			return fmt.Errorf("this query has no metrics function such as '| rate()'; " +
				"add one, or run it with 'oodle traces traceql search'")
		}
		if exemplars < 0 {
			return fmt.Errorf("--exemplars must not be negative")
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		var step int64
		if stepStr != "" {
			if step, err = parseTraceQLStep(stepStr); err != nil {
				return fmt.Errorf("--step: %w", err)
			}
		} else {
			step = defaultTraceQLStep(end - start)
		}
		params := url.Values{}
		params.Set("q", query)
		params.Set("start", strconv.FormatInt(start, 10))
		params.Set("end", strconv.FormatInt(end, 10))
		params.Set("step", strconv.FormatInt(step, 10)+"s")
		params.Set("exemplars", strconv.Itoa(exemplars))
		body, err := traceQLGet(cmd, "metrics/query_range", params)
		if err != nil {
			return err
		}
		var resp traceQLMetricsResponse
		decodeErr := json.Unmarshal(body, &resp)
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			series := resp.toPromSeries()
			switch format {
			case output.FormatGraph:
				return output.PrintGraph(cmd.OutOrStdout(), series)
			case output.FormatStats:
				return output.PrintStats(cmd.OutOrStdout(), series)
			}
			return output.Print(cmd.OutOrStdout(), format, traceQLMetricsRows(series), traceQLMetricsColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(resp.Series) == 0 {
			hintNoData(cmd, "series", time.Unix(start, 0), time.Unix(end, 0))
		}
		return nil
	}
	return cmd
}

// --- tags ---

// traceQLTagsWindowNote tells the user that tags and tag-values have no time
// range. Without it, an empty list reads as proof that a name or value does
// not exist, when it only did not occur in the last hour.
const traceQLTagsWindowNote = `The server reads only the spans of the last hour. To look further back, use
'oodle traces traceql search' with a --start.`

// hintTraceQLLastHour writes the empty-result hint for tags and tag-values.
func hintTraceQLLastHour(cmd *cobra.Command, what string) {
	fmt.Fprintf(cmd.ErrOrStderr(),
		"No %s in the last hour. The server reads only the last hour; use -q to narrow the spans, "+
			"or 'oodle traces traceql search --start -24h' to look further back.\n", what)
}

var traceQLScopes = []string{"resource", "span", "intrinsic"}

type traceQLTagsResponse struct {
	Scopes []struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	} `json:"scopes"`
}

type traceQLTagRow struct {
	Scope string
	Tag   string
}

var traceQLTagColumns = []output.Column{
	{Header: "SCOPE", Field: "Scope"},
	{Header: "TAG", Field: "Tag"},
}

// traceQLKeepScope removes the other scopes from a tags response, so that
// JSON and YAML output follow --scope as the table does. The server returns
// every scope. A body that is not the expected shape is returned as it is.
func traceQLKeepScope(body []byte, scope string) []byte {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return body
	}
	scopes, ok := resp["scopes"].([]any)
	if !ok {
		return body
	}
	kept := []any{}
	for _, s := range scopes {
		if m, ok := s.(map[string]any); ok && m["name"] == scope {
			kept = append(kept, s)
		}
	}
	resp["scopes"] = kept
	out, err := json.Marshal(resp)
	if err != nil {
		return body
	}
	return out
}

// traceQLTagRows lists the tags of the given scope, or of all scopes when
// scope is "all". The CLI filters by scope itself because the server returns
// every scope. Names are shown as a query uses them: span.http.route,
// resource.service.name, or a bare intrinsic such as duration.
func traceQLTagRows(resp traceQLTagsResponse, scope string) []traceQLTagRow {
	var rows []traceQLTagRow
	for _, s := range resp.Scopes {
		if scope != "all" && s.Name != scope {
			continue
		}
		tags := append([]string(nil), s.Tags...)
		sort.Strings(tags)
		for _, t := range tags {
			name := t
			if s.Name == "resource" || s.Name == "span" {
				name = s.Name + "." + t
			}
			rows = append(rows, traceQLTagRow{Scope: s.Name, Tag: name})
		}
	}
	return rows
}

func newTraceQLTagsCmd() *cobra.Command {
	var (
		scope string
		query string
	)
	cmd := &cobra.Command{
		Use:   "tags",
		Short: "List attribute names to use in TraceQL queries",
		Long: `List attribute names to use in TraceQL queries.

The table shows each name the way a query uses it, for example
resource.service.name, span.http.route, or an intrinsic such as duration.
Use --query to list only the names on spans that match a filter.

` + traceQLTagsWindowNote,
		Example: `  oodle traces traceql tags
  oodle traces traceql tags --scope span
  oodle traces traceql tags --query '{ resource.service.name="api" }'`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&scope, "scope", "all", "Scope to list: resource, span, intrinsic or all (the CLI filters the result)")
	cmd.Flags().StringVarP(&query, "query", "q", "", "TraceQL filter that limits the spans to look at")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope != "all" && !slices.Contains(traceQLScopes, scope) {
			return fmt.Errorf("--scope must be one of resource, span, intrinsic, all")
		}
		params := url.Values{}
		if scope != "all" {
			params.Set("scope", scope)
		}
		if query != "" {
			params.Set("q", query)
		}
		body, err := traceQLGet(cmd, "tags", params)
		if err != nil {
			return err
		}
		if scope != "all" {
			body = traceQLKeepScope(body, scope)
		}
		var resp traceQLTagsResponse
		decodeErr := json.Unmarshal(body, &resp)
		rows := traceQLTagRows(resp, scope)
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			return output.Print(cmd.OutOrStdout(), format, rows, traceQLTagColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(rows) == 0 {
			hintTraceQLLastHour(cmd, "tags")
		}
		return nil
	}
	return cmd
}

// --- tag-values ---

type traceQLTagValuesResponse struct {
	TagValues []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"tagValues"`
}

type traceQLTagValueRow struct {
	Value string
	Type  string
}

var traceQLTagValueColumns = []output.Column{
	{Header: "VALUE", Field: "Value"},
	{Header: "TYPE", Field: "Type"},
}

func newTraceQLTagValuesCmd() *cobra.Command {
	var query string
	cmd := &cobra.Command{
		Use:   "tag-values <tag>",
		Short: "List the values of a TraceQL attribute",
		Long: `List the values of a TraceQL attribute.

Give the name the way a query uses it, for example resource.service.name,
span.http.route, or an intrinsic such as name or status. Run
'oodle traces traceql tags' to list the names. Use --query to list only the
values on spans that match a filter.

` + traceQLTagsWindowNote,
		Example: `  oodle traces traceql tag-values resource.service.name
  oodle traces traceql tag-values span.http.route --query '{ resource.service.name="api" }'
  oodle traces traceql tag-values status -q '{ resource.service.name="api" }'`,
		Args: exactArgs(1),
	}
	cmd.Flags().StringVarP(&query, "query", "q", "", "TraceQL filter that limits the spans to look at")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		tag := strings.TrimSpace(args[0])
		if tag == "" {
			return fmt.Errorf("tag name must not be empty")
		}
		params := url.Values{}
		if query != "" {
			params.Set("q", query)
		}
		body, err := traceQLGet(cmd, "tags/"+url.PathEscape(tag)+"/values", params)
		if err != nil {
			return err
		}
		var resp traceQLTagValuesResponse
		decodeErr := json.Unmarshal(body, &resp)
		rows := make([]traceQLTagValueRow, 0, len(resp.TagValues))
		for _, v := range resp.TagValues {
			rows = append(rows, traceQLTagValueRow{Value: v.Value, Type: v.Type})
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Value < rows[j].Value })
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			return output.Print(cmd.OutOrStdout(), format, rows, traceQLTagValueColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(rows) == 0 {
			hintTraceQLLastHour(cmd, "values for "+tag)
		}
		return nil
	}
	return cmd
}
