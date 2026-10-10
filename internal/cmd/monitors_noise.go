package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
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
	// noiseMonitorIDLabel is the ALERTS label that holds the monitor ID.
	noiseMonitorIDLabel = "_oodle_monitor_id"
	// noiseSeverityLabel is the ALERTS label that holds the severity.
	noiseSeverityLabel = "_oodle_severity"

	// noisyMTTRHours and noisyTriggersPerWeek are the limits from which a
	// monitor counts as noisy.
	noisyMTTRHours       = 12
	noisyTriggersPerWeek = 10

	// noiseStormSeries is the number of series that fire at the same time
	// from which a monitor counts as a storm.
	noiseStormSeries = 10

	defaultNoiseTop          = 10
	defaultNoiseBreakdownTop = 20
)

// noiseCategories are the categories that --category accepts.
var noiseCategories = []string{"flapping", "perpetual", "auto_resolving", "storm", "boundary", "uncategorized"}

// noiseRecommendations give the next step for each category.
var noiseRecommendations = map[string]string{
	"flapping":       "Tune threshold or add hysteresis to prevent rapid toggling",
	"perpetual":      "Review if this alert is actionable or should be tuned/silenced",
	"auto_resolving": "Consider increasing 'for' duration to reduce transient alerts",
	"storm":          "Add aggregation or grouping to reduce cardinality",
	"boundary":       "Adjust threshold or add buffer to avoid crossings",
	"uncategorized":  "Review alert configuration for optimization opportunities",
}

// noiseStepSec returns the query step in seconds for a range of days. A
// longer range uses a longer step, so that each series stays inside the
// point limit of a range query.
func noiseStepSec(days float64) int64 {
	switch {
	case days <= 1:
		return 15
	case days <= 2:
		return 30
	case days <= 7:
		return 60
	case days <= 30:
		return 300
	case days <= 90:
		return 900
	}
	return 3600
}

// noiseEpisode is one firing episode: the first and the last point.
type noiseEpisode struct {
	start, end float64
}

// detectFiringEpisodes splits the points of one series into episodes. A gap
// of more than two steps, or a point with value 0, ends an episode. ALERTS
// has no points while an alert does not fire, so each gap is a resolve.
func detectFiringEpisodes(points []promPoint, stepSec int64) []noiseEpisode {
	var out []noiseEpisode
	gap := float64(2 * stepSec)
	open := false
	var cur, prev float64
	for _, p := range points {
		if p.value > 0 {
			switch {
			case !open:
				cur, open = p.ts, true
			case p.ts-prev > gap:
				out = append(out, noiseEpisode{cur, prev})
				cur = p.ts
			}
			prev = p.ts
			continue
		}
		if open {
			out = append(out, noiseEpisode{cur, prev})
			open = false
		}
	}
	if open {
		out = append(out, noiseEpisode{cur, prev})
	}
	return out
}

// classifyNoise gives the category of a monitor from its firing pattern. A
// storm is checked first, because many series that fire together look like
// any other category when they are counted one by one.
func classifyNoise(triggers int, avgMinutes, perDay float64, maxSeries int) string {
	switch {
	case maxSeries > noiseStormSeries:
		return "storm"
	case perDay >= 1.5 && avgMinutes < 30:
		return "flapping"
	case triggers <= 2 && avgMinutes > 720:
		return "perpetual"
	case avgMinutes < 15 && triggers >= 5:
		return "auto_resolving"
	case perDay >= 0.7 && avgMinutes < 60:
		return "boundary"
	}
	return "uncategorized"
}

// isNoisy reports whether a monitor passes the noisy limits for a range of
// days.
func isNoisy(triggers int, mttrHours, days float64) bool {
	return mttrHours >= noisyMTTRHours || float64(triggers) >= noisyTriggersPerWeek*days/7
}

// noiseMonitor holds the fields of a monitor that the noise commands read.
type noiseMonitor struct {
	ID                               string                    `json:"id"`
	Name                             string                    `json:"name"`
	NotificationPolicyID             string                    `json:"notification_policy_id"`
	Notifications                    []noiseNotificationsEntry `json:"notifications"`
	LabelMatcherNotificationPolicies []noiseNotificationsEntry `json:"label_matcher_notification_policies"`
}

