package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

const sliGroup = "sloth-slo-sli-recordings-postbox-api-availability"

const slothRules = `groups:
- name: sloth-slo-sli-recordings-postbox-api-availability
  rules:
  - record: slo:sli_error:ratio_rate5m
    expr: |
      sum(rate(errors_total[5m])) / sum(rate(requests_total[5m]))
    labels:
      sloth_id: postbox-api-availability
  - record: slo:sli_error:ratio_rate30m
    expr: sum(rate(errors_total[30m])) / sum(rate(requests_total[30m]))
- name: sloth-slo-meta-recordings-postbox-api-availability
  interval: 2m
  rules:
  - record: slo:objective:ratio
    expr: vector(0.999)
`

func TestPlanRecordingRules(t *testing.T) {
	planned, groups, err := planRecordingRules([]byte(slothRules))
	if err != nil {
		t.Fatal(err)
	}
	wantGroups := []string{
		"sloth-slo-sli-recordings-postbox-api-availability",
		"sloth-slo-meta-recordings-postbox-api-availability",
	}
	if strings.Join(groups, ",") != strings.Join(wantGroups, ",") {
		t.Errorf("groups = %v, want %v", groups, wantGroups)
	}
	wantKeys := []string{
		sliGroup + "/slo:sli_error:ratio_rate5m",
		sliGroup + "/slo:sli_error:ratio_rate30m",
		"sloth-slo-meta-recordings-postbox-api-availability/slo:objective:ratio",
	}
	if len(planned) != len(wantKeys) {
		t.Fatalf("got %d rules, want %d", len(planned), len(wantKeys))
	}
	for i, p := range planned {
		if p.Key != wantKeys[i] {
			t.Errorf("rule %d key = %q, want %q", i, p.Key, wantKeys[i])
		}
		if _, ok := p.Body["id"]; ok {
			t.Errorf("rule %d sends an id; the server makes it", i)
		}
	}

	first := planned[0].Body
	if first["prometheus_expr"] != "sum(rate(errors_total[5m])) / sum(rate(requests_total[5m]))" {
		t.Errorf("expr was not trimmed: %q", first["prometheus_expr"])
	}
	if first["interval_secs"] != 60 {
		t.Errorf("default interval = %v, want 60", first["interval_secs"])
	}
	labels := first["label_policy"].(map[string]any)["add"].(map[string]string)
	if labels["sloth_id"] != "postbox-api-availability" {
		t.Errorf("labels = %v", labels)
	}
	if got, want := first["name"], "slo:sli_error:ratio_rate5m (sloth-slo-sli-recordings-postbox-api-availability)"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if _, ok := planned[1].Body["label_policy"]; ok {
		t.Error("a rule without labels must not send label_policy")
	}
	if planned[2].Body["interval_secs"] != 120 {
		t.Errorf("group interval = %v, want 120", planned[2].Body["interval_secs"])
	}
}

func TestPlanRecordingRulesErrors(t *testing.T) {
	tests := []struct {
		name, file, want string
	}{
		{"no groups", "rules: []", "no rule groups"},
		{"alert", "groups:\n- name: g\n  rules:\n  - alert: High\n    expr: up == 0\n", "alerting rule"},
		{"no record", "groups:\n- name: g\n  rules:\n  - expr: up\n", "has no 'record'"},
		{"duplicate group", "groups:\n- name: g\n  rules: []\n- name: g\n  rules: []\n", "appears twice"},
		{"bad interval", "groups:\n- name: g\n  interval: often\n  rules: []\n", "invalid interval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := planRecordingRules([]byte(tt.file))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestPlanRecordingRulesNumbersARepeatedMetric(t *testing.T) {
	planned, _, err := planRecordingRules([]byte("groups:\n- name: g\n  rules:\n  - record: m\n    expr: '1'\n  - record: m\n    expr: '2'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if planned[0].Key != "g/m" || planned[1].Key != "g/m#2" {
		t.Errorf("keys = %q, %q", planned[0].Key, planned[1].Key)
	}
}

func TestStoredByKeyCountsInRunOrder(t *testing.T) {
	early := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	late := early.Add(time.Minute)
	g := "g"
	id := func(s string) *string { return &s }
	got := storedByKey([]client.RecordingRule{
		{Id: id("b"), ExecutionGroup: &g, MetricName: "m", CreatedAt: &late},
		{Id: id("a"), ExecutionGroup: &g, MetricName: "m", CreatedAt: &early},
		{Id: id("c"), ExecutionGroup: &g, MetricName: "other", CreatedAt: &early},
	})
	for key, want := range map[string]string{"g/m": "a", "g/m#2": "b", "g/other": "c"} {
		if r, ok := got[key]; !ok || *r.Id != want {
			t.Errorf("%s: got %v, want id %s", key, r.Id, want)
		}
	}
}

type fakeRecordingRulesServer struct {
	mu       sync.Mutex
	existing []map[string]any
	puts     map[string]map[string]any
	posts    []map[string]any
	deletes  []string
	listURL  string
}

func (f *fakeRecordingRulesServer) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	const base = "/v1/api/instance/inst/recording-rules"
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == base:
		f.listURL = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"recording_rules": f.existing, "page": map[string]any{}})
	case r.Method == http.MethodPost && r.URL.Path == base:
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		f.posts = append(f.posts, req)
		_ = json.NewEncoder(w).Encode(map[string]any{"recording_rule": req["recording_rule"]})
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, base+"/"):
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		f.puts[strings.TrimPrefix(r.URL.Path, base+"/")] = req
		_ = json.NewEncoder(w).Encode(map[string]any{"recording_rule": req["recording_rule"]})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, base+"/"):
		f.deletes = append(f.deletes, strings.TrimPrefix(r.URL.Path, base+"/"))
		_, _ = io.WriteString(w, "{}")
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
	}
}

