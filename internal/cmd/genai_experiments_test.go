package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// jobsServer answers a job create, and keeps the config that
// each create sent.
func jobsServer(t *testing.T) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var (
		mu      sync.Mutex
		configs []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/langfuse/api/public/jobs") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Config map[string]any `json:"config"`
		}
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		configs = append(configs, body.Config)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"job-1","type":"llm-experiment","status":"pending",` +
			`"config":{},"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z"}`))
	}))
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any{}, configs...)
	}
}

// --honor-dependencies reaches the job config. Set to false, it
// turns off a value from the file; not set, it keeps the file's
// value.
func TestExperimentsRunHonorDependencies(t *testing.T) {
	srv, sent := jobsServer(t)
	defer srv.Close()

	base := []string{
		"--dataset-id", "ds", "--connection-id", "conn", "--prompt-name", "p",
	}
	honorFile := writeTempFile(t, "run.yaml", "honorDependencies: true\n")
	cases := []struct {
		name string
		args []string
		want any
	}{
		{"flag", []string{"--honor-dependencies"}, true},
		{"flag false over file", []string{"-f", honorFile, "--honor-dependencies=false"}, false},
		{"file only", []string{"-f", honorFile}, true},
		{"neither", nil, nil},
	}
	for i, tc := range cases {
		_, err := runLibCmd(t, srv.URL, output.FormatJSON,
			newGenAIExperimentsRunCmd(), append(append([]string{}, base...), tc.args...)...)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		configs := sent()
		if len(configs) != i+1 {
			t.Fatalf("%s: %d jobs created, want %d", tc.name, len(configs), i+1)
		}
		got, ok := configs[i][cfgKeyHonorDependencies]
		if tc.want == nil {
			if ok {
				t.Errorf("%s: honorDependencies = %v, want it left out", tc.name, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: honorDependencies = %v, want %v", tc.name, got, tc.want)
		}
	}
}