type noiseNotificationsEntry struct {
	NotificationPolicyID string              `json:"notification_policy_id"`
	Notifiers            map[string][]string `json:"notifiers"`
}

func (e noiseNotificationsEntry) routed() bool {
	if e.NotificationPolicyID != "" {
		return true
	}
	for _, ids := range e.Notifiers {
		for _, id := range ids {
			if id != "" {
				return true
			}
		}
	}
	return false
}

// routed reports whether the monitor sends its alerts to a notifier or a
// notification policy. A monitor without routing pages nobody, so its alerts
// are not on-call noise. Global notification policies are not read.
func (m noiseMonitor) routed() bool {
	if m.NotificationPolicyID != "" {
		return true
	}
	for _, e := range m.Notifications {
		if e.routed() {
			return true
		}
	}
	for _, e := range m.LabelMatcherNotificationPolicies {
		if e.routed() {
			return true
		}
	}
	return false
}

// readRawBody reads the body of a 2xx response. The noise commands decode
// the body into small types of their own: the generated types reject the
// whole list when one object has a field that does not parse.
func readRawBody(resp *http.Response, err error) ([]byte, error) {
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
	return body, nil
}

// fetchNoiseMonitors returns the monitors by ID.
func fetchNoiseMonitors(cmd *cobra.Command) (map[string]noiseMonitor, error) {
	body, err := readRawBody(getClient(cmd).Inner.ListMonitors(cmd.Context(), getInstance(cmd)))
	if err != nil {
		return nil, err
	}
	var list []noiseMonitor
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing monitors: %w", err)
	}
	out := make(map[string]noiseMonitor, len(list))
	for _, m := range list {
		if m.ID != "" {
			out[m.ID] = m
		}
	}
	return out, nil
}

type noiseMutingRule struct {
	StartsAt    string   `json:"startsAt"`
	EndsAt      string   `json:"endsAt"`
	ScheduleIDs []string `json:"scheduleIds"`
	Matchers    []struct {
		Name  string `json:"name"`
		Type  int    `json:"type"`
		Value string `json:"value"`
	} `json:"matchers"`
}

// active reports whether the rule mutes at now. A rule with a schedule
// counts as active, because it mutes again on each scheduled window.
func (r noiseMutingRule) active(now time.Time) bool {
	if len(r.ScheduleIDs) > 0 {
		return true
	}
	if t, err := time.Parse(time.RFC3339, r.StartsAt); err == nil && t.After(now) {
		return false
	}
	if t, err := time.Parse(time.RFC3339, r.EndsAt); err == nil && t.Year() > 1 && !t.After(now) {
		return false
	}
	return true
}

// fetchMutedMonitorIDs returns the IDs of the monitors that an active muting
// rule mutes by an equal match on the monitor ID label.
func fetchMutedMonitorIDs(cmd *cobra.Command) (map[string]bool, error) {
	body, err := readRawBody(getClient(cmd).Inner.ListMutingRules(cmd.Context(), getInstance(cmd)))
	if err != nil {
		return nil, err
	}
	var rules []noiseMutingRule
	if err := json.Unmarshal(body, &rules); err != nil {
		return nil, fmt.Errorf("parsing muting rules: %w", err)
	}
	now := time.Now()
	out := map[string]bool{}
	for _, r := range rules {
		if !r.active(now) {
			continue
		}
		for _, m := range r.Matchers {
			if m.Name == noiseMonitorIDLabel && m.Type == 0 && m.Value != "" {
				out[m.Value] = true
			}
		}
	}
	return out, nil
}

// warnLookup writes a warning to stderr when a lookup that only removes rows
// fails. The analysis continues with all rows, so that a missing permission
// does not stop the command.
func warnLookup(w io.Writer, what string, err error) {
	fmt.Fprintf(w, "Warning: could not read %s, so no rows are removed for them: %v\n", what, err)
}

