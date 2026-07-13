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
)

const uploadFileTypeGrafanaMigration = "GRAFANA_MIGRATION"

// OodleClient talks to the Oodle api-server Grafana migration endpoints. These
// are not part of the generated OpenAPI client, so we issue raw HTTP requests.
type OodleClient struct {
	Endpoint string
	Instance string
	APIKey   string
	HTTP     *http.Client
}

// NewOodleClient builds a client for the given Oodle endpoint (e.g.
// https://us1.oodle.ai), instance and API key.
func NewOodleClient(endpoint, instance, apiKey string) *OodleClient {
	return &OodleClient{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Instance: instance,
		APIKey:   apiKey,
		HTTP:     &http.Client{Timeout: 5 * time.Minute},
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
func (c *OodleClient) UploadTar(ctx context.Context, tarPath string) (string, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return "", fmt.Errorf("opening tar file: %w", err)
	}
	defer f.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "oodle_data")
	if err != nil {
		return "", fmt.Errorf("creating form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", fmt.Errorf("copying tar into request: %w", err)
	}
	for field, value := range map[string]string{
		"uploadFileType": uploadFileTypeGrafanaMigration,
		"fileName":       "oodle_data",
		"fileFormat":     "tar.gz",
	} {
		if err := writer.WriteField(field, value); err != nil {
			return "", fmt.Errorf("writing %s field: %w", field, err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("closing multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.instanceURL("/upload-to-object-store"),
		&body,
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.setAuthHeaders(req)

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

// Register registers an uploaded export and returns a new migration ID. The
// assets become visible in the Oodle UI (in EXPORT_SUCCEEDED state) ready for
// review; nothing is imported yet.
func (c *OodleClient) Register(
	ctx context.Context,
	uploadedFilePath string,
) (string, error) {
	reqBody, err := json.Marshal(
		map[string]string{"exportedDataFilePath": uploadedFilePath},
	)
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
func (c *OodleClient) Import(
	ctx context.Context,
	migrationID string,
	uploadedFilePath string,
	overwrite bool,
) (*ImportStatus, error) {
	reqBody, err := json.Marshal(map[string]any{
		"exportedDataFilePath": uploadedFilePath,
		"overwrite":            overwrite,
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

func (c *OodleClient) setAuthHeaders(req *http.Request) {
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("X-Instance", c.Instance)
}

func (c *OodleClient) setJSONHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(req)
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
	if resp.StatusCode >= 300 {
		return fmt.Errorf(
			"oodle returned HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
	}
	return nil
}
