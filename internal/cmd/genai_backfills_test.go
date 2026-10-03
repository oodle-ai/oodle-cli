package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	backfillBaseURL = "/langfuse/api/public/v2/backfills"
	backfillID      = "bf-1"
	// 2026-09-01T00:00:00Z and 2026-09-02T00:00:00Z in microseconds.
	backfillStartUs = 1788220800000000
	backfillEndUs   = 1788307200000000
)

var backfillRunJSON = fmt.Sprintf(
	`{"id":%q,"status":"failed","error":"no spans",`+
		`"createdAt":"2026-09-03T00:00:00Z","updatedAt":"2026-09-03T00:00:00Z",`+
		`"totalBuckets":24,"config":{"name":"Last week","startTime":%d,`+
		`"endTime":%d,"evaluatorIds":["rule-1","rule-2"]}}`,
	backfillID, backfillStartUs, backfillEndUs,
)

// fakeBackfills is an API server for the backfill routes. It
// records each request, so that a test can check what a command
// sent. createStatus and createBody replace the answer of a
// create.
type fakeBackfills struct {
	mu           sync.Mutex
	requests     []recordedRequest
	createStatus int
	createBody   string
	deleteStatus int
}

func (f *fakeBackfills) last(t *testing.T, method string) recordedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if f.requests[i].method == method {
			return f.requests[i]
		}
	}
	t.Fatalf("no %s request was sent", method)
	return recordedRequest{}
}

