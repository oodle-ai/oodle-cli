package cmd

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	// traceAnomalySlowNs is the span duration from which a span counts as
	// slow. Buckets at or above it add to the slow share.
	traceAnomalySlowNs = 100 * float64(time.Millisecond)

	// traceAnomalyMaxPoints limits the points per series of the query.
	// Without a limit, a long range with a one-minute step reads too many
	// points.
	traceAnomalyMaxPoints = 1440

	// traceAnomalyMaxPeriods limits the number of periods in the default
	// period length, so that the table stays short.
	traceAnomalyMaxPeriods = 60

	// traceAnomalyMinPeriod is the shortest default period. A shorter
	// period has too few spans to give a stable error rate.
	traceAnomalyMinPeriod = 5 * 60
)

// traceAnomalyStep returns the query step in seconds for a range: one
// minute, or a larger whole number of minutes so that the range has at most
// traceAnomalyMaxPoints points.
func traceAnomalyStep(rangeSec int64) int64 {
	step := int64(60)
	if rangeSec/step <= traceAnomalyMaxPoints {
		return step
	}
	perPoint := (rangeSec + traceAnomalyMaxPoints - 1) / traceAnomalyMaxPoints
	return (perPoint + 59) / 60 * 60
}

// traceAnomalyPeriod returns the default period length in seconds. It is a
// whole number of steps, so that each period holds the same number of
// points.
func traceAnomalyPeriod(rangeSec, step int64) int64 {
	p := (rangeSec + traceAnomalyMaxPeriods - 1) / traceAnomalyMaxPeriods
	if p < traceAnomalyMinPeriod {
		p = traceAnomalyMinPeriod
	}
	return (p + step - 1) / step * step
}

// traceAnomalyQuery builds the query that counts spans per duration bucket
// and status.
func traceAnomalyQuery(service, namespace, cluster, env string, step int64) string {
	filters := []string{"service_name=" + promQuote(service)}
	if namespace != "" {
		filters = append(filters, "namespace="+promQuote(namespace))
	}
	if cluster != "" {
		filters = append(filters, "cluster="+promQuote(cluster))
	}
	if env != "" {
		filters = append(filters, "env="+promQuote(env))
	}
	return fmt.Sprintf("sum by (duration_ns_bucket, span_status) (increase(oodle_trace_metrics{%s}[%ds]))",
		strings.Join(filters, ", "), step)
}

type traceAnomalyStats struct {
	TotalSpans      int64   `json:"total_spans"`
	ErrorSpans      int64   `json:"error_spans"`
	ErrorRatePct    float64 `json:"error_rate_percent"`
	SlowSpans       int64   `json:"slow_spans"`
	SlowRatePct     float64 `json:"slow_rate_percent"`
	SlowThresholdMs int64   `json:"slow_threshold_ms"`
}

type traceAnomalyBucket struct {
	Duration string  `json:"duration"`
	Count    int64   `json:"count"`
	Percent  float64 `json:"percentage"`
}

type traceAnomalyPeriodResult struct {
	Time         string               `json:"timestamp"`
	TotalSpans   int64                `json:"total_spans"`
	ErrorRatePct float64              `json:"error_rate_percent"`
	SlowRatePct  float64              `json:"slow_rate_percent"`
	TopBuckets   []traceAnomalyBucket `json:"top_latency_buckets"`
	Anomalies    []string             `json:"anomalies,omitempty"`
}

type traceAnomalyResult struct {
	Service       string                     `json:"service_name"`
	Namespace     string                     `json:"namespace,omitempty"`
	Cluster       string                     `json:"cluster,omitempty"`
	Env           string                     `json:"env,omitempty"`
	Start         string                     `json:"start"`
	End           string                     `json:"end"`
	StepSeconds   int64                      `json:"step_seconds"`
	PeriodSeconds int64                      `json:"period_seconds"`
	Overall       traceAnomalyStats          `json:"overall_statistics"`
	Periods       []traceAnomalyPeriodResult `json:"temporal_analysis"`
	Insights      []string                   `json:"insights,omitempty"`
}

