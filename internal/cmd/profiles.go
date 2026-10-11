package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	// profilesQuerierPath is the path prefix of the Pyroscope-compatible
	// Connect API. The route is at the root of the API host, not under
	// /v1/api/instance/<instance>/, so the instance goes in a header.
	profilesQuerierPath = "/querier.v1.QuerierService/"

	// profilesInstanceHeader is the only tenant header that the profiles
	// route reads. The server ignores other tenant headers on this route.
	profilesInstanceHeader = "OODLE-INSTANCE"

	// profilesDefaultStep is the series step in seconds when --step is not
	// set and the range is short. The server uses the same default.
	profilesDefaultStep = 15

	// profilesDefaultMaxNodes is the number of flame graph nodes that the
	// server keeps when --max-nodes is not set.
	profilesDefaultMaxNodes = 2048

	// profilesDefaultTop is the number of functions that 'profiles
	// flamegraph' shows when --top is not set.
	profilesDefaultTop = 30

	// profilesFunctionWidth is the largest number of characters of a
	// function name in a table cell.
	profilesFunctionWidth = 100
)

const profilesLong = `Read continuous profiling data: profile types, labels, time series and
flame graphs. The commands use the Pyroscope-compatible query API.

  types         List the profile types, such as CPU, memory and goroutines.
  labels        List the label names on profiles.
  label-values  List the values of one label.
  series        Show a profile type as time series.
  flamegraph    Show the functions that use the most CPU, memory or other
                resource.

A profile type ID has the form name:sample_type:sample_unit:period_type:
period_unit. Get the IDs from the ID column of 'oodle profiles types'.

Each command reads only the time range in --start and --end (default: the
last hour). Use -o json to get every field.

Examples:
  oodle profiles types
  oodle profiles label-values service_name
  oodle profiles flamegraph --type process_cpu:cpu:nanoseconds:cpu:nanoseconds`

// newProfilesCmd returns the `oodle profiles` command tree.
func newProfilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profiles",
		Aliases: []string{"profiling"},
		Short:   "Read continuous profiling types, labels, series and flame graphs",
		Long:    profilesLong,
	}
	cmd.AddCommand(newProfilesTypesCmd())
	cmd.AddCommand(newProfilesLabelsCmd())
	cmd.AddCommand(newProfilesLabelValuesCmd())
	cmd.AddCommand(newProfilesSeriesCmd())
	cmd.AddCommand(newProfilesFlamegraphCmd())
	return cmd
}

// profilesPost sends a JSON body with POST to one method of the profiles
// query API and returns the body of a 2xx response. It sends the instance
// in a header because the route has no instance in its path.
func profilesPost(cmd *cobra.Command, method string, payload any) ([]byte, error) {
	return apiCall{
		method:  http.MethodPost,
		path:    profilesQuerierPath + method,
		payload: payload,
		header:  map[string]string{profilesInstanceHeader: getInstance(cmd)},
	}.do(cmd)
}

// profileTypeMatchers returns the matcher list that scopes a label read to
// one profile type. An empty type ID gives an empty list, which reads the
// labels of all profile types.
func profileTypeMatchers(typeID string) []string {
	if typeID == "" {
		return []string{}
	}
	return []string{`{__profile_type__="` + typeID + `"}`}
}

// --- types ---

type profileTypeRaw struct {
	ID         string `json:"ID"`
	Name       string `json:"name"`
	SampleType string `json:"sample_type"`
	SampleUnit string `json:"sample_unit"`
	PeriodType string `json:"period_type"`
	PeriodUnit string `json:"period_unit"`
}

// profileType is one row of 'profiles types'. The JSON keys are the keys
// of the MCP tool result, so scripts can use either source.
type profileType struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SampleType string `json:"sample_type"`
	SampleUnit string `json:"sample_unit"`
	PeriodType string `json:"period_type"`
	PeriodUnit string `json:"period_unit"`
}

