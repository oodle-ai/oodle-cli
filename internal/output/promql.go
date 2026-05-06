package output

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// FormatPromQLResult detects the Prometheus result type from a parsed response
// and renders it as a human-readable table (or CSV). If the response does not
// look like a Prometheus result, it returns false and the caller should fall
// back to generic formatting.
//
// Any warnings present in the response are written to warnOut (typically
// stderr) so they remain visible even when the main output is piped.
//
// Supported resultTypes: vector, matrix, scalar, string.
func FormatPromQLResult(w io.Writer, warnOut io.Writer, format Format, parsed any) (bool, error) {
	// The parsed value is a map[string]any from json.Unmarshal into `any`.
	top, ok := parsed.(map[string]any)
	if !ok {
		return false, nil
	}
	dataRaw, ok := top["data"]
	if !ok {
		return false, nil
	}
	data, ok := dataRaw.(map[string]any)
	if !ok {
		return false, nil
	}
	resultType, _ := data["resultType"].(string)
	result := data["result"]

	// Check for native histograms in vector/matrix results. The table
	// formatter cannot represent histogram buckets, so fall back to JSON.
	if (resultType == "vector" || resultType == "matrix") && containsHistograms(result) {
		return false, nil
	}

	var err error

	switch resultType {
	case "vector":
		err = formatVector(w, format, result)
	case "matrix":
		err = formatMatrix(w, format, result)
	case "scalar", "string":
		err = formatTupleResult(w, format, result)
	default:
		return false, nil
	}

	if err == nil {
		printWarnings(warnOut, top)
	}

	return true, err
}

// printWarnings writes any Prometheus API warnings to w. Prometheus may return
// successful responses that carry warnings (e.g. partial results). These are
// written to stderr so they are visible even when stdout is piped.
func printWarnings(w io.Writer, top map[string]any) {
	warnings, ok := top["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		return
	}
	for _, warn := range warnings {
		if s, ok := warn.(string); ok {
			fmt.Fprintf(w, "Warning: %s\n", s)
		}
	}
}

// containsHistograms returns true if the result array contains any items with
// "histogram" or "histograms" keys, indicating native histogram samples that
// the table formatter cannot represent.
func containsHistograms(result any) bool {
	items, ok := result.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, h := m["histogram"]; h {
			return true
		}
		if _, hs := m["histograms"]; hs {
			return true
		}
	}
	return false
}

// formatVector renders an instant-query vector result as a table with one row
// per time series.
//
// Example output:
//
//	METRIC                          TIMESTAMP            VALUE
//	{instance="localhost:9090"}     2024-01-15 10:30:00  1.5
func formatVector(w io.Writer, format Format, result any) error {
	items, ok := result.([]any)
	if !ok {
		return nil
	}
	type row struct {
		Metric    string
		Timestamp string
		Value     string
	}
	rows := make([]row, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metric := formatMetricLabels(m["metric"])
		ts, val := extractSample(m["value"])
		rows = append(rows, row{Metric: metric, Timestamp: ts, Value: val})
	}
	columns := []Column{
		{Header: "METRIC", Field: "Metric"},
		{Header: "TIMESTAMP", Field: "Timestamp"},
		{Header: "VALUE", Field: "Value"},
	}
	return printPromRows(w, format, rows, columns)
}

// formatMatrix renders a range-query matrix result as a table with one row per
// time series, showing sampled values across time.
//
// For CSV output, each sample is emitted as a separate row with METRIC,
// TIMESTAMP, VALUE columns to avoid data loss from truncation.
//
// For table output, values are shown inline and truncated when there are more
// than maxMatrixSamples points.
//
// Example table output:
//
//	METRIC                          VALUES
//	{instance="localhost:9090"}     1.5@Jan15 10:30:00 1.6@Jan15 10:31:00
func formatMatrix(w io.Writer, format Format, result any) error {
	items, ok := result.([]any)
	if !ok {
		return nil
	}

	// CSV: emit one row per sample to avoid data loss from truncation.
	if format == FormatCSV {
		return formatMatrixCSV(w, items)
	}

	type row struct {
		Metric string
		Values string
	}
	rows := make([]row, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metric := formatMetricLabels(m["metric"])
		values := formatMatrixValues(m["values"])
		rows = append(rows, row{Metric: metric, Values: values})
	}
	columns := []Column{
		{Header: "METRIC", Field: "Metric"},
		{Header: "VALUES", Field: "Values"},
	}
	return printPromRows(w, format, rows, columns)
}

