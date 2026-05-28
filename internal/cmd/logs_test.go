package cmd

import (
	"encoding/json"
	"testing"
)

// mustInjectAndParseBool is a test helper that calls injectTimeRange, splits
// the resulting NDJSON, unmarshals the search body (second line), and extracts
// search["query"]["bool"]. It fails the test on any error.
func mustInjectAndParseBool(t *testing.T, input []byte, startMs, endMs int64) (search, boolQ map[string]any) {
	t.Helper()

	result, err := injectTimeRange(input, startMs, endMs)
	if err != nil {
		t.Fatalf("injectTimeRange: %v", err)
	}

	lines := splitNDJSON(result)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 NDJSON lines, got %d", len(lines))
	}

	if err := json.Unmarshal(lines[1], &search); err != nil {
		t.Fatalf("parsing search body: %v", err)
	}

	query, ok := search["query"].(map[string]any)
	if !ok {
		t.Fatal("expected query in search body")
	}
	boolQ, ok = query["bool"].(map[string]any)
	if !ok {
		t.Fatal("expected bool query")
	}
	return search, boolQ
}

func TestNewLogsCmd_Structure(t *testing.T) {
	cmd := newLogsCmd()
	if cmd.Use != "logs" {
		t.Errorf("expected Use='logs', got %q", cmd.Use)
	}
	if len(cmd.Aliases) == 0 || cmd.Aliases[0] != "log" {
		t.Errorf("expected alias 'log', got %v", cmd.Aliases)
	}

	// Verify expected subcommands are registered.
	subs := map[string]bool{}
	for _, c := range cmd.Commands() {
		subs[c.Use] = true
	}
	for _, want := range []string{"query", "index-patterns"} {
		if !subs[want] {
			t.Errorf("missing subcommand %q; have %v", want, subs)
		}
	}
}

func TestNewLogsQueryCmd_RequiresFileFlag(t *testing.T) {
	cmd := newLogsQueryCmd()
	f := cmd.Flags().Lookup("file")
	if f == nil {
		t.Fatal("expected --file / -f flag")
	}
	// The flag should be required.
	if ann := f.Annotations; ann == nil {
		t.Error("expected --file to be required (missing annotations)")
	}
}

func TestNewLogsQueryCmd_TimeFlags(t *testing.T) {
	cmd := newLogsQueryCmd()
	for _, name := range []string{"start", "end"} {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("expected --%s flag", name)
			continue
		}
		// These should NOT be required (they have defaults applied at runtime).
		if ann := f.Annotations; ann != nil {
			if _, ok := ann["cobra_annotation_bash_completion_one_required_flag"]; ok {
				t.Errorf("--%s should not be required", name)
			}
		}
		// Default values should be empty (defaults applied at runtime).
		if f.DefValue != "" {
			t.Errorf("--%s default = %q, want empty", name, f.DefValue)
		}
	}
}

func TestInjectTimeRange_SimpleQuery(t *testing.T) {
	input := []byte(`{"index": "logs-*"}
{"query": {"match_all": {}}, "size": 10}
`)
	startMs := int64(0)
	endMs := int64(1700003600000)

	search, boolQ := mustInjectAndParseBool(t, input, startMs, endMs)

	// Check must contains original query.
	must, ok := boolQ["must"].([]any)
	if !ok || len(must) != 1 {
		t.Fatalf("expected must with 1 element, got %v", boolQ["must"])
	}
	mustQ, ok := must[0].(map[string]any)
	if !ok {
		t.Fatal("expected must[0] to be a map")
	}
	if _, ok := mustQ["match_all"]; !ok {
		t.Error("expected original match_all in must")
	}

	// Check filter contains range.
	filter, ok := boolQ["filter"].([]any)
	if !ok || len(filter) != 1 {
		t.Fatalf("expected filter with 1 element, got %v", boolQ["filter"])
	}
	rangeClause, ok := filter[0].(map[string]any)
	if !ok {
		t.Fatal("expected filter[0] to be a map")
	}
	rangeField, ok := rangeClause["range"].(map[string]any)
	if !ok {
		t.Fatal("expected range in filter")
	}
	ts, ok := rangeField["timestamp"].(map[string]any)
	if !ok {
		t.Fatal("expected timestamp in range")
	}
	if gte, _ := ts["gte"].(float64); int64(gte) != startMs {
		t.Errorf("gte = %v, want %d", ts["gte"], startMs)
	}
	if lte, _ := ts["lte"].(float64); int64(lte) != endMs {
		t.Errorf("lte = %v, want %d", ts["lte"], endMs)
	}
	if ts["format"] != "epoch_millis" {
		t.Errorf("format = %v, want epoch_millis", ts["format"])
	}

	// Verify size is preserved.
	if size, _ := search["size"].(float64); int(size) != 10 {
		t.Errorf("size = %v, want 10", search["size"])
	}
}