var profileTypeColumns = []output.Column{
	{Header: "ID", Field: "ID"},
	{Header: "NAME", Field: "Name"},
	{Header: "SAMPLE TYPE", Field: "SampleType"},
	{Header: "SAMPLE UNIT", Field: "SampleUnit"},
	{Header: "PERIOD TYPE", Field: "PeriodType"},
	{Header: "PERIOD UNIT", Field: "PeriodUnit"},
}

func newProfilesTypesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "types",
		Short: "List the profile types",
		Long: `List the profile types that have data in the time range, such as CPU,
memory and goroutines. Use a value from the ID column as --type in the
other profiles commands.`,
		Example: `  oodle profiles types
  oodle profiles types --start -24h -o json`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		body, err := profilesPost(cmd, "ProfileTypes", map[string]any{"start": start, "end": end})
		if err != nil {
			return err
		}
		var resp struct {
			ProfileTypes []profileTypeRaw `json:"profile_types"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
		types := make([]profileType, 0, len(resp.ProfileTypes))
		for _, pt := range resp.ProfileTypes {
			types = append(types, profileType(pt))
		}
		if err := printResult(cmd, types, func(format output.Format) error {
			return output.Print(cmd.OutOrStdout(), format, types, profileTypeColumns)
		}); err != nil {
			return err
		}
		if len(types) == 0 {
			hintNoData(cmd, "profile types", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- labels and label-values ---

// profilesNames decodes the "names" list that the LabelNames and
// LabelValues methods both return.
func profilesNames(body []byte) ([]string, error) {
	var resp struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if resp.Names == nil {
		return []string{}, nil
	}
	return resp.Names, nil
}

func newProfilesLabelsCmd() *cobra.Command {
	var typeID string
	cmd := &cobra.Command{
		Use:   "labels",
		Short: "List the label names on profiles",
		Long: `List the label names on profiles, such as service_name, namespace and pod.
Use the names in --query and --group-by of the series and flamegraph
commands. Set --type to read only the labels of one profile type.`,
		Example: `  oodle profiles labels
  oodle profiles labels --type process_cpu:cpu:nanoseconds:cpu:nanoseconds`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	cmd.Flags().StringVar(&typeID, "type", "", "Only this profile type ID (from 'oodle profiles types')")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		body, err := profilesPost(cmd, "LabelNames", map[string]any{
			"matchers": profileTypeMatchers(typeID),
			"start":    start,
			"end":      end,
		})
		if err != nil {
			return err
		}
		names, err := profilesNames(body)
		if err != nil {
			return err
		}
		if err := printStringSlice(cmd, getOutputFormat(cmd), names, "Label"); err != nil {
			return err
		}
		if len(names) == 0 {
			hintNoData(cmd, "profile labels", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

func newProfilesLabelValuesCmd() *cobra.Command {
	var typeID string
	cmd := &cobra.Command{
		Use:   "label-values <label_name>",
		Short: "List the values of a profile label",
		Long: `List the values of one profile label, such as the services that send
profiles. Set --type to read only the values of one profile type.`,
		Example: `  oodle profiles label-values service_name
  oodle profiles label-values pod --type memory:inuse_space:bytes:space:bytes --start -6h`,
		Args: exactArgs(1),
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	cmd.Flags().StringVar(&typeID, "type", "", "Only this profile type ID (from 'oodle profiles types')")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		body, err := profilesPost(cmd, "LabelValues", map[string]any{
			"name":     args[0],
			"matchers": profileTypeMatchers(typeID),
			"start":    start,
			"end":      end,
		})
		if err != nil {
			return err
		}
		values, err := profilesNames(body)
		if err != nil {
			return err
		}
		if err := printStringSlice(cmd, getOutputFormat(cmd), values, "Value"); err != nil {
			return err
		}
		if len(values) == 0 {
			hintNoData(cmd, "values for this label", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- series ---

type profilePoint struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// profileSeries is one series of 'profiles series'. The JSON keys are the
// keys of the MCP tool result.
type profileSeries struct {
	Labels map[string]string `json:"labels"`
	Count  int               `json:"count"`
	Sum    float64           `json:"sum"`
	Points []profilePoint    `json:"points"`
}

// condenseProfileSeries turns the SelectSeries response into one entry per
// series with a label map and the count and sum of the points. A series
// without points is left out, because it has no data to show.
func condenseProfileSeries(body []byte) ([]profileSeries, error) {
	var resp struct {
		Series []struct {
			Labels []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"labels"`
			Points []profilePoint `json:"points"`
		} `json:"series"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	out := make([]profileSeries, 0, len(resp.Series))
	for _, s := range resp.Series {
		if len(s.Points) == 0 {
			continue
		}
		labels := make(map[string]string, len(s.Labels))
		for _, l := range s.Labels {
			labels[l.Name] = l.Value
		}
		sum := 0.0
		for _, p := range s.Points {
			sum += p.Value
		}
		out = append(out, profileSeries{Labels: labels, Count: len(s.Points), Sum: sum, Points: s.Points})
	}
	return out, nil
}

// profilesSeriesStep returns the series step in seconds. The default is
// profilesDefaultStep, or a larger step for a long range, so that one series
// does not get too many points to read.
func profilesSeriesStep(rangeSec int64) int64 {
	if rangeSec/profilesDefaultStep <= traceQLMaxPoints {
		return profilesDefaultStep
	}
	return defaultTraceQLStep(rangeSec)
}

func newProfilesSeriesCmd() *cobra.Command {
	var (
		typeID  string
		query   string
		groupBy []string
		stepStr string
	)
	cmd := &cobra.Command{
		Use:   "series",
		Short: "Show a profile type as time series",
		Long: `Show a profile type as time series. Each point is the sum of the samples
