package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/config"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newConfiguredTestClient returns a client with a config, because the
// asset and S3 commands build some URLs from the configured API URL.
func newConfiguredTestClient(t *testing.T, serverURL string) *api.Client {
	t.Helper()
	gen, err := client.NewClientWithResponses(serverURL)
	if err != nil {
		t.Fatalf("creating test client: %v", err)
	}
	return &api.Client{Inner: gen, Config: &config.Config{APIURL: serverURL, APIKey: "test-key"}}
}

func configuredCtx(t *testing.T, serverURL string, f output.Format) context.Context {
	t.Helper()
	ctx := withInstance(context.Background(), "test-instance")
	ctx = withOutput(ctx, f)
	return withClient(ctx, newConfiguredTestClient(t, serverURL))
}

// assetsServer fakes the asset routes. The monitors that exist before
// the import are given, and the POST adds the recommended ones that are
// not there, as the server does on a first import.
type assetsServer struct {
	mu          sync.Mutex
	folders     []string
	folderUID   string
	recommended []string
	existing    []string
	// installedFlag makes the POST return only the existing monitors,
	// as the server does after the first import.
	installedFlag bool
	requests      []string
}

func (s *assetsServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
		base := "/v1/api/instance/test-instance"
		w.Header().Set("Content-Type", "application/json")
		monitorsJSON := func(names []string) []map[string]interface{} {
			out := []map[string]interface{}{}
			for _, n := range names {
				out = append(out, map[string]interface{}{"name": n, "labels": map[string]string{"oodle_integration": "Temporal"}})
			}
			return out
		}
		switch {
		case r.URL.Path == base+"/grafana/folders" && r.Method == http.MethodGet:
			out := []map[string]interface{}{}
			for i, uid := range s.folders {
				out = append(out, map[string]interface{}{"id": i + 1, "uid": uid, "title": uid})
			}
			_ = json.NewEncoder(w).Encode(out)
		case r.URL.Path == base+"/integrations/Temporal/manage-dashboards" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]string{"dashboardFolderId": s.folderUID})
		case r.URL.Path == base+"/integrations/Temporal/manage-monitors" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(monitorsJSON(s.recommended))
		case r.URL.Path == base+"/monitors" && r.Method == http.MethodGet:
			all := monitorsJSON(s.existing)
			all = append(all, map[string]interface{}{"name": "other", "labels": map[string]string{}})
			_ = json.NewEncoder(w).Encode(all)
		case r.URL.Path == base+"/integrations/Temporal/manage-monitors" && r.Method == http.MethodPost:
			if !s.installedFlag {
				have := map[string]bool{}
				for _, n := range s.existing {
					have[n] = true
				}
				for _, n := range s.recommended {
					if !have[n] {
						s.existing = append(s.existing, n)
					}
				}
			}
			_ = json.NewEncoder(w).Encode(monitorsJSON(s.existing))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"message":"not found"}]}`))
		}
	})
}

func runImportAssets(t *testing.T, srv *httptest.Server, f output.Format, args ...string) (string, error) {
	t.Helper()
	cmd := newIntegrationsImportAssetsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(configuredCtx(t, srv.URL, f))
	return out.String(), err
}

func TestResolveIntegrationAssetName(t *testing.T) {
	cases := map[string]string{
		"kubernetes":    "Kubernetes",
		"TEMPORAL":      "Temporal",
		"cloudwatch":    "AWS",
		"azure_metrics": "Azure",
		"crdb_cloud":    "CockroachDB",
		"Unknown-Thing": "Unknown-Thing",
	}
	for in, want := range cases {
		if got := resolveIntegrationAssetName(in); got != want {
			t.Errorf("resolveIntegrationAssetName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImportAssets_FirstRunCreatesBoth(t *testing.T) {
	s := &assetsServer{folderUID: "temporal-oodle", recommended: []string{"b", "a"}}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	out, err := runImportAssets(t, srv, output.FormatJSON, "temporal")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	var got importAssetsResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parsing output: %v\n%s", err, out)
	}
	if got.Integration != "Temporal" {
		t.Errorf("integration = %q", got.Integration)
	}
	if got.Dashboards == nil || got.Dashboards.Status != "created" || got.Dashboards.FolderUID != "temporal-oodle" {
		t.Errorf("dashboards = %+v", got.Dashboards)
	}
	if got.Monitors == nil || strings.Join(got.Monitors.Created, ",") != "a,b" || len(got.Monitors.Existed) != 0 {
		t.Errorf("monitors = %+v", got.Monitors)
	}
}

func TestImportAssets_SecondRunReportsExisting(t *testing.T) {
	s := &assetsServer{
		folders:       []string{"temporal-oodle"},
		folderUID:     "temporal-oodle",
		recommended:   []string{"a", "b", "c"},
		existing:      []string{"a", "b"},
		installedFlag: true,
	}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	out, err := runImportAssets(t, srv, output.FormatTable, "Temporal")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	for _, want := range []string{
		"the folder temporal-oodle existed",
		"0 created, 2 existed already",
		"not created: c",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestImportAssets_DashboardPrefixesAndOnlyDashboards(t *testing.T) {
	s := &assetsServer{folderUID: "temporal-oodle"}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	out, err := runImportAssets(t, srv, output.FormatTable, "Temporal", "--dashboards",
		"--dashboard-prefix", "ec2.json", "--dashboard-prefix", "rds")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if strings.Contains(out, "Monitors") {
		t.Errorf("output has monitors although only --dashboards was given:\n%s", out)
	}
	var sawPost bool
	for _, r := range s.requests {
		if strings.HasPrefix(r, "POST ") && strings.Contains(r, "manage-dashboards?dashboardPrefixesFilter=ec2.json&dashboardPrefixesFilter=rds") {
			sawPost = true
		}
		if strings.Contains(r, "manage-monitors") {
			t.Errorf("unexpected monitors request %q", r)
		}
	}
	if !sawPost {
		t.Errorf("no POST with the prefixes; requests: %v", s.requests)
	}
}

func TestImportAssets_NoneAvailable(t *testing.T) {
	s := &assetsServer{}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	// Without flags, a kind that the integration does not have is a note.
	out, err := runImportAssets(t, srv, output.FormatTable, "Temporal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "no dashboards") || !strings.Contains(out, "no recommended monitors") {
		t.Errorf("output:\n%s", out)
	}
	// When the kind is asked for, it is an error.
	if _, err := runImportAssets(t, srv, output.FormatTable, "Temporal", "--monitors"); err == nil ||
		!strings.Contains(err.Error(), "no recommended monitors") {
		t.Errorf("err = %v", err)
	}
}

func TestImportAssets_RequiresArg(t *testing.T) {
	cmd := newIntegrationsImportAssetsCmd()
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error without the integration argument")
	}
}