func TestInjectTimeRange_UsesTimestampFieldForOodleLogIndices(t *testing.T) {
	input := []byte(`{"index": "inst_oodle_claude_code_otel_etsvw8_logs"}
{"query": {"bool": {"filter": [{"term": {"cluster": "minikube"}}]}}, "size": 3, "sort": [{"timestamp": "desc"}], "_source": ["timestamp", "namespace", "pod_name", "message", "log"]}
`)
	startMs := int64(1700000000000)
	endMs := int64(1700003600000)

	search, boolQ := mustInjectAndParseBool(t, input, startMs, endMs)

	filter := boolQ["filter"].([]any)
	if len(filter) != 2 {
		t.Fatalf("expected original filter plus injected range, got %d clauses", len(filter))
	}

	rangeClause := filter[1].(map[string]any)
	rangeField := rangeClause["range"].(map[string]any)
	if _, ok := rangeField["@timestamp"]; ok {
		t.Fatal("did not expect @timestamp range field for Oodle log indices")
	}
	ts, ok := rangeField["timestamp"].(map[string]any)
	if !ok {
		t.Fatalf("expected timestamp range field, got %v", rangeField)
	}
	if gte, _ := ts["gte"].(float64); int64(gte) != startMs {
		t.Errorf("gte = %v, want %d", ts["gte"], startMs)
	}
	if lte, _ := ts["lte"].(float64); int64(lte) != endMs {
		t.Errorf("lte = %v, want %d", ts["lte"], endMs)
	}

	sortFields := search["sort"].([]any)
	sortField := sortFields[0].(map[string]any)
	if _, ok := sortField["timestamp"]; !ok {
		t.Fatalf("expected original timestamp sort to be preserved, got %v", sortField)
	}
}

func TestInjectTimeRange_ExistingBoolQuery(t *testing.T) {
	// Query already has a bool with a filter array.
	input := []byte(`{"index": "logs-*"}
{"query": {"bool": {"must": [{"match": {"level": "error"}}], "filter": [{"term": {"service": "api"}}]}}, "size": 5}
`)
	startMs := int64(1700000000000)
	endMs := int64(1700003600000)

	_, boolQ := mustInjectAndParseBool(t, input, startMs, endMs)

	// Must should still contain the original match query.
	must := boolQ["must"].([]any)
	if len(must) != 1 {
		t.Fatalf("expected 1 must clause, got %d", len(must))
	}

	// Filter should now have 2 elements: original term + injected range.
	filter := boolQ["filter"].([]any)
	if len(filter) != 2 {
		t.Fatalf("expected 2 filter clauses, got %d", len(filter))
	}

	// First filter should be the original term.
	firstFilter := filter[0].(map[string]any)
	if _, ok := firstFilter["term"]; !ok {
		t.Error("expected original term filter preserved")
	}

	// Second filter should be the injected range.
	secondFilter := filter[1].(map[string]any)
	if _, ok := secondFilter["range"]; !ok {
		t.Error("expected injected range filter")
	}
}

func TestInjectTimeRange_NoQueryField(t *testing.T) {
	// Search body without a query field — should get a default match_all wrapped.
	input := []byte(`{"index": "logs-*"}
{"size": 20}
`)
	startMs := int64(1700000000000)
	endMs := int64(1700003600000)

	_, boolQ := mustInjectAndParseBool(t, input, startMs, endMs)

	// Must should contain match_all.
	must := boolQ["must"].([]any)
	if len(must) != 1 {
		t.Fatalf("expected 1 must clause, got %d", len(must))
	}
	mustQ := must[0].(map[string]any)
	if _, ok := mustQ["match_all"]; !ok {
		t.Error("expected match_all in must when no query provided")
	}

	// Filter should have the range.
	filter := boolQ["filter"].([]any)
	if len(filter) != 1 {
		t.Fatalf("expected 1 filter clause, got %d", len(filter))
	}
}

func TestInjectTimeRange_TooFewLines(t *testing.T) {
	input := []byte(`{"index": "logs-*"}`)
	_, err := injectTimeRange(input, 0, 0)
	if err == nil {
		t.Fatal("expected error for single-line NDJSON")
	}
}