// parseLabelFilters parses key=value pairs into PromQL matchers.
func parseLabelFilters(pairs []string) ([]string, map[string]string, error) {
	var matchers []string
	applied := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || !isPromLabelName(k) {
			return nil, nil, fmt.Errorf("invalid --label %q: use name=value", p)
		}
		matchers = append(matchers, k+"="+promQuote(v))
		applied[k] = v
	}
	return matchers, applied, nil
}

// --- noise ---

type noiseMonitorResult struct {
	MonitorID      string   `json:"monitor_id"`
	Name           string   `json:"name"`
	TriggerCount   int      `json:"trigger_count"`
	AvgDurationMin float64  `json:"avg_duration_minutes"`
	MTTRHours      float64  `json:"mttr_hours"`
	TriggersPerDay float64  `json:"triggers_per_day"`
	MaxSeries      int      `json:"max_concurrent_series"`
	Category       string   `json:"noise_category"`
	Noisy          bool     `json:"is_noisy"`
	Severities     []string `json:"severities"`
	Recommendation string   `json:"recommendation"`
	Muted          bool     `json:"is_muted,omitempty"`
}

type noiseSummary struct {
	MonitorsAnalyzed int            `json:"total_monitors_analyzed"`
	NoisyCount       int            `json:"noisy_monitors_count"`
	TotalTriggers    int            `json:"total_triggers"`
	ByCategory       map[string]int `json:"by_category"`
	Start            string         `json:"start"`
	End              string         `json:"end"`
	Days             float64        `json:"time_range_days"`
	StepSeconds      int64          `json:"step_seconds"`
	MutedExcluded    int            `json:"muted_monitors_excluded,omitempty"`
	UnroutedExcluded int            `json:"unrouted_monitors_excluded,omitempty"`
}

type noiseResult struct {
	Monitors []noiseMonitorResult `json:"noisy_monitors"`
	Summary  noiseSummary         `json:"summary"`
	Filters  map[string]string    `json:"filters_applied,omitempty"`
}

type noiseRow struct {
	Name        string
	ID          string
	Triggers    int
	AvgDuration string
	PerDay      string
	MaxSeries   int
	Category    string
	Noisy       string
}

var noiseColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "ID"},
	{Header: "TRIGGERS", Field: "Triggers"},
	{Header: "AVG DURATION", Field: "AvgDuration"},
	{Header: "PER DAY", Field: "PerDay"},
	{Header: "MAX SERIES", Field: "MaxSeries"},
	{Header: "CATEGORY", Field: "Category"},
	{Header: "NOISY", Field: "Noisy"},
}

// formatMinutes formats a number of minutes for a table cell.
func formatMinutes(m float64) string {
	d := time.Duration(m * float64(time.Minute)).Round(time.Second)
	return d.String()
}

func noiseRows(monitors []noiseMonitorResult) []noiseRow {
	rows := make([]noiseRow, 0, len(monitors))
	for _, m := range monitors {
		noisy := "no"
		if m.Noisy {
			noisy = "yes"
		}
		rows = append(rows, noiseRow{
			Name:        m.Name,
			ID:          m.MonitorID,
			Triggers:    m.TriggerCount,
			AvgDuration: formatMinutes(m.AvgDurationMin),
			PerDay:      strconv.FormatFloat(m.TriggersPerDay, 'f', 2, 64),
			MaxSeries:   m.MaxSeries,
			Category:    m.Category,
			Noisy:       noisy,
		})
	}
	return rows
}

// noiseInput is what the noise analysis reads. It holds no API access, so
// that tests can give it fixed data.
type noiseInput struct {
	firing         []promSeries
	storm          []promSeries
	monitors       map[string]noiseMonitor
	muted          map[string]bool
	days           float64
	stepSec        int64
	includeMuted   bool
	includeUnroute bool
	category       string
	sortBy         string
	top            int
}