in one step, or the average for gauge types such as memory in use.

Use --query to select profiles by label, and --group-by to get one series
for each label value. The table shows one row per series with the number of
points and the last, minimum, maximum and average values. Use -o graph for
a chart, -o stats for a summary, or -o json for the points.

When --step is not set, the step is 15s, or larger for a long range.`,
		Example: `  oodle profiles series --type process_cpu:cpu:nanoseconds:cpu:nanoseconds --group-by service_name
  oodle profiles series --type goroutines:goroutines:count:goroutine:count --query '{service_name="api"}' -o graph
  oodle profiles series --type memory:inuse_space:bytes:space:bytes --start -24h --step 5m`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	f := cmd.Flags()
	f.StringVar(&typeID, "type", "", "Profile type ID (from 'oodle profiles types')")
	f.StringVar(&query, "query", "", `Label selector, such as '{service_name="api"}'`)
	f.StringSliceVar(&groupBy, "group-by", nil, "Label names to group by (repeat the flag or separate with commas)")
	f.StringVar(&stepStr, "step", "", "Resolution step, such as 15s, 5m or 1h (default: 15s, larger for long ranges)")
	_ = cmd.MarkFlagRequired("type")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
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
			step = profilesSeriesStep((end - start) / 1000)
		}
		payload := map[string]any{
			"profile_typeID": typeID,
			"label_selector": query,
			"start":          start,
			"end":            end,
			"step":           float64(step),
		}
		if len(groupBy) > 0 {
			payload["group_by"] = groupBy
		}
		body, err := profilesPost(cmd, "SelectSeries", payload)
		if err != nil {
			return err
		}
		series, err := condenseProfileSeries(body)
		if err != nil {
			return err
		}
		if err := printResult(cmd, series, func(format output.Format) error {
			prom := make([]output.PromSeries, 0, len(series))
			for _, s := range series {
				values := make([]output.PromSample, 0, len(s.Points))
				for _, p := range s.Points {
					values = append(values, output.PromSample{Timestamp: float64(p.Timestamp) / 1000, Value: p.Value})
				}
				sort.Slice(values, func(i, j int) bool { return values[i].Timestamp < values[j].Timestamp })
				prom = append(prom, output.PromSeries{Labels: s.Labels, Values: values})
			}
			switch format {
			case output.FormatGraph:
				return output.PrintGraph(cmd.OutOrStdout(), prom)
			case output.FormatStats:
				return output.PrintStats(cmd.OutOrStdout(), prom)
			}
			return output.Print(cmd.OutOrStdout(), format, traceQLMetricsRows(prom), traceQLMetricsColumns)
		}); err != nil {
			return err
		}
		if len(series) == 0 {
			hintNoData(cmd, "profile series", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- flamegraph ---

type flameGraph struct {
	Names  []string `json:"names"`
	Levels []struct {
		Values []int64 `json:"values"`
	} `json:"levels"`
	Total int64 `json:"total"`
}

// profileFunction is one function of 'profiles flamegraph'. The JSON keys
// are the keys of the MCP tool result.
type profileFunction struct {
	Function string  `json:"function"`
	Self     int64   `json:"self"`
	SelfPct  float64 `json:"self_pct"`
	Total    int64   `json:"total"`
}

// flameGraphSummary is the condensed flame graph: the total value and the
// functions with the most self value.
type flameGraphSummary struct {
	Total        int64             `json:"total"`
	TopFunctions []profileFunction `json:"top_functions"`
}

// topProfileFunctions adds the self and total values of each function over
// all flame graph nodes, and returns the top n functions by self value.
//
// Each level holds one group of four values per node: offset, total, self
// and the index of the name. The root node "total" is left out, because it
// is the sum of all functions and not a function.
func topProfileFunctions(fg flameGraph, n int) []profileFunction {
	self := map[int64]int64{}
	total := map[int64]int64{}
	for _, level := range fg.Levels {
		v := level.Values
		for i := 0; i+3 < len(v); i += 4 {
			if v[i+2] > 0 {
				self[v[i+3]] += v[i+2]
			}
			total[v[i+3]] += v[i+1]
		}
	}
	out := make([]profileFunction, 0, len(self))
	for idx, s := range self {
		name := fmt.Sprintf("unknown_%d", idx)
		if idx >= 0 && idx < int64(len(fg.Names)) {
			name = fg.Names[idx]
		}
		if name == "total" {
			continue
		}
		pct := 0.0
		if fg.Total > 0 {
			pct = math.Round(float64(s)/float64(fg.Total)*10000) / 100
		}
		out = append(out, profileFunction{Function: name, Self: s, SelfPct: pct, Total: total[idx]})
	}
	// Sort ties by total and name, so the same data gives the same order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Self != out[j].Self {
			return out[i].Self > out[j].Self
		}
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Function < out[j].Function
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// profileValueCell formats a flame graph value in the sample unit of the
// profile type, so that CPU time shows as a duration and memory as bytes.
func profileValueCell(v int64, unit string) string {
	switch unit {
	case "nanoseconds":
		// Keep about three significant digits, so that small values do
		// not round to zero and large values stay short.
		d := time.Duration(v)
		switch {
		case d >= time.Second:
			d = d.Round(time.Millisecond)
		case d >= time.Millisecond:
			d = d.Round(time.Microsecond)
		}
		return d.String()
	case "bytes":
		return bytesCell(v)
	}
	return strconv.FormatInt(v, 10)
}

// bytesCell formats a number of bytes with a binary unit, such as 1.5 MiB.
func bytesCell(v int64) string {
	const unit = 1024
	if v < unit {
		return strconv.FormatInt(v, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := v / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(v)/float64(div), "KMGTPE"[exp])
}

// profileSampleUnit returns the sample unit part of a profile type ID.
func profileSampleUnit(typeID string) string {
	parts := strings.Split(typeID, ":")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

type profileFunctionRow struct {
	Function string
	Self     string
	SelfPct  string
	Total    string
}

var profileFunctionColumns = []output.Column{
	{Header: "FUNCTION", Field: "Function"},
	{Header: "SELF", Field: "Self"},
	{Header: "SELF %", Field: "SelfPct"},
	{Header: "TOTAL", Field: "Total"},
}

func newProfilesFlamegraphCmd() *cobra.Command {
	var (
		typeID   string
		query    string
		maxNodes int64
		top      int
		raw      bool
	)
	cmd := &cobra.Command{
		Use:   "flamegraph",
		Short: "Show the functions that use the most resource",
		Long: `Merge the profiles of one type in the time range into a flame graph, and