// formatMatrixCSV emits one row per sample with METRIC, TIMESTAMP, VALUE
// columns so that CSV output is lossless even for large result sets.
func formatMatrixCSV(w io.Writer, items []any) error {
	type row struct {
		Metric    string
		Timestamp string
		Value     string
	}
	var rows []row
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metric := formatMetricLabels(m["metric"])
		valuesRaw, ok := m["values"].([]any)
		if !ok {
			continue
		}
		for _, v := range valuesRaw {
			ts, val := extractSample(v)
			rows = append(rows, row{Metric: metric, Timestamp: ts, Value: val})
		}
	}
	columns := []Column{
		{Header: "METRIC", Field: "Metric"},
		{Header: "TIMESTAMP", Field: "Timestamp"},
		{Header: "VALUE", Field: "Value"},
	}
	return printCSV(w, rows, columns)
}

// formatTupleResult renders a scalar or string result (a single
// timestamp+value tuple).
func formatTupleResult(w io.Writer, format Format, result any) error {
	ts, val := extractSample(result)
	type row struct {
		Timestamp string
		Value     string
	}
	rows := []row{{Timestamp: ts, Value: val}}
	columns := []Column{
		{Header: "TIMESTAMP", Field: "Timestamp"},
		{Header: "VALUE", Field: "Value"},
	}
	return printPromRows(w, format, rows, columns)
}

// formatMetricLabels renders a metric label set as a compact {key="value",...}
// string, sorted by key for deterministic output.
func formatMetricLabels(metric any) string {
	m, ok := metric.(map[string]any)
	if !ok || len(m) == 0 {
		return "{}"
	}
	// Extract __name__ separately if present.
	name, _ := m["__name__"].(string)

	keys := make([]string, 0, len(m))
	for k := range m {
		if k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	if name != "" {
		b.WriteString(name)
	}
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		v, _ := m[k].(string)
		fmt.Fprintf(&b, "%s=%q", k, v)
	}
	b.WriteByte('}')
	return b.String()
}

// extractSample extracts a timestamp and value from a Prometheus sample tuple
// [timestamp_float, "value_string"].
func extractSample(sample any) (timestamp, value string) {
	arr, ok := sample.([]any)
	if !ok || len(arr) < 2 {
		return "", ""
	}
	timestamp = formatTimestamp(arr[0])
	value = fmt.Sprintf("%v", arr[1])
	return timestamp, value
}

// formatTimestamp converts a Prometheus epoch-seconds float to a human-readable
// timestamp string. JSON numbers are always decoded as float64, so that is the
// only numeric type handled.
func formatTimestamp(v any) string {
	t, ok := v.(float64)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	sec := int64(t)
	nsec := int64((t - float64(sec)) * 1e9)
	ts := time.Unix(sec, nsec).UTC()
	return ts.Format("2006-01-02 15:04:05")
}

// maxMatrixSamples is the maximum number of sample points to display inline
// for a matrix series before truncating with "...".
const maxMatrixSamples = 10

// formatMatrixValues renders the values array from a matrix series as a compact
// "value@timestamp" summary. If there are more than maxMatrixSamples points,
// it shows the first few, an ellipsis, and the last few.
func formatMatrixValues(values any) string {
	arr, ok := values.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}

	type sample struct {
		ts  string
		val string
	}
	fmtSample := func(s sample) string {
		return s.val + "@" + compactTimestamp(s.ts)
	}

	samples := make([]sample, 0, len(arr))
	for _, v := range arr {
		ts, val := extractSample(v)
		samples = append(samples, sample{ts: ts, val: val})
	}

	// If few enough samples, show all.
	if len(samples) <= maxMatrixSamples {
		parts := make([]string, len(samples))
		for i, s := range samples {
			parts[i] = fmtSample(s)
		}
		return strings.Join(parts, " ")
	}

	// Show first 5 ... last 5
	half := maxMatrixSamples / 2
	parts := make([]string, 0, maxMatrixSamples+1)
	for i := 0; i < half; i++ {
		parts = append(parts, fmtSample(samples[i]))
	}
	parts = append(parts, fmt.Sprintf("... (%d total)", len(samples)))
	for i := len(samples) - half; i < len(samples); i++ {
		parts = append(parts, fmtSample(samples[i]))
	}
	return strings.Join(parts, " ")
}

// compactTimestamp returns a shorter timestamp representation suitable for
// inline display in matrix value lists. It uses "Jan02 15:04:05" format to
// keep the date context while saving horizontal space.
func compactTimestamp(ts string) string {
	// Parse the full "2006-01-02 15:04:05" format and re-format compactly.
	t, err := time.Parse("2006-01-02 15:04:05", ts)
	if err != nil {
		return ts
	}
	return t.Format("Jan02 15:04:05")
}

// printPromRows renders a slice of structs using the standard table/CSV
// formatter. This is a thin wrapper to keep the PromQL formatting functions
// concise.
func printPromRows(w io.Writer, format Format, data any, columns []Column) error {
	switch format {
	case FormatCSV:
		return printCSV(w, data, columns)
	default:
		return printTable(w, data, columns)
	}
}