// analyzeNoise computes the noise result from the query results.
func analyzeNoise(in noiseInput) noiseResult {
	maxSeries := map[string]int{}
	for _, s := range in.storm {
		if id := s.Metric[noiseMonitorIDLabel]; id != "" {
			if v, ok := s.instantValue(); ok {
				maxSeries[id] = int(v)
			}
		}
	}

	type stats struct {
		triggers   int
		minutes    float64
		maxSeries  int
		severities map[string]bool
	}
	byMonitor := map[string]*stats{}
	var order []string
	for _, s := range in.firing {
		id := s.Metric[noiseMonitorIDLabel]
		if id == "" {
			continue
		}
		episodes := detectFiringEpisodes(s.points(), in.stepSec)
		if len(episodes) == 0 {
			continue
		}
		st := byMonitor[id]
		if st == nil {
			ms := maxSeries[id]
			if ms == 0 {
				ms = 1
			}
			st = &stats{maxSeries: ms, severities: map[string]bool{}}
			byMonitor[id] = st
			order = append(order, id)
		}
		st.triggers += len(episodes)
		for _, e := range episodes {
			st.minutes += (e.end - e.start) / 60
		}
		sev := s.Metric[noiseSeverityLabel]
		if sev == "" {
			sev = "unknown"
		}
		st.severities[sev] = true
	}

	res := noiseResult{
		Monitors: []noiseMonitorResult{},
		Summary: noiseSummary{
			MonitorsAnalyzed: len(byMonitor),
			ByCategory:       map[string]int{},
			Days:             round(in.days, 2),
			StepSeconds:      in.stepSec,
		},
	}
	for _, id := range order {
		st := byMonitor[id]
		avg := st.minutes / float64(st.triggers)
		mttr := avg / 60
		perDay := 0.0
		if in.days > 0 {
			perDay = float64(st.triggers) / in.days
		}
		category := classifyNoise(st.triggers, avg, perDay, st.maxSeries)
		noisy := isNoisy(st.triggers, mttr, in.days)
		res.Summary.ByCategory[category]++
		res.Summary.TotalTriggers += st.triggers
		if noisy {
			res.Summary.NoisyCount++
		}

		muted := in.muted[id]
		if muted && !in.includeMuted {
			res.Summary.MutedExcluded++
			continue
		}
		// A monitor that the list does not have is kept, so that a
		// failed or partial monitor list removes no rows.
		if m, ok := in.monitors[id]; ok && !in.includeUnroute && !m.routed() {
			res.Summary.UnroutedExcluded++
			continue
		}
		if in.category != "" && category != in.category {
			continue
		}
		name := in.monitors[id].Name
		if name == "" {
			name = "Monitor " + id
		}
		sevs := make([]string, 0, len(st.severities))
		for s := range st.severities {
			sevs = append(sevs, s)
		}
		sort.Strings(sevs)
		res.Monitors = append(res.Monitors, noiseMonitorResult{
			MonitorID:      id,
			Name:           name,
			TriggerCount:   st.triggers,
			AvgDurationMin: round(avg, 1),
			MTTRHours:      round(mttr, 2),
			TriggersPerDay: round(perDay, 2),
			MaxSeries:      st.maxSeries,
			Category:       category,
			Noisy:          noisy,
			Severities:     sevs,
			Recommendation: noiseRecommendations[category],
			Muted:          muted && in.includeMuted,
		})
	}

	sort.SliceStable(res.Monitors, func(i, j int) bool {
		a, b := res.Monitors[i], res.Monitors[j]
		if in.sortBy == "mttr" {
			if a.MTTRHours != b.MTTRHours {
				return a.MTTRHours > b.MTTRHours
			}
		} else if a.TriggerCount != b.TriggerCount {
			return a.TriggerCount > b.TriggerCount
		}
		return a.MonitorID < b.MonitorID
	})
	if in.top > 0 && len(res.Monitors) > in.top {
		res.Monitors = res.Monitors[:in.top]
	}
	return res
}