// traceAnomalySeries is one decoded series: its bucket bound, its status and
// its points.
type traceAnomalySeries struct {
	durationNs float64
	isError    bool
	points     []promPoint
}

func decodeTraceAnomalySeries(series []promSeries) []traceAnomalySeries {
	out := make([]traceAnomalySeries, 0, len(series))
	for _, s := range series {
		d, err := strconv.ParseFloat(s.Metric["duration_ns_bucket"], 64)
		if err != nil {
			d = 0
		}
		out = append(out, traceAnomalySeries{
			durationNs: d,
			isError:    s.Metric["span_status"] == "Error",
			points:     s.points(),
		})
	}
	return out
}

// formatBucketDuration formats a duration bucket bound in nanoseconds.
func formatBucketDuration(ns float64) string {
	if math.IsInf(ns, 1) {
		return "+Inf"
	}
	return time.Duration(ns).String()
}

func pct(part, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return part / total * 100
}

// analyzeTraceAnomalies computes the totals and the periods. A period is
// marked when its error rate is more than twice the average period error
// rate and above 5%, or when its slow share is more than 1.5 times the
// average and above 20%. The minimum values keep a quiet service from being
// marked for small changes.
func analyzeTraceAnomalies(series []traceAnomalySeries, period int64) (traceAnomalyStats, []traceAnomalyPeriodResult) {
	type agg struct {
		total, errors, slow float64
		byBucket            map[float64]float64
	}
	var total, errs, slow float64
	periods := map[int64]*agg{}
	for _, s := range series {
		isSlow := s.durationNs >= traceAnomalySlowNs
		for _, p := range s.points {
			total += p.value
			if s.isError {
				errs += p.value
			}
			if isSlow {
				slow += p.value
			}
			key := int64(p.ts) / period * period
			a := periods[key]
			if a == nil {
				a = &agg{byBucket: map[float64]float64{}}
				periods[key] = a
			}
			a.total += p.value
			if s.isError {
				a.errors += p.value
			}
			if isSlow {
				a.slow += p.value
			}
			a.byBucket[s.durationNs] += p.value
		}
	}
	stats := traceAnomalyStats{
		TotalSpans:      int64(total),
		ErrorSpans:      int64(errs),
		ErrorRatePct:    round(pct(errs, total), 2),
		SlowSpans:       int64(slow),
		SlowRatePct:     round(pct(slow, total), 2),
		SlowThresholdMs: int64(traceAnomalySlowNs / float64(time.Millisecond)),
	}
	if len(periods) == 0 {
		return stats, []traceAnomalyPeriodResult{}
	}

	keys := make([]int64, 0, len(periods))
	for k := range periods {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var errSum, slowSum float64
	for _, k := range keys {
		errSum += pct(periods[k].errors, periods[k].total)
		slowSum += pct(periods[k].slow, periods[k].total)
	}
	avgErr := errSum / float64(len(keys))
	avgSlow := slowSum / float64(len(keys))

	out := make([]traceAnomalyPeriodResult, 0, len(keys))
	for _, k := range keys {
		a := periods[k]
		if a.total <= 0 {
			continue
		}
		errRate := pct(a.errors, a.total)
		slowRate := pct(a.slow, a.total)
		var notes []string
		if errRate > avgErr*2 && errRate > 5 {
			notes = append(notes, fmt.Sprintf("High error rate: %.1f%% (baseline: %.1f%%)", errRate, avgErr))
		}
		if slowRate > avgSlow*1.5 && slowRate > 20 {
			notes = append(notes, fmt.Sprintf("Elevated latency: %.1f%% of spans >= %dms (baseline: %.1f%%)",
				slowRate, stats.SlowThresholdMs, avgSlow))
		}
		buckets := make([]float64, 0, len(a.byBucket))
		for b := range a.byBucket {
			buckets = append(buckets, b)
		}
		sort.Slice(buckets, func(i, j int) bool {
			ci, cj := a.byBucket[buckets[i]], a.byBucket[buckets[j]]
			if ci != cj {
				return ci > cj
			}
			return buckets[i] < buckets[j]
		})
		if len(buckets) > 3 {
			buckets = buckets[:3]
		}
		top := make([]traceAnomalyBucket, 0, len(buckets))
		for _, b := range buckets {
			top = append(top, traceAnomalyBucket{
				Duration: formatBucketDuration(b),
				Count:    int64(a.byBucket[b]),
				Percent:  round(pct(a.byBucket[b], a.total), 1),
			})
		}
		out = append(out, traceAnomalyPeriodResult{
			Time:         time.Unix(k, 0).UTC().Format(time.RFC3339),
			TotalSpans:   int64(a.total),
			ErrorRatePct: round(errRate, 2),
			SlowRatePct:  round(slowRate, 2),
			TopBuckets:   top,
			Anomalies:    notes,
		})
	}
	return stats, out
}

// traceAnomalyInsights gives the summary lines of the result.
func traceAnomalyInsights(stats traceAnomalyStats, periods []traceAnomalyPeriodResult) []string {
	var out []string
	if stats.ErrorRatePct > 5 {
		out = append(out, fmt.Sprintf("High overall error rate: %.2f%%", stats.ErrorRatePct))
	}
	if stats.SlowRatePct > 30 {
		out = append(out, fmt.Sprintf("Many slow spans: %.2f%% of spans >= %dms", stats.SlowRatePct, stats.SlowThresholdMs))
	}
	marked := 0
	for _, p := range periods {
		if len(p.Anomalies) > 0 {
			marked++
		}
	}
	if marked > 0 {
		out = append(out, fmt.Sprintf("Found %d periods with anomalies", marked))
	}
	return out
}

type traceAnomalyRow struct {
	Time       string
	Spans      int64
	ErrorRate  string
	SlowRate   string
	TopLatency string
	Anomalies  string
}

func traceAnomalyColumns(slowMs int64) []output.Column {
	return []output.Column{
		{Header: "PERIOD START (UTC)", Field: "Time"},
		{Header: "SPANS", Field: "Spans"},
		{Header: "ERROR RATE", Field: "ErrorRate"},
		{Header: fmt.Sprintf("SLOW (>= %dMS)", slowMs), Field: "SlowRate"},
		{Header: "TOP LATENCY BUCKETS", Field: "TopLatency"},
		{Header: "ANOMALIES", Field: "Anomalies"},
	}
}

func traceAnomalyRows(periods []traceAnomalyPeriodResult) []traceAnomalyRow {
	rows := make([]traceAnomalyRow, 0, len(periods))
	for _, p := range periods {
		tops := make([]string, len(p.TopBuckets))
		for i, b := range p.TopBuckets {
			tops[i] = fmt.Sprintf("%s (%.0f%%)", b.Duration, b.Percent)
		}
		ts := p.Time
		if t, err := time.Parse(time.RFC3339, p.Time); err == nil {
			ts = t.UTC().Format("2006-01-02 15:04")
		}
		rows = append(rows, traceAnomalyRow{
			Time:       ts,
			Spans:      p.TotalSpans,
			ErrorRate:  formatPercent(p.ErrorRatePct),
			SlowRate:   formatPercent(p.SlowRatePct),
			TopLatency: strings.Join(tops, ", "),
			Anomalies:  strings.Join(p.Anomalies, "; "),
		})
	}
	return rows
}

func newTracesAnomaliesCmd() *cobra.Command {
	var (
		service, namespace, cluster, env string
		periodStr                        string
		anomalousOnly                    bool
	)
	cmd := &cobra.Command{
		Use:   "anomalies",
		Short: "Find periods with high error rates or slow spans for a service",
		Long: `Find periods with high error rates or slow spans for one service.

The command reads the oodle_trace_metrics span counts of the service, per
duration bucket and status, and splits the time range into periods. For
each period it shows the span count, the error rate, the share of spans of
100ms or more, and the three most common duration buckets.

A period is marked as an anomaly when:
  - its error rate is more than 2 times the average period error rate, and
    more than 5%, or
  - its share of slow spans is more than 1.5 times the average share, and
    more than 20%.

The default period is 5 minutes, or longer so that the range has at most 60
periods. -o json gives the totals, the periods and a short summary.`,
		Example: `  # The last hour of the checkout service
  oodle traces anomalies --service checkout

  # One day in production, only the marked periods
  oodle traces anomalies --service checkout --env production --start -1d --anomalous-only

  # Full analysis as JSON
  oodle traces anomalies --service checkout --start -6h -o json`,
		Args: cobra.NoArgs,
	}
	parseRange := addTraceQLTimeFlags(cmd)
	cmd.Flags().StringVar(&service, "service", "", "Service name (required)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Only read this Kubernetes namespace")
	cmd.Flags().StringVar(&cluster, "cluster", "", "Only read this cluster")
	cmd.Flags().StringVar(&env, "env", "", "Only read this environment")
	cmd.Flags().StringVar(&periodStr, "period", "", "Length of each period, such as 5m or 1h. Defaults to 5m, or longer for long ranges")
	cmd.Flags().BoolVar(&anomalousOnly, "anomalous-only", false, "Show only the periods that are marked as anomalies")
	_ = cmd.MarkFlagRequired("service")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		rangeSec := end - start
		step := traceAnomalyStep(rangeSec)
		period := traceAnomalyPeriod(rangeSec, step)
		if periodStr != "" {
			p, err := parseTraceQLStep(periodStr)
			if err != nil {
				return fmt.Errorf("--period: %w", err)
			}
			if p < step {
				return fmt.Errorf("--period must be at least the query step (%ds) for this range", step)
			}
			period = p
		}

		query := traceAnomalyQuery(service, namespace, cluster, env, step)
		series, err := promQueryRange(cmd, query, float64(start), float64(end), strconv.FormatInt(step, 10)+"s")
		if err != nil {
			return err
		}
		stats, periods := analyzeTraceAnomalies(decodeTraceAnomalySeries(series), period)
		result := traceAnomalyResult{
			Service:       service,
			Namespace:     namespace,
			Cluster:       cluster,
			Env:           env,
			Start:         time.Unix(start, 0).UTC().Format(time.RFC3339),
			End:           time.Unix(end, 0).UTC().Format(time.RFC3339),
			StepSeconds:   step,
			PeriodSeconds: period,
			Overall:       stats,
			Insights:      traceAnomalyInsights(stats, periods),
		}
		if anomalousOnly {
			marked := make([]traceAnomalyPeriodResult, 0)
			for _, p := range periods {
				if len(p.Anomalies) > 0 {
					marked = append(marked, p)
				}
			}
			periods = marked
		}
		result.Periods = periods

		if err := printComputed(cmd, result, traceAnomalyRows(periods), traceAnomalyColumns(stats.SlowThresholdMs)); err != nil {
			return err
		}
		if stats.TotalSpans == 0 && len(series) == 0 {
			hintNoData(cmd, "spans for service "+service, time.Unix(start, 0), time.Unix(end, 0))
			return nil
		}
		if getOutputFormat(cmd) == output.FormatTable {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "\nTotal: %d spans, %.2f%% errors, %.2f%% >= %dms\n",
				stats.TotalSpans, stats.ErrorRatePct, stats.SlowRatePct, stats.SlowThresholdMs)
			for _, s := range result.Insights {
				fmt.Fprintln(w, s)
			}
		}
		return nil
	}
	return cmd
}
