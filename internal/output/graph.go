package output

import (
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/NimbleMarkets/ntcharts/linechart/timeserieslinechart"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// seriesColorPalette defines the color palette for multi-series graphs.
// Colors cycle if there are more series than palette entries.
var seriesColorPalette = []lipgloss.Color{
	lipgloss.Color("2"),   // green
	lipgloss.Color("3"),   // yellow
	lipgloss.Color("4"),   // blue
	lipgloss.Color("1"),   // red
	lipgloss.Color("6"),   // cyan
	lipgloss.Color("5"),   // magenta
	lipgloss.Color("214"), // orange
	lipgloss.Color("7"),   // white
}

// PrintGraph renders one or more Prometheus time series as a terminal line
// chart using braille characters (via ntcharts). It writes the chart to w,
// using terminal width/height for sizing when available.
func PrintGraph(w io.Writer, series []PromSeries) error {
	if len(series) == 0 {
		return fmt.Errorf("no time series data to graph")
	}

	// Force lipgloss to use ANSI256 colors when writing to a TTY.
	// Without this, lipgloss may fail to detect color support in some
	// environments (e.g. when TERM is not set or in minimal shells),
	// resulting in colorless output even on capable terminals.
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		lipgloss.SetColorProfile(termenv.ANSI256)
	}

	// Determine terminal dimensions for chart sizing.
	width := 80
	height := 20
	if f, ok := w.(*os.File); ok {
		if tw, th, err := term.GetSize(int(f.Fd())); err == nil {
			if tw > 0 {
				width = tw
			}
			if th > 0 {
				// Use about 50% of terminal height for the chart.
				h := th / 2
				if h > 8 {
					height = h
				}
			}
		}
	}

	// Filter out series with no values. ParsePromResponse never returns
	// zero-length series, but this is a defensive check for direct callers.
	var nonEmpty []PromSeries
	for _, s := range series {
		if len(s.Values) > 0 {
			nonEmpty = append(nonEmpty, s)
		}
	}
	if len(nonEmpty) == 0 {
		return fmt.Errorf("no data points to graph")
	}

	// Compute the global time range using first/last values of each series
	// (values are ordered by timestamp within each series).
	minT, maxT := math.MaxFloat64, -math.MaxFloat64
	for _, s := range nonEmpty {
		if s.Values[0].Timestamp < minT {
			minT = s.Values[0].Timestamp
		}
		if s.Values[len(s.Values)-1].Timestamp > maxT {
			maxT = s.Values[len(s.Values)-1].Timestamp
		}
	}

	// For instant queries or single-point series, expand the range slightly
	// so ntcharts can render a visible axis.
	if minT == maxT {
		minT -= 30
		maxT += 30
	}

	// Determine time format based on range duration.
	timeFmt := "15:04:05"
	if maxT-minT > 24*3600 {
		timeFmt = "Jan 02 15:04"
	}

	// Create the X-axis label formatter.
	xLabelFmt := func(_ int, epoch float64) string {
		return time.Unix(int64(epoch), 0).Local().Format(timeFmt)
	}

	// Create the time series line chart with an explicit time range
	// so that historical data is rendered correctly, rather than defaulting
	// to the current wall clock.
	minTime := time.Unix(int64(minT), 0)
	maxTime := time.Unix(int64(maxT), 0)
	chart := timeserieslinechart.New(width, height,
		timeserieslinechart.WithTimeRange(minTime, maxTime),
		timeserieslinechart.WithXLabelFormatter(xLabelFmt),
		timeserieslinechart.WithXYSteps(4, 2),
	)

	// Push data into named data sets and style each series.
	var legends []string
	for i, s := range nonEmpty {
		name := fmt.Sprintf("series_%d", i)
		color := seriesColorPalette[i%len(seriesColorPalette)]
		style := lipgloss.NewStyle().Foreground(color)
		chart.SetDataSetStyle(name, style)

		for _, v := range s.Values {
			chart.PushDataSet(name, timeserieslinechart.TimePoint{
				Time:  time.Unix(int64(v.Timestamp), 0),
				Value: v.Value,
			})
		}
		legends = append(legends, formatLabels(s.Labels))
	}

	// Render using braille characters for high-resolution output.
	chart.DrawBrailleAll()

	// Get the rendered chart string.
	chartStr := chart.View()
	if _, err := fmt.Fprintln(w, chartStr); err != nil {
		return err
	}

	// Print legend below the chart.
	if len(legends) > 1 || (len(legends) == 1 && legends[0] != "{}") {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for i, legend := range legends {
			color := seriesColorPalette[i%len(seriesColorPalette)]
			style := lipgloss.NewStyle().Foreground(color)
			dot := style.Render("●")
			if _, err := fmt.Fprintf(w, "  %s %s\n", dot, legend); err != nil {
				return err
			}
		}
	}

	return nil
}