func newMonitorsNoiseCmd() *cobra.Command {
	var (
		top             int
		sortBy          string
		category        string
		labels          []string
		includeMuted    bool
		includeUnrouted bool
	)
	cmd := &cobra.Command{
		Use:   "noise",
		Short: "Find the noisiest monitors in a time range",
		Long: `Find the noisiest monitors in a time range.

The command reads the firing series of the ALERTS metric, and splits each
series into firing episodes. A gap of more than two query steps ends an
episode. For each monitor it shows the number of episodes (TRIGGERS), their
average duration, the episodes per day, and the largest number of series
that fired at the same time.

A monitor is noisy when its average episode lasts 12 hours or more, or when
it has 10 or more episodes for each 7 days of the range.

Categories:
  storm           More than 10 series fire at the same time.
  flapping        1.5 or more episodes a day, shorter than 30 minutes.
  perpetual       2 or fewer episodes, longer than 12 hours.
  auto_resolving  5 or more episodes, shorter than 15 minutes.
  boundary        0.7 or more episodes a day, shorter than 60 minutes.

Muted monitors, and monitors that send to no notifier or notification
policy, are not shown. Use --include-muted and --include-unrouted to show
them. -o json adds the severities and a recommendation for each monitor.`,
		Example: `  # Top 10 monitors by episodes in the last 7 days
  oodle monitors noise

  # Flapping monitors in production over 30 days
  oodle monitors noise --start -30d --label env=production --category flapping

  # Monitors that stay in the firing state longest
  oodle monitors noise --sort mttr --top 20 -o json`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlagsSec(cmd, defaultHistoryStartOffset)
	cmd.Flags().IntVar(&top, "top", defaultNoiseTop, "Number of monitors to show")
	cmd.Flags().StringVar(&sortBy, "sort", "triggers", "Sort by: triggers or mttr")
	cmd.Flags().StringVar(&category, "category", "", "Only show this category: "+strings.Join(noiseCategories, ", "))
	cmd.Flags().StringArrayVar(&labels, "label", nil, "Only read alerts with this label, as name=value (repeatable)")
	cmd.Flags().BoolVar(&includeMuted, "include-muted", false, "Also show monitors that an active muting rule mutes")
	cmd.Flags().BoolVar(&includeUnrouted, "include-unrouted", false, "Also show monitors that send to no notifier or notification policy")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if sortBy != "triggers" && sortBy != "mttr" {
			return fmt.Errorf("--sort must be triggers or mttr, got %q", sortBy)
		}
		if category != "" && !slices.Contains(noiseCategories, category) {
			return fmt.Errorf("--category must be one of %s", strings.Join(noiseCategories, ", "))
		}
		if top < 1 {
			return fmt.Errorf("--top must be at least 1")
		}
		matchers, applied, err := parseLabelFilters(labels)
		if err != nil {
			return err
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		rangeSec := end - start
		days := float64(rangeSec) / 86400
		step := noiseStepSec(math.Ceil(days))
		selector := strings.Join(append([]string{`alertstate="firing"`}, matchers...), ", ")

		firing, err := promQueryRange(cmd,
			fmt.Sprintf("sum by (%s, %s) (ALERTS{%s})", noiseMonitorIDLabel, noiseSeverityLabel, selector),
			float64(start), float64(end), strconv.FormatInt(step, 10)+"s")
		if err != nil {
			return err
		}
		in := noiseInput{
			firing:         firing,
			days:           days,
			stepSec:        step,
			includeMuted:   includeMuted,
			includeUnroute: includeUnrouted,
			category:       category,
			sortBy:         sortBy,
			top:            top,
		}
		if len(firing) > 0 {
			stormRes := max(step, 60)
			in.storm, err = promQueryInstant(cmd,
				fmt.Sprintf("max_over_time(count(ALERTS{%s}) by (%s)[%ds:%ds])", selector, noiseMonitorIDLabel, rangeSec, stormRes),
				float64(end))
			if err != nil {
				return err
			}
			if in.monitors, err = fetchNoiseMonitors(cmd); err != nil {
				warnLookup(cmd.ErrOrStderr(), "monitors", err)
			}
			if !includeMuted {
				if in.muted, err = fetchMutedMonitorIDs(cmd); err != nil {
					warnLookup(cmd.ErrOrStderr(), "muting rules", err)
				}
			}
		}

		res := analyzeNoise(in)
		res.Summary.Start = time.Unix(start, 0).UTC().Format(time.RFC3339)
		res.Summary.End = time.Unix(end, 0).UTC().Format(time.RFC3339)
		if len(applied) > 0 {
			res.Filters = applied
		}
		if err := printComputed(cmd, res, noiseRows(res.Monitors), noiseColumns); err != nil {
			return err
		}
		if len(firing) == 0 {
			hintNoData(cmd, "firing alerts", time.Unix(start, 0), time.Unix(end, 0))
			return nil
		}
		if getOutputFormat(cmd) == output.FormatTable {
			s := res.Summary
			fmt.Fprintf(cmd.OutOrStdout(), "\n%d monitors fired, %d are noisy, %d episodes in all.",
				s.MonitorsAnalyzed, s.NoisyCount, s.TotalTriggers)
			if s.MutedExcluded > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), " %d muted not shown.", s.MutedExcluded)
			}
			if s.UnroutedExcluded > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), " %d unrouted not shown.", s.UnroutedExcluded)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return nil
	}
	return cmd
}

