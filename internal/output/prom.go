package output

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// PromSeries represents a single time series from a Prometheus matrix result.
type PromSeries struct {
	// Labels is the metric label set (e.g. {handler="/api/v1/query"}).
	Labels map[string]string
	// Values is the ordered list of (timestamp, value) pairs.
	Values []PromSample
}

// PromSample is a single data point in a time series.
type PromSample struct {
	Timestamp float64
	Value     float64
}

// ParsePromResponse extracts time series data from a parsed Prometheus API
// response body. It handles both "matrix" (range query) and "vector" (instant
// query) result types.
//
// The expected structure is:
//
//	{
//	  "status": "success",
//	  "data": {
//	    "resultType": "matrix" | "vector",
//	    "result": [...]
//	  }
//	}
func ParsePromResponse(parsed any) ([]PromSeries, error) {
	root, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("response is not a JSON object")
	}

	data, ok := root["data"]
	if !ok {
		return nil, fmt.Errorf("response missing 'data' field")
	}
	dataMap, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("'data' is not a JSON object")
	}

	resultType, _ := dataMap["resultType"].(string)
	result, ok := dataMap["result"]
	if !ok {
		return nil, fmt.Errorf("response missing 'data.result' field")
	}
	resultSlice, ok := result.([]any)
	if !ok {
		return nil, fmt.Errorf("'data.result' is not an array")
	}

	switch resultType {
	case "matrix":
		return parseMatrix(resultSlice), nil
	case "vector":
		return parseVector(resultSlice), nil
	case "scalar", "string":
		return nil, fmt.Errorf("%s result type is not supported for this output format; use json or table instead", resultType)
	default:
		return nil, fmt.Errorf("unsupported result type %q", resultType)
	}
}

// parseMatrix parses a Prometheus matrix result (range query).
// Each element: {"metric": {...}, "values": [[timestamp, "value"], ...]}
// Malformed entries are silently skipped.
func parseMatrix(results []any) []PromSeries {
	var series []PromSeries
	for _, item := range results {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		labels := extractLabels(m["metric"])
		values, err := extractValues(m["values"])
		if err != nil {
			continue
		}
		if len(values) == 0 {
			continue
		}
		series = append(series, PromSeries{Labels: labels, Values: values})
	}
	return series
}

// parseVector parses a Prometheus vector result (instant query).
// Each element: {"metric": {...}, "value": [timestamp, "value"]}
// Malformed entries are silently skipped.
func parseVector(results []any) []PromSeries {
	var series []PromSeries
	for _, item := range results {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		labels := extractLabels(m["metric"])
		sample, err := parseSamplePair(m["value"])
		if err != nil {
			continue
		}
		series = append(series, PromSeries{Labels: labels, Values: []PromSample{sample}})
	}
	return series
}

// extractLabels converts the "metric" field to a map[string]string.
func extractLabels(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	labels := make(map[string]string, len(m))
	for k, val := range m {
		if s, ok := val.(string); ok {
			labels[k] = s
		} else {
			labels[k] = fmt.Sprintf("%v", val)
		}
	}
	return labels
}

// parseSamplePair parses a single [timestamp, "value"] pair into a PromSample.
// It returns an error if the pair is malformed, or if the value is NaN or Inf.
func parseSamplePair(v any) (PromSample, error) {
	pair, ok := v.([]any)
	if !ok || len(pair) < 2 {
		return PromSample{}, fmt.Errorf("value is not a [timestamp, value] pair")
	}
	ts, err := toFloat64(pair[0])
	if err != nil {
		return PromSample{}, err
	}
	val, err := toFloat64(pair[1])
	if err != nil {
		return PromSample{}, err
	}
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return PromSample{}, fmt.Errorf("value is NaN or Inf")
	}
	return PromSample{Timestamp: ts, Value: val}, nil
}

// extractValues parses the "values" array from a matrix result.
// Each value is [timestamp_float, "string_value"].
func extractValues(v any) ([]PromSample, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("values is not an array")
	}
	samples := make([]PromSample, 0, len(arr))
	for _, item := range arr {
		sample, err := parseSamplePair(item)
		if err != nil {
			continue
		}
		samples = append(samples, sample)
	}
	return samples, nil
}

// toFloat64 converts a JSON number or string to float64.
func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case string:
		return strconv.ParseFloat(n, 64)
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

// formatLabels formats a label map as a compact string like {handler="/api", method="GET"}.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}

	// Remove __name__ from display since it's usually redundant with the query.
	filtered := make(map[string]string)
	for k, v := range labels {
		if k == "__name__" {
			continue
		}
		filtered[k] = v
	}

	if len(filtered) == 0 {
		if name, ok := labels["__name__"]; ok {
			return name
		}
		return "{}"
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(filtered))
	for k := range filtered {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, filtered[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
