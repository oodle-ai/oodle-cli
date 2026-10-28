package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/config"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// grafanaExportStub answers every Grafana API call the migration export makes
// with an empty result, so a migrate run in a test needs no live Grafana.
func grafanaExportStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/health":
			fmt.Fprint(w, "{}")
		case strings.HasPrefix(r.URL.Path, "/api/search"),
			r.URL.Path == "/api/folders",
			r.URL.Path == "/api/datasources",
			r.URL.Path == "/api/v1/provisioning/alert-rules",
			r.URL.Path == "/api/v1/provisioning/contact-points",
			r.URL.Path == "/api/v1/provisioning/mute-timings",
			r.URL.Path == "/api/v1/provisioning/templates":
			fmt.Fprint(w, "[]")
		default:
			fmt.Fprint(w, "{}")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// oodleImportStub records the import request, so a test can prove whether the
// CLI sent it and with which overwrite value.
type oodleImportStub struct {
	srv        *httptest.Server
	imports    int
	lastImport map[string]any
}

func newOodleImportStub(t *testing.T) *oodleImportStub {
	t.Helper()
	stub := &oodleImportStub{}
	stub.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/upload-to-object-store"):
			fmt.Fprint(w, `{"uploadedToFilePath":"s3://bucket/oodle_data.tar.gz"}`)
		case strings.HasSuffix(r.URL.Path, "/grafana/migration/export"):
			fmt.Fprint(w, `{"migrationID":"mig-1"}`)
		case strings.HasSuffix(r.URL.Path, "/grafana/migration/mig-1/import"):
			stub.imports++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding import body: %v", err)
			}
			stub.lastImport = body
			fmt.Fprint(w, `{"migratedDashboards":[{"name":"api-latency","state":"CREATED"}]}`)
		default:
			t.Errorf("unexpected Oodle request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(stub.srv.Close)
	return stub
}

func grafanaCmdContext(t *testing.T, oodleURL string) context.Context {
	t.Helper()
	c, err := api.NewClient(&config.Config{
		APIURL:   oodleURL,
		Instance: "acme",
		APIKey:   "test-key",
	}, 0)
	if err != nil {
		t.Fatalf("creating API client: %v", err)
	}
	return withOutput(withClient(context.Background(), c), output.FormatJSON)
}

// runGrafanaMigrateImport runs the migrate command and returns the error it
// produced. force registers the persistent --force flag that the root command
// adds in real use. stdin is fed to os.Stdin for the confirmation prompt.
func runGrafanaMigrateImport(
	t *testing.T,
	ctx context.Context,
	force bool,
	stdin string,
	args ...string,
) error {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdin pipe: %v", err)
	}
	if _, err := w.WriteString(stdin); err != nil {
		t.Fatalf("writing stdin: %v", err)
	}
	w.Close()
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = oldStdin
		r.Close()
	})

	cmd := newGrafanaMigrateCmd()
	cmd.SetContext(ctx)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.Flags().Bool("force", force, "")
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestGrafanaMigrateImport_DeclinedPromptSkipsImport(t *testing.T) {
	grafana := grafanaExportStub(t)
	oodle := newOodleImportStub(t)

	err := runGrafanaMigrateImport(t, grafanaCmdContext(t, oodle.srv.URL), false, "n\n",
		"--grafana-url", grafana.URL, "--grafana-token", "tok", "--import")

	if err == nil {
		t.Fatal("err = nil, want the declined import to abort")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("err = %v, want an aborted import", err)
	}
	if oodle.imports != 0 {
		t.Errorf("import requests = %d, want 0 after the user declined", oodle.imports)
	}
}

func TestGrafanaMigrateImport_ForceSkipsPromptAndOverwrites(t *testing.T) {
	grafana := grafanaExportStub(t)
	oodle := newOodleImportStub(t)

	err := runGrafanaMigrateImport(t, grafanaCmdContext(t, oodle.srv.URL), true, "",
		"--grafana-url", grafana.URL, "--grafana-token", "tok", "--import")

	if err != nil {
		t.Fatalf("err = %v, want a successful import with --force", err)
	}
	if oodle.imports != 1 {
		t.Fatalf("import requests = %d, want 1", oodle.imports)
	}
	if got := oodle.lastImport["overwrite"]; got != true {
		t.Errorf("overwrite = %v, want true", got)
	}
}

func TestGrafanaMigrateImport_AcceptedPromptImports(t *testing.T) {
	grafana := grafanaExportStub(t)
	oodle := newOodleImportStub(t)

	err := runGrafanaMigrateImport(t, grafanaCmdContext(t, oodle.srv.URL), false, "y\n",
		"--grafana-url", grafana.URL, "--grafana-token", "tok", "--import")

	if err != nil {
		t.Fatalf("err = %v, want a successful import after the user agreed", err)
	}
	if oodle.imports != 1 {
		t.Fatalf("import requests = %d, want 1", oodle.imports)
	}
}

func TestGrafanaMigrateImport_NoOverwriteNeedsNoPrompt(t *testing.T) {
	grafana := grafanaExportStub(t)
	oodle := newOodleImportStub(t)

	// stdin is empty, so any prompt reads EOF and aborts. With
	// --overwrite=false the import replaces nothing, so it must not prompt.
	err := runGrafanaMigrateImport(t, grafanaCmdContext(t, oodle.srv.URL), false, "",
		"--grafana-url", grafana.URL, "--grafana-token", "tok", "--import", "--overwrite=false")

	if err != nil {
		t.Fatalf("err = %v, want a successful non-destructive import", err)
	}
	if oodle.imports != 1 {
		t.Fatalf("import requests = %d, want 1", oodle.imports)
	}
	if got := oodle.lastImport["overwrite"]; got != false {
		t.Errorf("overwrite = %v, want false", got)
	}
}