// --- noise-breakdown ---

type noiseBreakdownEntry struct {
	Value          string  `json:"value"`
	FiringMinutes  float64 `json:"firing_minutes"`
	TriggerCount   int     `json:"trigger_count"`
	PercentOfTotal float64 `json:"percentage"`
}

type noiseBreakdownResult struct {
	GroupBy   string                `json:"group_by"`
	MonitorID string                `json:"monitor_id,omitempty"`
	Breakdown []noiseBreakdownEntry `json:"breakdown"`
	Summary   struct {
		Dimensions          int     `json:"total_dimensions"`
		TotalFiringMinutes  float64 `json:"total_firing_minutes"`
		TotalTriggers       int     `json:"total_triggers"`
		Start               string  `json:"start"`
		End                 string  `json:"end"`
		StepSeconds         int64   `json:"step_seconds"`
		MutedSeriesExcluded int     `json:"muted_series_excluded,omitempty"`
	} `json:"summary"`
}

type noiseBreakdownRow struct {
	Value    string
	Firing   string
	Triggers int
	Percent  string
}

func noiseBreakdownColumns(groupBy string) []output.Column {
	return []output.Column{
		{Header: strings.ToUpper(groupBy), Field: "Value"},
		{Header: "FIRING TIME", Field: "Firing"},
		{Header: "TRIGGERS", Field: "Triggers"},
		{Header: "SHARE", Field: "Percent"},
	}
}

// analyzeNoiseBreakdown adds up the firing time and the episodes of each
// value of groupBy. A series of a muted monitor is left out.
func analyzeNoiseBreakdown(series []promSeries, groupBy string, stepSec int64, muted map[string]bool, top int) noiseBreakdownResult {
	var res noiseBreakdownResult
	res.GroupBy = groupBy
	agg := map[string]*noiseBreakdownEntry{}
	var total float64
	for _, s := range series {
		if muted[s.Metric[noiseMonitorIDLabel]] {
			res.Summary.MutedSeriesExcluded++
			continue
		}
		pts := s.points()
		if len(pts) == 0 {
			continue
		}
		val, ok := s.Metric[groupBy]
		if !ok {
			val = "(none)"
		}
		episodes := detectFiringEpisodes(pts, stepSec)
		var minutes float64
		for _, e := range episodes {
			minutes += (e.end - e.start) / 60
		}
		e := agg[val]
		if e == nil {
			e = &noiseBreakdownEntry{Value: val}
			agg[val] = e
		}
		e.FiringMinutes += minutes
		e.TriggerCount += len(episodes)
		total += minutes
		res.Summary.TotalTriggers += len(episodes)
	}
	res.Breakdown = make([]noiseBreakdownEntry, 0, len(agg))
	for _, e := range agg {
		e.PercentOfTotal = round(pct(e.FiringMinutes, total), 1)
		e.FiringMinutes = round(e.FiringMinutes, 1)
		res.Breakdown = append(res.Breakdown, *e)
	}
	sort.Slice(res.Breakdown, func(i, j int) bool {
		a, b := res.Breakdown[i], res.Breakdown[j]
		if a.FiringMinutes != b.FiringMinutes {
			return a.FiringMinutes > b.FiringMinutes
		}
		return a.Value < b.Value
	})
	res.Summary.Dimensions = len(res.Breakdown)
	if top > 0 && len(res.Breakdown) > top {
		res.Breakdown = res.Breakdown[:top]
	}
	res.Summary.TotalFiringMinutes = round(total, 1)
	res.Summary.StepSeconds = stepSec
	return res
}

