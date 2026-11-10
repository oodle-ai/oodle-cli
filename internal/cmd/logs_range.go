package cmd

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"errors"
)

const logQueryRangeLimitError = "Log query time range exceeds the maximum allowed duration of 7 days. Narrow the time range to 7 days or less."
const logQueryRangeRequiredError = "Log queries require a bounded timestamp range with a start and end. Use a range of 7 days or less."

// Only mandatory filters constrain both hits and aggregations. A range under
// should, must_not, or post_filter does not bound all of the work.
func validateLogQueryRange(body []byte, now time.Time) error {
	var search map[string]json.RawMessage
	if err := json.Unmarshal(body, &search); err != nil {
		return errors.New("invalid search JSON")
	}
	if containsGlobalAggregation(search["aggs"]) || containsGlobalAggregation(search["aggregations"]) {
		return errors.New("Global aggregations are not supported by the public logs API. Use a query with a timestamp range of 7 days or less.")
	}
	// Require one timestamp range to keep validation unambiguous.
	if countTimestampRanges(search["query"]) != 1 {
		return errors.New(logQueryRangeRequiredError)
	}
	found, err := boundedLogQuery(search["query"], now)
	if err != nil {
		return err
	}
	if !found {
		return errors.New(logQueryRangeRequiredError)
	}
	return nil
}

func boundedLogQuery(raw json.RawMessage, now time.Time) (bool, error) {
	var query map[string]json.RawMessage
	if err := json.Unmarshal(raw, &query); err != nil {
		return false, nil
	}
	if len(query) != 1 {
		return false, nil
	}
	if rawRange, ok := query["range"]; ok {
		var fields map[string]json.RawMessage
		if json.Unmarshal(rawRange, &fields) != nil {
			return false, nil
		}
		rawBounds, ok := fields["timestamp"]
		if !ok || len(fields) != 1 {
			return false, nil
		}
		var bounds map[string]json.RawMessage
		if json.Unmarshal(rawBounds, &bounds) != nil {
			return false, nil
		}
		format := ""
		if rawFormat, ok := bounds["format"]; ok {
			if json.Unmarshal(rawFormat, &format) != nil {
				return false, nil
			}
			if format != "epoch_millis" && format != "strict_date_optional_time" && format != "strict_date_optional_time||epoch_millis" {
				return false, nil
			}
		}
		// Local date math and custom zones can change the effective query bounds.
		if _, ok := bounds["time_zone"]; ok {
			return false, nil
		}
		startRaw := bounds["gte"]
		if startRaw == nil {
			startRaw = bounds["gt"]
		}
		endRaw := bounds["lte"]
		if endRaw == nil {
			endRaw = bounds["lt"]
		}
		start, startOK := logQueryTime(startRaw, now)
		end, endOK := logQueryTime(endRaw, now)
		if !startOK || !endOK || end.Before(start) {
			return false, nil
		}
		if end.Sub(start) > 7*24*time.Hour {
			return false, errors.New(logQueryRangeLimitError)
		}
		return true, nil
	}
	var boolQuery map[string]json.RawMessage
	if json.Unmarshal(query["bool"], &boolQuery) != nil {
		return false, nil
	}
	found := false
	for _, key := range []string{"filter", "must"} {
		rawClause, ok := boolQuery[key]
		if !ok {
			continue
		}
		var clauses []json.RawMessage
		if json.Unmarshal(rawClause, &clauses) != nil {
			clauses = []json.RawMessage{rawClause}
		}
		for _, clause := range clauses {
			bounded, err := boundedLogQuery(clause, now)
			if err != nil {
				return false, err
			}
			found = found || bounded
		}
	}
	return found, nil
}

func logQueryTime(raw json.RawMessage, now time.Time) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var value string
	if raw[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return time.Time{}, false
		}
	} else {
		value = string(raw)
	}
	if millis, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.UnixMilli(millis), true
	}
	if value == "now" {
		return now, true
	}
	if strings.HasPrefix(value, "now-") || strings.HasPrefix(value, "now+") {
		offset := value[3:]
		if strings.HasSuffix(offset, "d") || strings.HasSuffix(offset, "w") {
			unit := time.Hour * 24
			if strings.HasSuffix(offset, "w") {
				unit *= 7
			}
			count, err := strconv.ParseInt(offset[:len(offset)-1], 10, 32)
			if err != nil || count > 10000 || count < -10000 {
				return time.Time{}, false
			}
			return now.Add(time.Duration(count) * unit), true
		}
		duration, err := time.ParseDuration(offset)
		if err != nil {
			return time.Time{}, false
		}
		return now.Add(duration), true
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func containsGlobalAggregation(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var aggs map[string]json.RawMessage
	if json.Unmarshal(raw, &aggs) != nil {
		return true
	}
	for _, rawAgg := range aggs {
		var agg map[string]json.RawMessage
		if json.Unmarshal(rawAgg, &agg) != nil {
			return true
		}
		if _, ok := agg["global"]; ok {
			return true
		}
		if containsGlobalAggregation(agg["aggs"]) || containsGlobalAggregation(agg["aggregations"]) {
			return true
		}
	}
	return false
}

func countTimestampRanges(raw json.RawMessage) int {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return 0
	}
	var count func(any) int
	count = func(value any) int {
		result := 0
		switch node := value.(type) {
		case map[string]any:
			if fields, ok := node["range"].(map[string]any); ok {
				if _, ok := fields["timestamp"]; ok {
					result++
				}
			}
			for _, child := range node {
				result += count(child)
			}
		case []any:
			for _, child := range node {
				result += count(child)
			}
		}
		return result
	}
	return count(value)
}

func validateLogsQueryBody(data []byte, now time.Time) error {
	lines := splitNDJSON(data)
	if len(lines) != 2 {
		return errors.New("request must contain exactly 1 msearch task")
	}
	return validateLogQueryRange(lines[1], now)
}

func parseLogsTimeFlagMs(value string, now time.Time) (int64, error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "now") {
		return now.UnixMilli(), nil
	}
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		if duration, err := parseRelativeDuration(value); err == nil {
			return now.Add(duration).UnixMilli(), nil
		}
	}
	return parseTimeFlagMs(value)
}
