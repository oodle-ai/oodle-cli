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

const projectsJSON = `{"data":[` +
	`{"id":"","name":"default","isDefault":true,"createdAt":"","updatedAt":""},` +
	`{"id":"p-1","name":"checkout-evals","isDefault":false,"createdAt":"","updatedAt":""}]}`

// projectsServer answers the project list and records every other
// request with its query string.
func projectsServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/projects") && r.Method == http.MethodGet {
			_, _ = io.WriteString(w, projectsJSON)
			return
		}
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "/datasets"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "d-1", "name": "golden"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestDatasetCreateInAProjectSendsItsID(t *testing.T) {
	srv, seen := projectsServer(t)
	cmd := newGenAIDatasetsCreateCmd()
	addProjectFlag(cmd)
	_, _, err := runCmdSplit(t, srv.URL, cmd, output.FormatJSON,
		"--name", "golden", "--project", "checkout-evals")
	if err != nil {
		t.Fatal(err)
	}
	got := seen()
	if len(got) != 1 || !strings.Contains(got[0], "projectId=p-1") {
		t.Fatalf("requests = %v, want one create with projectId=p-1", got)
	}
}

func TestDatasetWithoutAProjectSendsNoProjectID(t *testing.T) {
	srv, seen := projectsServer(t)
	cmd := newGenAIDatasetsCreateCmd()
	addProjectFlag(cmd)
	if _, _, err := runCmdSplit(t, srv.URL, cmd, output.FormatJSON,
		"--name", "golden"); err != nil {
		t.Fatal(err)
	}
	got := seen()
	if len(got) != 1 || strings.Contains(got[0], "projectId") {
		t.Fatalf("requests = %v, want one create without projectId", got)
	}
}

func TestAnUnknownProjectSendsNothing(t *testing.T) {
	// Falling back to the default project would put the dataset
	// where the user did not ask for it.
	srv, seen := projectsServer(t)
	cmd := newGenAIDatasetsCreateCmd()
	addProjectFlag(cmd)
	_, _, err := runCmdSplit(t, srv.URL, cmd, output.FormatJSON,
		"--name", "golden", "--project", "nope")
	if err == nil || !strings.Contains(err.Error(), "checkout-evals") {
		t.Fatalf("err = %v, want one that lists the projects", err)
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("requests = %v, want none", got)
	}
}

func TestProjectDeleteFindsTheProjectByName(t *testing.T) {
	srv, seen := projectsServer(t)
	cmd := newGenAIProjectsDeleteCmd()
	cmd.Flags().Bool("force", true, "")
	if _, _, err := runCmdSplit(t, srv.URL, cmd, output.FormatJSON,
		"checkout-evals", "--force"); err != nil {
		t.Fatal(err)
	}
	got := seen()
	if len(got) != 1 || !strings.HasPrefix(got[0], "DELETE ") ||
		!strings.Contains(got[0], "/projects/p-1") {
		t.Fatalf("requests = %v, want DELETE of projects/p-1", got)
	}
}

func TestTheDefaultProjectIsNotDeleted(t *testing.T) {
	srv, seen := projectsServer(t)
	cmd := newGenAIProjectsDeleteCmd()
	cmd.Flags().Bool("force", true, "")
	_, _, err := runCmdSplit(t, srv.URL, cmd, output.FormatJSON,
		"default", "--force")
	if err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("requests = %v, want none", got)
	}
}