show the functions that use the most CPU, memory or other resource.

SELF is the value used in the function itself, without the functions that
it calls. TOTAL includes the functions that it calls. The rows are sorted
by SELF.

Use -o json for the total and the top functions. Use --raw to get the full
flame graph tree from the server as JSON (or YAML with -o yaml).`,
		Example: `  oodle profiles flamegraph --type process_cpu:cpu:nanoseconds:cpu:nanoseconds
  oodle profiles flamegraph --type memory:alloc_space:bytes:space:bytes --query '{service_name="api"}' --top 10
  oodle profiles flamegraph --type process_cpu:cpu:nanoseconds:cpu:nanoseconds --max-nodes 512 --raw`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	f := cmd.Flags()
	f.StringVar(&typeID, "type", "", "Profile type ID (from 'oodle profiles types')")
	f.StringVar(&query, "query", "", `Label selector, such as '{service_name="api"}'`)
	f.Int64Var(&maxNodes, "max-nodes", profilesDefaultMaxNodes, "Largest number of nodes that the server keeps in the flame graph")
	f.IntVar(&top, "top", profilesDefaultTop, "Number of functions to show")
	f.BoolVar(&raw, "raw", false, "Print the full flame graph from the server instead of the top functions")
	_ = cmd.MarkFlagRequired("type")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if maxNodes <= 0 {
			return fmt.Errorf("--max-nodes must be greater than zero")
		}
		if top <= 0 {
			return fmt.Errorf("--top must be greater than zero")
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		selector := query
		if selector == "" {
			selector = "{}"
		}
		body, err := profilesPost(cmd, "SelectMergeStacktraces", map[string]any{
			"profile_typeID": typeID,
			"label_selector": selector,
			"start":          start,
			"end":            end,
			"max_nodes":      maxNodes,
		})
		if err != nil {
			return err
		}
		var resp struct {
			Flamegraph flameGraph `json:"flamegraph"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
		fg := resp.Flamegraph
		summary := flameGraphSummary{Total: fg.Total, TopFunctions: topProfileFunctions(fg, top)}
		if raw {
			format := getOutputFormat(cmd)
			if format != output.FormatYAML {
				format = output.FormatJSON
			}
			if _, err := printTraceQLBody(cmd, format, body); err != nil {
				return err
			}
		} else if err := printResult(cmd, summary, func(format output.Format) error {
			unit := profileSampleUnit(typeID)
			rows := make([]profileFunctionRow, 0, len(summary.TopFunctions))
			for _, fn := range summary.TopFunctions {
				rows = append(rows, profileFunctionRow{
					Function: shortCell(fn.Function, profilesFunctionWidth),
					Self:     profileValueCell(fn.Self, unit),
					SelfPct:  strconv.FormatFloat(fn.SelfPct, 'f', 2, 64),
					Total:    profileValueCell(fn.Total, unit),
				})
			}
			return output.Print(cmd.OutOrStdout(), format, rows, profileFunctionColumns)
		}); err != nil {
			return err
		}
		if fg.Total == 0 || len(summary.TopFunctions) == 0 {
			hintNoData(cmd, "profile samples", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}
