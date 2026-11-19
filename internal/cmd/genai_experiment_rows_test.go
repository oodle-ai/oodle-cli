package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// The rows command sends the compare flags as query parameters,
// and the table shows the row's scores beside the base's.
func TestExperimentsRowsCompare(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/experiments/exp-1/rows") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"row-1","traceId":"t1","datasetItemId":"",` +
			`"datasetRunId":"exp-1","createdAt":"","updatedAt":"",` +
			`"scores":[{"name":"accuracy","value":0.5}],` +
			`"comparison":{"matched":true,"baseScores":{"accuracy":0.75}}}],` +
			`"movedTotal":1}`))
	}))
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatTable, newGenAIExperimentsRowsCmd(),
		"exp-1", "--compare-to", "exp-0", "--moved", "accuracy:worse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"compareTo=exp-0", "moved=accuracy%3Aworse"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
	for _, want := range []string{"row-1", "accuracy=0.5", "accuracy=0.75"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

// A wrong --moved is refused before a request is sent.
func TestExperimentsRowsMovedChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	}))
	defer srv.Close()
	cases := [][]string{
		{"exp-1", "--moved", "accuracy:worse"},
		{"exp-1", "--compare-to", "b", "--moved", "accuracy"},
		{"exp-1", "--compare-to", "b", "--moved", "accuracy:down"},
	}
	for _, args := range cases {
		if _, err := runLibCmd(t, srv.URL, output.FormatTable,
			newGenAIExperimentsRowsCmd(), args...); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}
