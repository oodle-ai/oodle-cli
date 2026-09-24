package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/client"
)

const startersBody = `{"data":[{"id":"pii-leak","name":"PII and secret leak",` +
	`"category":"Safety and tone","description":"Output leaks PII.",` +
	`"sourceCode":"CHECKS = [\"email\"]\n\n\ndef evaluate(ctx):\n    return None\n"}]}`

func startersServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/langfuse/api/public/code-eval-starters") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, startersBody)
	}))
}

func TestTemplatesStartersLists(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	out := new(bytes.Buffer)
	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "pii-leak") {
		t.Errorf("list output lacks the starter: %s", out.String())
	}
}

// The printed file must be one `templates create -f` reads back
// into a code template with the source unchanged, line for line.
func TestTemplatesStarterPrintsACreateFile(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	out := new(bytes.Buffer)
	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(out)
	cmd.SetArgs([]string{"pii-leak"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "sourceCode: |") {
		t.Errorf("source is not a literal block:\n%s", out.String())
	}

	path := filepath.Join(t.TempDir(), "pii.yaml")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var body client.CreateGenaiEvaluatorJSONRequestBody
	if err := readInputFile(path, &body); err != nil {
		t.Fatalf("create -f cannot read it: %v", err)
	}
	if body.Type != "code" || body.Name != "PII and secret leak" {
		t.Errorf("name/type = %q/%q", body.Name, body.Type)
	}
	want := "CHECKS = [\"email\"]\n\n\ndef evaluate(ctx):\n    return None\n"
	if body.SourceCode == nil || *body.SourceCode != want {
		t.Errorf("source changed in the round trip: %v", body.SourceCode)
	}
}

func TestTemplatesStarterUnknownID(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"nope"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `no starter "nope"`) {
		t.Fatalf("err = %v, want a no-starter error", err)
	}
}
