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

	"github.com/oodle-ai/oodle-cli/internal/output"
)

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
	wantSlugs := []string{
		"sloth-slo-sli-recordings-postbox-api-availability.slo-sli_error-ratio_rate5m",
		"sloth-slo-sli-recordings-postbox-api-availability.slo-sli_error-ratio_rate30m",
		"sloth-slo-meta-recordings-postbox-api-availability.slo-objective-ratio",
	}
	if len(planned) != len(wantSlugs) {
		t.Fatalf("got %d rules, want %d", len(planned), len(wantSlugs))
	}
	for i, p := range planned {
		if p.Slug != wantSlugs[i] {
			t.Errorf("rule %d slug = %q, want %q", i, p.Slug, wantSlugs[i])
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

func TestRuleSlug(t *testing.T) {
	seen := map[string]int{}
	if got := ruleSlug("G", "a:b", seen); got != "g.a-b" {
		t.Errorf("slug = %q", got)
	}
	if got := ruleSlug("G", "a:b", seen); got != "g.a-b.2" {
		t.Errorf("second rule with the same metric: slug = %q", got)
	}

	long := strings.Repeat("x", 300)
	a := ruleSlug(long, "a", map[string]int{})
	b := ruleSlug(long, "b", map[string]int{})
	if len(a) > maxRecordingRuleSlug || len(b) > maxRecordingRuleSlug {
		t.Errorf("slug longer than %d: %d, %d", maxRecordingRuleSlug, len(a), len(b))
	}
	if a == b {
		t.Error("long slugs of different rules must differ")
	}
	if a != ruleSlug(long, "a", map[string]int{}) {
		t.Error("the slug must be the same on every apply")
	}
}

type fakeRecordingRulesServer struct {
	mu       sync.Mutex
	existing []map[string]any
	puts     map[string]map[string]any
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

func TestApplyPrunesOnlyTheFileGroups(t *testing.T) {
	f := &fakeRecordingRulesServer{
		puts: map[string]map[string]any{},
		existing: []map[string]any{
			{"slug": "sloth-slo-sli-recordings-postbox-api-availability.slo-sli_error-ratio_rate5m", "name": "a", "metric_name": "m", "prometheus_expr": "up"},
			{"slug": "sloth-slo-sli-recordings-postbox-api-availability.slo-sli_error-ratio_rate1h", "name": "b", "metric_name": "m", "prometheus_expr": "up"},
		},
	}
	out := runApply(t, f, "--prune")

	if len(f.puts) != 3 {
		t.Errorf("got %d PUTs, want 3", len(f.puts))
	}
	for slug, req := range f.puts {
		if req["create_if_missing"] != true || req["dry_run"] != false {
			t.Errorf("%s: create_if_missing=%v dry_run=%v", slug, req["create_if_missing"], req["dry_run"])
		}
	}
	if !strings.Contains(f.listURL, "execution_groups=") {
		t.Errorf("prune must list only the file's groups; query = %q", f.listURL)
	}
	want := "sloth-slo-sli-recordings-postbox-api-availability.slo-sli_error-ratio_rate1h"
	if len(f.deletes) != 1 || f.deletes[0] != want {
		t.Errorf("deletes = %v, want [%s]", f.deletes, want)
	}
	if !strings.Contains(out, "Applied 3 recording rules in 2 groups; deleted 1.") {
		t.Errorf("output = %q", out)
	}
}

func TestApplyDryRunChangesNothing(t *testing.T) {
	f := &fakeRecordingRulesServer{
		puts:     map[string]map[string]any{},
		existing: []map[string]any{{"slug": "sloth-slo-sli-recordings-postbox-api-availability.old", "name": "a", "metric_name": "m", "prometheus_expr": "up"}},
	}
	out := runApply(t, f, "--prune", "--dry-run")

	for slug, req := range f.puts {
		if req["dry_run"] != true {
			t.Errorf("%s: dry_run = %v, want true", slug, req["dry_run"])
		}
	}
	if len(f.deletes) != 0 {
		t.Errorf("a dry run deleted %v", f.deletes)
	}
	if !strings.Contains(out, "would delete sloth-slo-sli-recordings-postbox-api-availability.old") {
		t.Errorf("output = %q", out)
	}
}
