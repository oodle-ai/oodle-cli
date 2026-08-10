package grafana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/api"
)

const uploadFileTypeGrafanaMigration = "GRAFANA_MIGRATION"

// uploadTimeout is generous because the tarball can be hundreds of megabytes
// on a large Grafana, far beyond a normal API call.
const uploadTimeout = 5 * time.Minute

// OodleClient talks to the Oodle api-server Grafana migration endpoints. These
// are not part of the generated OpenAPI client, so we issue raw HTTP requests -
// but authentication, retries and error formatting come from internal/api, so
// these calls behave like every other command.
type OodleClient struct {
	Endpoint string
	Instance string
	HTTP     *http.Client
}

// NewOodleClient builds a client from the shared API client, so endpoint,
// instance, auth and retries all match the rest of the CLI. Auth follows the
// shared rules: OAuth Bearer when logged in via `oodle auth login`, otherwise
// the API key from `oodle configure`.
func NewOodleClient(c *api.Client) *OodleClient {
	return &OodleClient{
		Endpoint: strings.TrimRight(c.Config.APIURL, "/"),
		Instance: c.Config.Instance,
		HTTP:     c.NewAuthedHTTPClient(uploadTimeout),
	}
}

// MigratedItem is one row of a migration result (datasource, dashboard, folder
// or alert rule). Only the fields the CLI reports on are modelled.
type MigratedItem struct {
	Name          string `json:"name"`
	UID           string `json:"uid"`
	Type          string `json:"type"`
	State         string `json:"state"`
	FailureReason string `json:"failureReason"`
	Message       string `json:"message"`
}

// ImportStatus mirrors the api-server GrafanaMigrationImportStatus response.
type ImportStatus struct {
	MigratedDataSources []MigratedItem `json:"migratedDataSources"`
	MigratedDashboards  []MigratedItem `json:"migratedDashboards"`
	MigratedFolders     []MigratedItem `json:"migratedFolders"`
	MigratedAlertRules  []MigratedItem `json:"migratedAlertRules"`
}

func (c *OodleClient) instanceURL(suffix string) string {
	return fmt.Sprintf(
		"%s/v1/api/instance/%s/integrations%s",
		c.Endpoint,
		c.Instance,
		suffix,
	)
}

// UploadTar uploads the exported tarball to Oodle object storage and returns
// the stored object path used to reference it in later calls.
//
// The request body is streamed from disk through a pipe rather than assembled
// in memory: a large Grafana can produce a tarball of hundreds of megabytes,
// and buffering it would double that in RSS for no reason.
func (c *OodleClient) UploadTar(ctx context.Context, tarPath string) (string, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return "", fmt.Errorf("opening tar file: %w", err)
	}
	defer f.Close()

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		// CloseWithError(nil) is equivalent to Close, so a nil err here ends
		// the body cleanly.
		pw.CloseWithError(func() error {
			for field, value := range map[string]string{
				"uploadFileType": uploadFileTypeGrafanaMigration,
				"fileName":       "oodle_data",
				"fileFormat":     "tar.gz",
			} {
				if err := writer.WriteField(field, value); err != nil {
					return fmt.Errorf("writing %s field: %w", field, err)
				}
			}

			part, err := writer.CreateFormFile("file", "oodle_data")
			if err != nil {
				return fmt.Errorf("creating form file: %w", err)
			}
			if _, err := io.Copy(part, f); err != nil {
				return fmt.Errorf("copying tar into request: %w", err)
			}

			return writer.Close()
		}())
	}()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.instanceURL("/upload-to-object-store"),
		pr,
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Instance", c.Instance)

	var out struct {
		UploadedToFilePath string `json:"uploadedToFilePath"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.UploadedToFilePath == "" {
		return "", fmt.Errorf("upload response missing uploadedToFilePath")
	}
	return out.UploadedToFilePath, nil
}

// Register records an uploaded export as a new draft migration and returns its
// ID. The assets become reviewable in the Oodle UI; nothing is imported yet.
//
// grafanaURL is stored alongside the migration so the UI can show where the
// export came from. The token is never sent.
func (c *OodleClient) Register(
	ctx context.Context,
	uploadedFilePath string,
	grafanaURL string,
) (string, error) {
	reqBody, err := json.Marshal(map[string]string{
		"exportedDataFilePath": uploadedFilePath,
		"grafanaUrl":           grafanaURL,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.instanceURL("/grafana/migration/export"),
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return "", err
	}
	c.setJSONHeaders(req)

	var out struct {
		MigrationID string `json:"migrationID"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.MigrationID == "" {
		return "", fmt.Errorf("export response missing migrationID")
	}
	return out.MigrationID, nil
}

// Import triggers a server-side import of a previously registered migration.
//
// The tarball to import is resolved from the migration itself, so only the
// import options travel here. A migration that has already been imported is
// frozen and the server rejects this with HTTP 409.
func (c *OodleClient) Import(
	ctx context.Context,
	migrationID string,
	overwrite bool,
) (*ImportStatus, error) {
	reqBody, err := json.Marshal(map[string]any{
		"overwrite": overwrite,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.instanceURL(fmt.Sprintf("/grafana/migration/%s/import", migrationID)),
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return nil, err
	}
	c.setJSONHeaders(req)

	var out ImportStatus
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *OodleClient) setJSONHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Instance", c.Instance)
}

func (c *OodleClient) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", req.URL.Path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	// Shared error formatting, so an Oodle API error reads the same here as it
	// does from any other command.
	if err := api.CheckResponse(resp, body); err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
	}
	return nil
}
