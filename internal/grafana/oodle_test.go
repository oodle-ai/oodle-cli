package grafana

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/config"
)

// testClient builds an api.Client pointed at a stub server, mirroring how the
// CLI constructs one from resolved config.
func testClient(t *testing.T, url, instance, apiKey string) *api.Client {
	t.Helper()
	c, err := api.NewClient(&config.Config{
		APIURL:   url,
		Instance: instance,
		APIKey:   apiKey,
	}, 0)
	if err != nil {
		t.Fatalf("api.NewClient: %v", err)
	}
	return c
}

func TestOodleClient_UploadRegisterImport(t *testing.T) {
	const instance = "acme"
	const apiKey = "secret-key"

	var (
		sawUpload   bool
		sawRegister bool
		sawImport   bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != apiKey {
			t.Errorf("X-API-Key = %q, want %q", got, apiKey)
		}
		if got := r.Header.Get("X-Instance"); got != instance {
			t.Errorf("X-Instance = %q, want %q", got, instance)
		}

		switch {
		case strings.HasSuffix(r.URL.Path, "/integrations/upload-to-object-store"):
			sawUpload = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm: %v", err)
			}
			if got := r.FormValue("uploadFileType"); got != uploadFileTypeGrafanaMigration {
				t.Errorf("uploadFileType = %q, want %q", got, uploadFileTypeGrafanaMigration)
			}
			if got := r.FormValue("fileFormat"); got != "tar.gz" {
				t.Errorf("fileFormat = %q, want tar.gz", got)
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("FormFile: %v", err)
			}
			defer file.Close()
			content, _ := io.ReadAll(file)
			if string(content) != "tar-bytes" {
				t.Errorf("uploaded content = %q, want tar-bytes", content)
			}
			writeJSON(t, w, map[string]string{"uploadedToFilePath": "s3://bucket/oodle_data.tar.gz"})

		case strings.HasSuffix(r.URL.Path, "/grafana/migration/export"):
			sawRegister = true
			var body map[string]string
			decode(t, r, &body)
			if body["exportedDataFilePath"] != "s3://bucket/oodle_data.tar.gz" {
				t.Errorf("register exportedDataFilePath = %q", body["exportedDataFilePath"])
			}
			if body["grafanaUrl"] != "https://grafana.example.com" {
				t.Errorf("register grafanaUrl = %q", body["grafanaUrl"])
			}
			writeJSON(t, w, map[string]string{"migrationID": "mig-123"})

		case strings.HasSuffix(r.URL.Path, "/grafana/migration/mig-123/import"):
			sawImport = true
			var body map[string]any
			decode(t, r, &body)
			if body["overwrite"] != true {
				t.Errorf("import overwrite = %v, want true", body["overwrite"])
			}
			// The tarball is resolved from the migration itself, so the CLI
			// must not send a path here.
			if _, ok := body["exportedDataFilePath"]; ok {
				t.Errorf("import must not send exportedDataFilePath")
			}
			writeJSON(t, w, ImportStatus{
				MigratedDashboards: []MigratedItem{
					{Name: "cpu", State: "IMPORT_SUCCEEDED"},
				},
				MigratedDataSources: []MigratedItem{
					{Name: "bq", State: "CREATED"},
				},
			})

		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewOodleClient(testClient(t, srv.URL, instance, apiKey))
	ctx := context.Background()

	tarPath := filepath.Join(t.TempDir(), "oodle_data.tar.gz")
	if err := os.WriteFile(tarPath, []byte("tar-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	uploadedPath, err := c.UploadTar(ctx, tarPath)
	if err != nil {
		t.Fatalf("UploadTar: %v", err)
	}
	if uploadedPath != "s3://bucket/oodle_data.tar.gz" {
		t.Fatalf("uploadedPath = %q", uploadedPath)
	}

	migrationID, err := c.Register(ctx, uploadedPath, "https://grafana.example.com")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if migrationID != "mig-123" {
		t.Fatalf("migrationID = %q", migrationID)
	}

	status, err := c.Import(ctx, migrationID, true)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(status.MigratedDashboards) != 1 || status.MigratedDashboards[0].State != "IMPORT_SUCCEEDED" {
		t.Errorf("unexpected dashboards: %+v", status.MigratedDashboards)
	}
	if len(status.MigratedDataSources) != 1 || status.MigratedDataSources[0].State != "CREATED" {
		t.Errorf("unexpected datasources: %+v", status.MigratedDataSources)
	}

	if !sawUpload || !sawRegister || !sawImport {
		t.Errorf("not all endpoints hit: upload=%v register=%v import=%v", sawUpload, sawRegister, sawImport)
	}
}

func TestOodleClient_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewOodleClient(testClient(t, srv.URL, "acme", "key"))
	_, err := c.Register(context.Background(), "s3://x", "")
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	// Errors come from the shared api layer, so they are typed and carry the
	// status code rather than embedding it in the message.
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Message, "boom") {
		t.Errorf("message should include the server body: %v", apiErr.Message)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func decode(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
}

// The Grafana endpoints previously hardcoded X-API-Key, so a user who had only
// run `oodle auth login` sent an empty key and got a 401. Auth now comes from
// the shared api layer, which prefers an OAuth Bearer token.
func TestOodleClient_UsesOAuthBearerWhenConfigured(t *testing.T) {
	var gotAuth, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("X-API-Key")
		_ = json.NewEncoder(w).Encode(map[string]string{"migrationID": "mig-1"})
	}))
	defer srv.Close()

	c, err := api.NewClient(&config.Config{
		APIURL:           srv.URL,
		Instance:         "acme",
		OAuthAccessToken: "tok-abc",
	}, 0)
	if err != nil {
		t.Fatalf("api.NewClient: %v", err)
	}

	if _, err := NewOodleClient(c).Register(
		context.Background(), "s3://x", "https://grafana.example.com",
	); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if gotAuth != "Bearer tok-abc" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok-abc")
	}
	if gotAPIKey != "" {
		t.Errorf("X-API-Key should not be sent when OAuth is configured, got %q", gotAPIKey)
	}
}