func (f *fakeBackfills) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeBackfills) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, recordedRequest{r.Method, r.URL.Path, body})
		f.mu.Unlock()

		i := strings.Index(r.URL.Path, backfillBaseURL)
		if i < 0 {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rest := r.URL.Path[i+len(backfillBaseURL):]
		w.Header().Set("Content-Type", "application/json")
		switch {
		case rest == "" && r.Method == http.MethodGet:
			fmt.Fprintf(w, `{"data":[%s],"active":0,"maxConcurrent":2}`, backfillRunJSON)
		case rest == "" && r.Method == http.MethodPost:
			if f.createStatus != 0 {
				w.WriteHeader(f.createStatus)
				fmt.Fprint(w, f.createBody)
				return
			}
			fmt.Fprint(w, strings.TrimSuffix(backfillRunJSON, "}")+
				`,"alsoRuns":[{"id":"rule-3","name":"Helpfulness","readBy":["Refund check"]}]}`)
		case rest == "/"+backfillID && r.Method == http.MethodGet:
			fmt.Fprint(w, backfillRunJSON)
		case rest == "/"+backfillID && r.Method == http.MethodDelete:
			if f.deleteStatus != 0 {
				w.WriteHeader(f.deleteStatus)
				fmt.Fprint(w, `{"message":"that backfill cannot be deleted now","error":"Bad Request"}`)
				return
			}
			fmt.Fprintf(w, `{"id":%q}`, backfillID)
		case rest == "/"+backfillID+"/cancel" && r.Method == http.MethodPost:
			fmt.Fprint(w, strings.Replace(backfillRunJSON, `"failed"`, `"cancelled"`, 1))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// The table flattens the run's config, and shows an error next
// to the status, because a failed run is the one a user looks at.
func TestBackfillsListTable(t *testing.T) {
	f := &fakeBackfills{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatTable, newGenAIBackfillsListCmd())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Last week", backfillID, "failed: no spans",
		"2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "rule-1, rule-2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

// JSON keeps the list envelope, so that a script can read the
// count of active runs and the limit.
func TestBackfillsListJSONKeepsCounts(t *testing.T) {
	f := &fakeBackfills{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatJSON, newGenAIBackfillsListCmd())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got["maxConcurrent"] != float64(2) {
		t.Errorf("maxConcurrent = %v, want 2", got["maxConcurrent"])
	}
}

func TestBackfillsCreateFromFlags(t *testing.T) {
	f := &fakeBackfills{}
	srv := f.server(t)
	defer srv.Close()

	before := time.Now().UnixMicro()
	out, err := runLibCmd(t, srv.URL, output.FormatJSON, newGenAIBackfillsCreateCmd(),
		"--name", "Last week", "--evaluator-id", "rule-1", "--evaluator-id", "rule-2",
		"--start", "-7d", "--sample-rate", "0.25", "--bucket", "2h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := f.last(t, http.MethodPost).body
	if sent["name"] != "Last week" {
		t.Errorf("name = %v", sent["name"])
	}
	ids, _ := sent["evaluatorIds"].([]any)
	if len(ids) != 2 || ids[0] != "rule-1" || ids[1] != "rule-2" {
		t.Errorf("evaluatorIds = %v", sent["evaluatorIds"])
	}
	start, _ := sent["startTime"].(float64)
	end, _ := sent["endTime"].(float64)
	week := float64((7 * 24 * time.Hour).Microseconds())
	// The end defaults to now, and the start is 7 days before it.
	if end < float64(before) || end-start < week-float64(time.Minute.Microseconds()) ||
		end-start > week+float64(time.Minute.Microseconds()) {
		t.Errorf("window = %v..%v, want the last 7 days", start, end)
	}
	if sent["sampleRate"] != 0.25 {
		t.Errorf("sampleRate = %v, want 0.25", sent["sampleRate"])
	}
	if sent["bucketSeconds"] != float64(7200) {
		t.Errorf("bucketSeconds = %v, want 7200", sent["bucketSeconds"])
	}
	if !strings.Contains(out, `"alsoRuns"`) || !strings.Contains(out, "Helpfulness") {
		t.Errorf("output lacks the evaluators the run adds:\n%s", out)
	}
}

// A flag left at its default must not replace the file's value,
// and a flag that is set must.
func TestBackfillsCreateFileAndFlags(t *testing.T) {
	f := &fakeBackfills{}
	srv := f.server(t)
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "backfill.yaml")
	file := fmt.Sprintf("name: From file\nevaluatorIds: [rule-1]\n"+
		"startTime: %d\nendTime: %d\nsampleRate: 0.5\nbucketSeconds: 10800\n"+
		"filters:\n  - {name: \"span::gen_ai.operation.name\", type: 0, value: chat}\n",
		backfillStartUs, backfillEndUs)
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runLibCmd(t, srv.URL, output.FormatJSON, newGenAIBackfillsCreateCmd(),
		"-f", path, "--name", "From flag"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := f.last(t, http.MethodPost).body
	if sent["name"] != "From flag" {
		t.Errorf("name = %v, want the flag to win", sent["name"])
	}
	if sent["startTime"] != float64(backfillStartUs) || sent["endTime"] != float64(backfillEndUs) {
		t.Errorf("window = %v..%v, want the file's", sent["startTime"], sent["endTime"])
	}
	if sent["sampleRate"] != 0.5 || sent["bucketSeconds"] != float64(10800) {
		t.Errorf("sampleRate, bucketSeconds = %v, %v, want the file's",
			sent["sampleRate"], sent["bucketSeconds"])
	}
	filters, _ := sent["filters"].([]any)
	if len(filters) != 1 {
		t.Errorf("filters = %v, want the file's one filter", sent["filters"])
	}
}

// A request that the server would refuse is refused before it
// is sent.
func TestBackfillsCreateNeedsInput(t *testing.T) {
	f := &fakeBackfills{}
	srv := f.server(t)
	defer srv.Close()

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--evaluator-id", "rule-1", "--start", "-1d"}, "needs a name"},
		{[]string{"--name", "x", "--start", "-1d"}, "at least one evaluator"},
		{[]string{"--name", "x", "--evaluator-id", "rule-1"}, "needs a start"},
		{[]string{"--name", "x", "--evaluator-id", "r", "--start", "yesterday"}, "--start"},
	} {
		_, err := runLibCmd(t, srv.URL, output.FormatJSON, newGenAIBackfillsCreateCmd(), tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args %v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
	if n := f.count(); n != 0 {
		t.Errorf("%d requests were sent, want none", n)
	}
}

// A 400 shows the server's message, not the raw JSON body.
func TestBackfillsCreateRefused(t *testing.T) {
	f := &fakeBackfills{
		createStatus: http.StatusBadRequest,
		createBody: `{"message":"endTime is in the future. A backfill reads ` +
			`traces that already arrived","error":"Bad Request"}`,
	}
	srv := f.server(t)
	defer srv.Close()

	_, err := runLibCmd(t, srv.URL, output.FormatJSON, newGenAIBackfillsCreateCmd(),
		"--name", "x", "--evaluator-id", "rule-1", "--start", "-1d", "--end", "+1d")
	if err == nil || !strings.Contains(err.Error(), "endTime is in the future") {
		t.Fatalf("err = %v, want the server's message", err)
	}
	if strings.Contains(err.Error(), `"message"`) {
		t.Errorf("error shows the raw JSON body: %v", err)
	}
}

func TestBackfillsGetCancelDelete(t *testing.T) {
	f := &fakeBackfills{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatTable, newGenAIBackfillsGetCmd(), backfillID)
	if err != nil || !strings.Contains(out, "Last week") {
		t.Errorf("get: err = %v, out:\n%s", err, out)
	}

	out, err = runLibCmd(t, srv.URL, output.FormatTable, newGenAIBackfillsCancelCmd(), backfillID)
	if err != nil || !strings.Contains(out, "cancelled") {
		t.Errorf("cancel: err = %v, out:\n%s", err, out)
	}
	if got := f.last(t, http.MethodPost).path; !strings.HasSuffix(got, "/"+backfillID+"/cancel") {
		t.Errorf("cancel went to %s", got)
	}

	out, err = runLibCmd(t, srv.URL, output.FormatTable, newGenAIBackfillsDeleteCmd(), backfillID)
	if err != nil || !strings.Contains(out, "Deleted backfill run "+backfillID) {
		t.Errorf("delete: err = %v, out:\n%s", err, out)
	}
	if got := f.last(t, http.MethodDelete).path; !strings.HasSuffix(got, "/"+backfillID) {
		t.Errorf("delete went to %s", got)
	}
}

// A refused delete tells the user to cancel the run first.
func TestBackfillsDeleteRunning(t *testing.T) {
	f := &fakeBackfills{deleteStatus: http.StatusBadRequest}
	srv := f.server(t)
	defer srv.Close()

	_, err := runLibCmd(t, srv.URL, output.FormatTable, newGenAIBackfillsDeleteCmd(), backfillID)
	if err == nil {
		t.Fatal("the delete of a running run did not fail")
	}
	for _, want := range []string{
		"cannot be deleted now", "oodle genai backfills cancel " + backfillID,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}
