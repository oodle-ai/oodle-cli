package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// testDocsBody has two hits on one page and one hit on a second page.
// The first hit of the first page has no text, so the snippet comes
// from the second hit.
const testDocsBody = `{"nbHits":3,"hits":[
 {"url":"https://docs.oodle.ai/logs/metrics","url_without_anchor":"https://docs.oodle.ai/logs/metrics","content":null,
  "hierarchy":{"lvl0":"Logs","lvl1":"Log​ Metrics","lvl2":null}},
 {"url":"https://docs.oodle.ai/logs/metrics#rules","url_without_anchor":"https://docs.oodle.ai/logs/metrics","anchor":"rules",
  "content":"Make metrics\n  from   log streams.","hierarchy":{"lvl0":"Logs","lvl1":"Log Metrics","lvl2":"Rules"}},
 {"url":"https://docs.oodle.ai/api/log-metrics","url_without_anchor":"https://docs.oodle.ai/api/log-metrics","content":"API",
  "hierarchy":{"lvl0":"API","lvl1":"Log Metrics"}}]}`

func TestDocsSearch(t *testing.T) {
	srv, got := newGenAITestServer(t, fixedReply(testDocsBody))
	old := docsSearchURL
	docsSearchURL = srv.URL + "/1/indexes/oodle/query"
	t.Cleanup(func() { docsSearchURL = old })

	stdout, stderr, err := runCmdSplit(t, srv.URL, newDocsSearchCmd(), output.FormatJSON, "log metrics")
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.escapedPath != "/1/indexes/oodle/query" {
		t.Errorf("%s %s", got.method, got.escapedPath)
	}
	var req map[string]any
	if err := json.Unmarshal(got.body, &req); err != nil || req["query"] != "log metrics" {
		t.Errorf("request = %s (%v)", got.body, err)
	}
	var out []docsResult
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	want := []docsResult{
		{Title: "Logs > Log Metrics", URL: "https://docs.oodle.ai/logs/metrics", Snippet: "Make metrics from log streams."},
		{Title: "API > Log Metrics", URL: "https://docs.oodle.ai/api/log-metrics", Snippet: "API"},
	}
	if len(out) != 2 || out[0] != want[0] || out[1] != want[1] {
		t.Errorf("results = %+v", out)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}

	stdout, _, err = runCmdSplit(t, srv.URL, newDocsSearchCmd(), output.FormatTable, "log metrics", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "TITLE") || !strings.Contains(stdout, "docs.oodle.ai/logs/metrics") || strings.Contains(stdout, "api/log-metrics") {
		t.Errorf("table:\n%s", stdout)
	}
}

func TestDocsSearch_Errors(t *testing.T) {
	srv, _ := newGenAITestServer(t, func(string) (int, string) { return 403, `{"message":"Invalid API key"}` })
	old := docsSearchURL
	docsSearchURL = srv.URL
	t.Cleanup(func() { docsSearchURL = old })

	if _, _, err := runCmdSplit(t, srv.URL, newDocsSearchCmd(), output.FormatJSON, "x"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v", err)
	}
	if _, _, err := runCmdSplit(t, srv.URL, newDocsSearchCmd(), output.FormatJSON, "x", "--limit", "21"); err == nil {
		t.Error("expected an error for --limit 21")
	}

	srv, _ = newGenAITestServer(t, fixedReply(`{"hits":[]}`))
	docsSearchURL = srv.URL
	stdout, stderr, err := runCmdSplit(t, srv.URL, newDocsSearchCmd(), output.FormatJSON, "zzz")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != "[]" || !strings.Contains(stderr, "No docs pages match") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestDocsSkipsConfig(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"docs", "search"})
	if err != nil {
		t.Fatal(err)
	}
	if !shouldSkipConfig(cmd) {
		t.Error("docs search must run without Oodle credentials")
	}
}