func newMonitorsNoiseBreakdownCmd() *cobra.Command {
	var (
		groupBy      string
		monitorID    string
		top          int
		includeMuted bool
	)
	cmd := &cobra.Command{
		Use:   "noise-breakdown",
		Short: "Show which label values cause the most alert firing time",
		Long: `Show which values of one label cause the most alert firing time.

The command reads the firing series of the ALERTS metric, grouped by the
--group-by label, and splits each series into firing episodes. For each
label value it shows the total firing time, the number of episodes, and the
share of all firing time.

Use any label that is on the ALERTS series, such as namespace, service, pod,
cluster or env. Run 'oodle metrics labels ALERTS' to see the labels.
Give --monitor to read the alerts of one monitor only. Series of muted
monitors are not counted; use --include-muted to count them.`,
		Example: `  # Which namespaces fire the most in the last 7 days
  oodle monitors noise-breakdown --group-by namespace

  # Which pods make one monitor noisy
  oodle monitors noise-breakdown --group-by pod --monitor 0193f692-b95e-7cf7-a00b-56bebd929480

  # Environments over 30 days, as JSON
  oodle monitors noise-breakdown --group-by env --start -30d -o json`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlagsSec(cmd, defaultHistoryStartOffset)
	cmd.Flags().StringVar(&groupBy, "group-by", "", "Label to group by, such as namespace or service (required)")
	cmd.Flags().StringVar(&monitorID, "monitor", "", "Only read the alerts of this monitor ID")
	cmd.Flags().IntVar(&top, "top", defaultNoiseBreakdownTop, "Number of label values to show")
	cmd.Flags().BoolVar(&includeMuted, "include-muted", false, "Also count the series of monitors that an active muting rule mutes")
	_ = cmd.MarkFlagRequired("group-by")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		groupBy = strings.TrimSpace(groupBy)
		if !isPromLabelName(groupBy) {
			return fmt.Errorf("--group-by %q is not a valid label name", groupBy)
		}
		if top < 1 {
			return fmt.Errorf("--top must be at least 1")
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		days := math.Max(1, math.Floor(float64(end-start)/86400))
		step := noiseStepSec(days)

		var muted map[string]bool
		if !includeMuted {
			if muted, err = fetchMutedMonitorIDs(cmd); err != nil {
				warnLookup(cmd.ErrOrStderr(), "muting rules", err)
			}
		}
		selector := `alertstate="firing"`
		if monitorID != "" {
			selector += ", " + noiseMonitorIDLabel + "=" + promQuote(monitorID)
		}
		by := groupBy
		if len(muted) > 0 && groupBy != noiseMonitorIDLabel {
			by += ", " + noiseMonitorIDLabel
		}
		series, err := promQueryRange(cmd, fmt.Sprintf("sum by (%s) (ALERTS{%s})", by, selector),
			float64(start), float64(end), strconv.FormatInt(step, 10)+"s")
		if err != nil {
			return err
		}
		res := analyzeNoiseBreakdown(series, groupBy, step, muted, top)
		res.MonitorID = monitorID
		res.Summary.Start = time.Unix(start, 0).UTC().Format(time.RFC3339)
		res.Summary.End = time.Unix(end, 0).UTC().Format(time.RFC3339)

		rows := make([]noiseBreakdownRow, 0, len(res.Breakdown))
		for _, e := range res.Breakdown {
			rows = append(rows, noiseBreakdownRow{
				Value:    e.Value,
				Firing:   formatMinutes(e.FiringMinutes),
				Triggers: e.TriggerCount,
				Percent:  formatPercent(e.PercentOfTotal),
			})
		}
		if err := printComputed(cmd, res, rows, noiseBreakdownColumns(groupBy)); err != nil {
			return err
		}
		if len(res.Breakdown) == 0 {
			hintNoData(cmd, "firing alerts", time.Unix(start, 0), time.Unix(end, 0))
		}
		return nil
	}
	return cmd
}

// isPromLabelName reports whether s is a valid PromQL label name. The name
// goes into the query text without quotes, so a check stops a value that
// changes the query.
func isPromLabelName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