func runApply(t *testing.T, f *fakeRecordingRulesServer, args ...string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)

	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(slothRules), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newRecordingRulesApplyCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := withClient(context.Background(), newTestClient(t, srv.URL))
	ctx = withOutput(ctx, output.FormatTable)
	ctx = withInstance(ctx, "inst")
	cmd.SetContext(ctx)
	if err := cmd.ParseFlags(append([]string{"-f", path}, args...)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, cmd.Flags().Args()); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func storedRule(id, group, metric string) map[string]any {
	return map[string]any{
		"id": id, "name": "x", "metric_name": metric, "prometheus_expr": "up",
		"execution_group": group, "created_at": "2026-11-01T00:00:00Z",
	}
}

func TestApplyUpdatesMatchedRulesCreatesNewOnesAndPrunes(t *testing.T) {
	f := &fakeRecordingRulesServer{
		puts: map[string]map[string]any{},
		existing: []map[string]any{
			storedRule("id-5m", sliGroup, "slo:sli_error:ratio_rate5m"),
			storedRule("id-1h", sliGroup, "slo:sli_error:ratio_rate1h"),
		},
	}
	out := runApply(t, f, "--prune")

	if len(f.puts) != 1 || f.puts["id-5m"] == nil {
		t.Errorf("PUTs = %v, want one for id-5m", f.puts)
	}
	if len(f.posts) != 2 {
		t.Errorf("got %d creates, want 2", len(f.posts))
	}
	for _, req := range append([]map[string]any{f.puts["id-5m"]}, f.posts...) {
		if req["dry_run"] != false {
			t.Errorf("dry_run = %v", req["dry_run"])
		}
		if _, ok := req["create_if_missing"]; ok {
			t.Error("apply must not send create_if_missing")
		}
	}
	if !strings.Contains(f.listURL, "execution_groups=") {
		t.Errorf("apply must list only the file's groups; query = %q", f.listURL)
	}
	if len(f.deletes) != 1 || f.deletes[0] != "id-1h" {
		t.Errorf("deletes = %v, want [id-1h]", f.deletes)
	}
	if !strings.Contains(out, "Applied 3 recording rules in 2 groups (created 2, updated 1); deleted 1.") {
		t.Errorf("output = %q", out)
	}
}

func TestApplyWithoutPruneDeletesNothing(t *testing.T) {
	f := &fakeRecordingRulesServer{
		puts:     map[string]map[string]any{},
		existing: []map[string]any{storedRule("id-old", sliGroup, "slo:old")},
	}
	runApply(t, f)

	if len(f.deletes) != 0 {
		t.Errorf("deletes = %v, want none", f.deletes)
	}
}

func TestApplyDryRunChangesNothing(t *testing.T) {
	f := &fakeRecordingRulesServer{
		puts:     map[string]map[string]any{},
		existing: []map[string]any{storedRule("id-old", sliGroup, "slo:old")},
	}
	out := runApply(t, f, "--prune", "--dry-run")

	for _, req := range f.posts {
		if req["dry_run"] != true {
			t.Errorf("dry_run = %v, want true", req["dry_run"])
		}
	}
	if len(f.deletes) != 0 {
		t.Errorf("a dry run deleted %v", f.deletes)
	}
	if !strings.Contains(out, "would delete "+sliGroup+"/slo:old (id-old)") {
		t.Errorf("output = %q", out)
	}
}
