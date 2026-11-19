package cmd

import (
	"testing"
	"time"
)

func TestPublicLogRange(t *testing.T) {
	tests := []struct {
		name, query string
		valid       bool
	}{
		{"exact seven days", `{"range":{"timestamp":{"gte":0,"lte":604800000,"format":"epoch_millis"}}}`, true},
		{"over seven days", `{"range":{"timestamp":{"gte":0,"lte":604800001}}}`, false},
		{"ISO bounds", `{"bool":{"filter":[{"range":{"timestamp":{"gte":"2026-10-01T00:00:00Z","lte":"2026-10-08T00:00:00Z"}}}]}}`, true},
		{"relative bounds", `{"range":{"timestamp":{"gte":"now-7d","lte":"now"}}}`, true},
		{"long relative bounds", `{"range":{"timestamp":{"gte":"now-8d","lte":"now"}}}`, false},
		{"missing range", `{"match_all":{}}`, false},
		{"missing end", `{"range":{"timestamp":{"gte":0}}}`, false},
		{"reversed range", `{"range":{"timestamp":{"gte":1,"lte":0}}}`, false},
		{"optional range", `{"bool":{"should":[{"range":{"timestamp":{"gte":0,"lte":1}}},{"match_all":{}}]}}`, false},
		{"negative range", `{"bool":{"must_not":[{"range":{"timestamp":{"gte":0,"lte":1}}}]}}`, false},
		{"non timestamp", `{"range":{"responseCode":{"gte":0,"lte":1}}}`, false},
		{"split bounds require an explicit bounded filter", `{"bool":{"must":[{"range":{"timestamp":{"gte":0}}},{"range":{"timestamp":{"lte":604800000}}}]}}`, false},
		{"optional range cannot replace bounded range", `{"bool":{"filter":[{"range":{"timestamp":{"gte":0,"lte":1}}}],"should":[{"range":{"timestamp":{"gte":0,"lte":9999999999}}}]}}`, false},
		{"bad timestamp", `{"range":{"timestamp":{"gte":"invalid","lte":"now"}}}`, false},
		{"conflicting format", `{"range":{"timestamp":{"gte":0,"lte":604800,"format":"epoch_second"}}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLogQueryRange([]byte("{\"query\":"+tt.query+"}"), time.Now())
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
}

func TestGlobalAggregationCannotBypassRange(t *testing.T) {
	body := []byte(`{"query":{"range":{"timestamp":{"gte":0,"lte":1}}},"aggs":{"all":{"global":{}}}}`)
	if err := validateLogQueryRange(body, time.Now()); err == nil {
		t.Fatal("global aggregation accepted")
	}
}

func TestLogRangeErrorMessage(t *testing.T) {
	err := validateLogQueryRange([]byte(`{"query":{"range":{"timestamp":{"gte":0,"lte":604800001}}}}`), time.Now())
	if err == nil || err.Error() != "Log query time range exceeds the maximum allowed duration of 7 days. Narrow the time range to 7 days or less." {
		t.Fatalf("unexpected error: %v", err)
	}
}
