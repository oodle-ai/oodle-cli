package output

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// SeriesStats holds computed statistics for a single time series.
type SeriesStats struct {
	Labels  string
	Samples int
	Min     float64
	Max     float64
	Avg     float64
	Current float64
	Start   time.Time
	End     time.Time
	Trend   string
}

// PrintStats renders one or more Prometheus time series as a compact
// statistical summary designed for consumption by AI agents and scripts.
// Each series gets one line with labels, sample count, min/max/avg/current
// values, and a trend indicator.
func PrintStats(w io.Writer, series []PromSeries) error {
	if len(series) == 0 {
		_, err := fmt.Fprintln(w, "0 series, 0 total samples")
		return err
	}

	stats := computeStats(series)
	if len(stats) == 0 {
		_, err := fmt.Fprintln(w, "0 series, 0 total samples")
		return err
	}

	// Determine column widths for aligned output.
	maxLabelLen := 0
	for _, s := range stats {
		if len(s.Labels) > maxLabelLen {
			maxLabelLen = len(s.Labels)
		}
	}
	// Cap label width to avoid excessively wide output.
	if maxLabelLen > 80 {
		maxLabelLen = 80
	}

	// Print header.
	header := fmt.Sprintf("%-*s  %6s  %10s  %10s  %10s  %10s  %5s  %s",
		maxLabelLen, "SERIES", "COUNT", "MIN", "MAX", "AVG", "CURRENT", "TREND", "TIME RANGE")
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, strings.Repeat("─", len(header))); err != nil {
		return err
	}

	// Print each series.
	for _, s := range stats {
		label := s.Labels
		if len(label) > maxLabelLen {
			label = label[:maxLabelLen-3] + "..."
		}

		timeRange := formatTimeRange(s.Start, s.End)

		line := fmt.Sprintf("%-*s  %6d  %10s  %10s  %10s  %10s  %5s  %s",
			maxLabelLen, label,
			s.Samples,
			formatFloat(s.Min),
			formatFloat(s.Max),
			formatFloat(s.Avg),
			formatFloat(s.Current),
			s.Trend,
			timeRange,
		)
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	// Print summary footer.
	if _, err := fmt.Fprintf(w, "\n%d series, %d total samples\n",
		len(stats), totalSamples(stats)); err != nil {
		return err
	}

	return nil
}

// computeStats calculates statistics for each series.
func computeStats(series []PromSeries) []SeriesStats {
	var results []SeriesStats
	for _, s := range series {
		if len(s.Values) == 0 {
			continue
		}

		var sum float64
		min := math.MaxFloat64
		max := -math.MaxFloat64

		for _, v := range s.Values {
			sum += v.Value
			if v.Value < min {
				min = v.Value
			}
			if v.Value > max {
				max = v.Value
			}
		}

		n := len(s.Values)
		avg := sum / float64(n)
		current := s.Values[n-1].Value
		start := time.Unix(int64(s.Values[0].Timestamp), 0)
		end := time.Unix(int64(s.Values[n-1].Timestamp), 0)
		trend := computeTrend(s.Values)

		results = append(results, SeriesStats{
			Labels:  formatLabels(s.Labels),
			Samples: n,
			Min:     min,
			Max:     max,
			Avg:     avg,
			Current: current,
			Start:   start,
			End:     end,
			Trend:   trend,
		})
	}

	// Sort by label for deterministic output.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Labels < results[j].Labels
	})

	return results
}

// computeTrend determines the trend direction by comparing the first and last
// thirds of the series values.
func computeTrend(values []PromSample) string {
	n := len(values)
	if n < 2 {
		return "→"
	}

	// Compare average of first third vs last third.
	third := n / 3
	if third == 0 {
		third = 1
	}

	var firstSum, lastSum float64
	firstCount := 0
	lastCount := 0

	for i := 0; i < third && i < n; i++ {
		firstSum += values[i].Value
		firstCount++
	}
	for i := n - third; i < n; i++ {
		lastSum += values[i].Value
		lastCount++
	}

	if firstCount == 0 || lastCount == 0 {
		return "→"
	}

	firstAvg := firstSum / float64(firstCount)
	lastAvg := lastSum / float64(lastCount)

	// Use a 5% threshold to avoid noise.
	if firstAvg == 0 {
		if lastAvg == 0 {
			return "→"
		}
		if lastAvg > 0 {
			return "↑"
		}
		return "↓"
	}

	change := (lastAvg - firstAvg) / math.Abs(firstAvg)
	switch {
	case change > 0.20:
		return "↑↑"
	case change > 0.05:
		return "↑"
	case change < -0.20:
		return "↓↓"
	case change < -0.05:
		return "↓"
	default:
		return "→"
	}
}

// formatFloat formats a float64 for display, using appropriate precision.
func formatFloat(v float64) string {
	abs := math.Abs(v)
	switch {
	case abs == 0:
		return "0"
	case abs >= 1_000_000:
		return fmt.Sprintf("%.2fM", v/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("%.2fK", v/1_000)
	case abs >= 1:
		return fmt.Sprintf("%.2f", v)
	default:
		return fmt.Sprintf("%.4f", v)
	}
}

// formatTimeRange formats a start-end time range as a compact string.
func formatTimeRange(start, end time.Time) string {
	if start.Equal(end) {
		return start.Local().Format("15:04:05")
	}
	duration := end.Sub(start)
	startFmt := start.Local().Format("15:04:05")
	endFmt := end.Local().Format("15:04:05")

	if duration > 24*time.Hour {
		startFmt = start.Local().Format("Jan 02 15:04")
		endFmt = end.Local().Format("Jan 02 15:04")
	}

	return fmt.Sprintf("%s → %s (%s)", startFmt, endFmt, formatDuration(duration))
}

// formatDuration formats a duration as a human-readable string.
func formatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := d / (24 * time.Hour)
		hours := (d % (24 * time.Hour)) / time.Hour
		if hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case d >= time.Hour:
		hours := d / time.Hour
		mins := (d % time.Hour) / time.Minute
		if mins > 0 {
			return fmt.Sprintf("%dh%dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

// totalSamples returns the sum of all sample counts.
func totalSamples(stats []SeriesStats) int {
	total := 0
	for _, s := range stats {
		total += s.Samples
	}
	return total
}
