package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
)

// This file holds the helpers of the commands that compute a result on the
// client from PromQL queries.

// promSeries is one series of a PromQL result. Values holds the points of a
// range query, and Value holds the point of an instant query.
type promSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"`
	Value  [2]any            `json:"value"`
}

// promPoint is one decoded point. Points with a value that is not a number
// are dropped when they are decoded.
type promPoint struct {
	ts    float64
	value float64
}

// points decodes the range points of s. A NaN or a value that does not parse
// is dropped, so that it does not add to a sum.
func (s promSeries) points() []promPoint {
	out := make([]promPoint, 0, len(s.Values))
	for _, p := range s.Values {
		ts, ok1 := promNumber(p[0])
		v, ok2 := promNumber(p[1])
		if !ok1 || !ok2 || v != v {
			continue
		}
		out = append(out, promPoint{ts: ts, value: v})
	}
	return out
}

// instantValue returns the value of an instant query series.
func (s promSeries) instantValue() (float64, bool) {
	v, ok := promNumber(s.Value[1])
	if !ok || v != v {
		return 0, false
	}
	return v, true
}

// promNumber reads a number that Prometheus gives as a JSON number
// (timestamps) or as a string (values).
func promNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

// decodePromResult reads the series from a PromQL response body. A body with
// status other than "success" is an error, so that a failed query does not
// look like an empty result.
func decodePromResult(httpResp *http.Response) ([]promSeries, error) {
	body, err := readRawBody(httpResp, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []promSeries `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing query response: %w", err)
	}
	if resp.Status != "success" {
		msg := resp.Error
		if msg == "" {
			msg = "status " + resp.Status
		}
		return nil, fmt.Errorf("query failed: %s", msg)
	}
	return resp.Data.Result, nil
}

// promQueryRange runs a PromQL range query through the metrics query route.
func promQueryRange(cmd *cobra.Command, query string, start, end float64, step string) ([]promSeries, error) {
	c := getClient(cmd)
	httpResp, err := c.Inner.QueryMetricsRange(cmd.Context(), &client.QueryMetricsRangeParams{
		Query:         query,
		Start:         start,
		End:           end,
		Step:          step,
		OODLEINSTANCE: getInstance(cmd),
	})
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	return decodePromResult(httpResp)
}

// promQueryInstant runs a PromQL instant query at time t (epoch seconds).
func promQueryInstant(cmd *cobra.Command, query string, t float64) ([]promSeries, error) {
	c := getClient(cmd)
	httpResp, err := c.Inner.QueryMetricsInstant(cmd.Context(), &client.QueryMetricsInstantParams{
		Query:         query,
		Time:          &t,
		OODLEINSTANCE: getInstance(cmd),
	})
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	return decodePromResult(httpResp)
}

// promQuote returns s as a PromQL string literal. A label value that has a
// quote or a backslash would otherwise end the literal early and change the
// query.
func promQuote(s string) string {
	return strconv.Quote(s)
}

// round rounds f to n decimal places, for values that are shown to people.
func round(f float64, n int) float64 {
	s := strconv.FormatFloat(f, 'f', n, 64)
	r, _ := strconv.ParseFloat(s, 64)
	return r
}

// formatPercent formats a percentage for a table cell.
func formatPercent(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64) + "%"
}

// pct returns part as a percent of total, or 0 when total is 0.
func pct(part, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return part / total * 100
}