func TestInjectTimeRange_SingleObjectFilter(t *testing.T) {
	// Query has a bool with a single-object filter (not an array).
	input := []byte(`{"index": "logs-*"}
{"query": {"bool": {"filter": {"term": {"service": "api"}}}}, "size": 5}
`)
	startMs := int64(1700000000000)
	endMs := int64(1700003600000)

	_, boolQ := mustInjectAndParseBool(t, input, startMs, endMs)

	// Filter should now have 2 elements: original single-object term + injected range.
	filter := boolQ["filter"].([]any)
	if len(filter) != 2 {
		t.Fatalf("expected 2 filter clauses, got %d", len(filter))
	}

	// First filter should be the original term.
	firstFilter := filter[0].(map[string]any)
	if _, ok := firstFilter["term"]; !ok {
		t.Error("expected original term filter preserved")
	}

	// Second filter should be the injected range.
	secondFilter := filter[1].(map[string]any)
	if _, ok := secondFilter["range"]; !ok {
		t.Error("expected injected range filter")
	}
}

func TestSplitNDJSON(t *testing.T) {
	input := []byte("{\"a\":1}\n{\"b\":2}\n\n{\"c\":3}\n")
	lines := splitNDJSON(input)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestQueryContainsTimestampRange(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{
			name: "no range filter",
			data: "{\"index\": \"logs-*\"}\n{\"query\": {\"match_all\": {}}, \"size\": 10}\n",
			want: false,
		},
		{
			name: "range on timestamp in bool filter array",
			data: "{\"index\": \"logs-*\"}\n{\"query\": {\"bool\": {\"filter\": [{\"range\": {\"timestamp\": {\"gte\": 1700000000000, \"lte\": 1700003600000}}}]}}, \"size\": 10}\n",
			want: true,
		},
		{
			name: "range on timestamp as single filter object",
			data: "{\"index\": \"logs-*\"}\n{\"query\": {\"bool\": {\"filter\": {\"range\": {\"timestamp\": {\"gte\": 1700000000000}}}}}}\n",
			want: true,
		},
		{
			name: "range on different field",
			data: "{\"index\": \"logs-*\"}\n{\"query\": {\"bool\": {\"filter\": [{\"range\": {\"@timestamp\": {\"gte\": 1700000000000}}}]}}}\n",
			want: false,
		},
		{
			name: "range on timestamp nested in must",
			data: "{\"index\": \"logs-*\"}\n{\"query\": {\"bool\": {\"must\": [{\"range\": {\"timestamp\": {\"gte\": 0}}}]}}}\n",
			want: true,
		},
		{
			name: "single line NDJSON",
			data: "{\"index\": \"logs-*\"}\n",
			want: false,
		},
		{
			name: "invalid JSON in search body",
			data: "{\"index\": \"logs-*\"}\n{bad json}\n",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := queryContainsTimestampRange([]byte(tt.data))
			if got != tt.want {
				t.Errorf("queryContainsTimestampRange() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryContainsTimestampRange_SkipsInjection(t *testing.T) {
	// When the query already has a timestamp range, the search body should
	// be left untouched (no extra range injected by the CLI).
	input := "{\"index\": \"logs-*\"}\n{\"query\": {\"bool\": {\"filter\": [{\"range\": {\"timestamp\": {\"gte\": 1700000000000, \"lte\": 1700003600000}}}]}}, \"size\": 10}\n"

	if !queryContainsTimestampRange([]byte(input)) {
		t.Fatal("expected queryContainsTimestampRange to return true")
	}

	// Verify the data passes through unchanged (the RunE skips injection
	// when the query has its own range and flags weren't provided). We
	// simulate by checking that NOT calling injectTimeRange preserves the
	// original search body.
	lines := splitNDJSON([]byte(input))
	if len(lines) < 2 {
		t.Fatal("expected at least 2 NDJSON lines")
	}

	var search map[string]any
	if err := json.Unmarshal(lines[1], &search); err != nil {
		t.Fatalf("parsing search body: %v", err)
	}

	boolQ := search["query"].(map[string]any)["bool"].(map[string]any)
	filter := boolQ["filter"].([]any)
	if len(filter) != 1 {
		t.Fatalf("expected 1 filter clause (user's original), got %d", len(filter))
	}

	rangeClause := filter[0].(map[string]any)["range"].(map[string]any)
	ts := rangeClause["timestamp"].(map[string]any)
	if gte, _ := ts["gte"].(float64); int64(gte) != 1700000000000 {
		t.Errorf("gte = %v, want 1700000000000", ts["gte"])
	}
	if lte, _ := ts["lte"].(float64); int64(lte) != 1700003600000 {
		t.Errorf("lte = %v, want 1700003600000", ts["lte"])
	}
}
