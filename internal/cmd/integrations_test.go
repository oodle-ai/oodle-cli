package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// TestNewIntegrationsCmd_Structure verifies the integrations command tree is
// wired up with the expected subcommands and aliases.
func TestNewIntegrationsCmd_Structure(t *testing.T) {
	cmd := newIntegrationsCmd()
	if cmd.Use != "integrations" {
		t.Errorf("Use = %q, want %q", cmd.Use, "integrations")
	}
	wantAliases := []string{"integ", "integration"}
	gotAliases := append([]string{}, cmd.Aliases...)
	sort.Strings(gotAliases)
	if strings.Join(gotAliases, ",") != strings.Join(wantAliases, ",") {
		t.Errorf("Aliases = %v, want %v", cmd.Aliases, wantAliases)
	}

	wantSubs := []string{"get-setup-spec", "list"}
	got := subcommandNames(cmd)
	if strings.Join(got, ",") != strings.Join(wantSubs, ",") {
		t.Errorf("subcommands = %v, want %v", got, wantSubs)
	}
}

// TestRootCmd_RegistersIntegrationsCommand ensures NewRootCmd wires in the
// integrations command.
func TestRootCmd_RegistersIntegrationsCommand(t *testing.T) {
	root := NewRootCmd()
	if findSubcommand(root, "integrations") == nil {
		t.Error("root: missing subcommand 'integrations'")
	}
}

// TestIntegrationsGetSetupSpec_RequiresArg verifies that get-setup-spec
// requires exactly one positional argument.
func TestIntegrationsGetSetupSpec_RequiresArg(t *testing.T) {
	cmd := newIntegrationsCmd()
	spec := findSubcommand(cmd, "get-setup-spec")
	if spec == nil {
		t.Fatal("get-setup-spec subcommand missing")
	}
	// Validate with zero args.
	if err := spec.Args(spec, []string{}); err == nil {
		t.Error("expected error with zero args")
	}
	// Validate with one arg.
	if err := spec.Args(spec, []string{"kubernetes"}); err != nil {
		t.Errorf("unexpected error with one arg: %v", err)
	}
	// Validate with two args.
	if err := spec.Args(spec, []string{"a", "b"}); err == nil {
		t.Error("expected error with two args")
	}
}

// TestIntegrationColumns verifies the column definitions.
func TestIntegrationColumns(t *testing.T) {
	cols := integrationColumns()
	if len(cols) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(cols))
	}
	wantHeaders := []string{"NAME", "TYPE", "STATUS", "CATEGORIES"}
	for i, col := range cols {
		if col.Header != wantHeaders[i] {
			t.Errorf("column %d: Header = %q, want %q", i, col.Header, wantHeaders[i])
		}
	}
}

// newTestClient creates an api.Client pointing at the given test server.
func newTestClient(t *testing.T, serverURL string) *api.Client {
	t.Helper()
	gen, err := client.NewClientWithResponses(serverURL)
	if err != nil {
		t.Fatalf("creating test client: %v", err)
	}
	return &api.Client{Inner: gen}
}

// TestIntegrationsList_JSON verifies the list subcommand parses a JSON array
// response and outputs it correctly.
func TestIntegrationsList_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/integrations") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `[{"name":"kubernetes","type":"k8s","status":"active","categories":"metrics"},{"name":"aws-cloudwatch","type":"cloudwatch","status":"inactive","categories":"logs"}]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	cmd := newIntegrationsListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := context.Background()
	ctx = withClient(ctx, c)
	ctx = withOutput(ctx, output.FormatJSON)
	ctx = withInstance(ctx, "test-instance")
	cmd.SetContext(ctx)

	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, out)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
	if items[0]["name"] != "kubernetes" {
		t.Errorf("expected first item name 'kubernetes', got %v", items[0]["name"])
	}
}

// TestIntegrationsList_Table verifies table output includes column headers.
func TestIntegrationsList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `[{"name":"kubernetes","type":"k8s","status":"active","categories":"metrics"}]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	cmd := newIntegrationsListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := context.Background()
	ctx = withClient(ctx, c)
	ctx = withOutput(ctx, output.FormatTable)
	ctx = withInstance(ctx, "test-instance")
	cmd.SetContext(ctx)

	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, header := range []string{"NAME", "TYPE", "STATUS", "CATEGORIES"} {
		if !strings.Contains(out, header) {
			t.Errorf("expected %q column header in table output, got: %s", header, out)
		}
	}
	if !strings.Contains(out, "kubernetes") {
		t.Errorf("expected 'kubernetes' in table output, got: %s", out)
	}
}

// TestIntegrationsList_EmptyArray verifies the list subcommand handles an
// empty array response.
func TestIntegrationsList_EmptyArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	cmd := newIntegrationsListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := context.Background()
	ctx = withClient(ctx, c)
	ctx = withOutput(ctx, output.FormatJSON)
	ctx = withInstance(ctx, "test-instance")
	cmd.SetContext(ctx)

	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := strings.TrimSpace(buf.String())
	if out != "[]" {
		t.Errorf("expected '[]', got: %s", out)
	}
}

// TestIntegrationsList_APIError verifies the list subcommand returns an error
// on non-2xx responses.
func TestIntegrationsList_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		fmt.Fprint(w, `{"errors":[{"message":"internal error","code":"INTERNAL"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	cmd := newIntegrationsListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := context.Background()
	ctx = withClient(ctx, c)
	ctx = withOutput(ctx, output.FormatJSON)
	ctx = withInstance(ctx, "test-instance")
	cmd.SetContext(ctx)

	err := cmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Errorf("expected 'internal error' in error message, got: %v", err)
	}
}

// TestIntegrationsGetSetupSpec_JSON verifies the get-setup-spec subcommand
// parses a dynamic JSON object response.
func TestIntegrationsGetSetupSpec_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/integrations/kubernetes/setup-spec") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"name":"kubernetes","requirements":["helm"],"parameters":{"cluster_name":"required"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	cmd := newIntegrationsGetSetupSpecCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := context.Background()
	ctx = withClient(ctx, c)
	ctx = withOutput(ctx, output.FormatJSON)
	ctx = withInstance(ctx, "test-instance")
	cmd.SetContext(ctx)

	if err := cmd.RunE(cmd, []string{"kubernetes"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	var spec map[string]interface{}
	if err := json.Unmarshal([]byte(out), &spec); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, out)
	}
	if spec["name"] != "kubernetes" {
		t.Errorf("expected name 'kubernetes', got %v", spec["name"])
	}
}

// TestIntegrationsGetSetupSpec_NotFound verifies the get-setup-spec subcommand
// returns an error on 404.
func TestIntegrationsGetSetupSpec_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		fmt.Fprint(w, `{"errors":[{"message":"integration type not found","code":"NOT_FOUND"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	cmd := newIntegrationsGetSetupSpecCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := context.Background()
	ctx = withClient(ctx, c)
	ctx = withOutput(ctx, output.FormatJSON)
	ctx = withInstance(ctx, "test-instance")
	cmd.SetContext(ctx)

	err := cmd.RunE(cmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error on 404 response")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error message, got: %v", err)
	}
}

// TestIntegrationsGetSetupSpec_YAML verifies YAML output format works.
func TestIntegrationsGetSetupSpec_YAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"name":"aws-cloudwatch","version":"1.0"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	cmd := newIntegrationsGetSetupSpecCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	ctx := context.Background()
	ctx = withClient(ctx, c)
	ctx = withOutput(ctx, output.FormatYAML)
	ctx = withInstance(ctx, "test-instance")
	cmd.SetContext(ctx)

	if err := cmd.RunE(cmd, []string{"aws-cloudwatch"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "aws-cloudwatch") {
		t.Errorf("expected 'aws-cloudwatch' in YAML output, got: %s", out)
	}
	if !strings.Contains(out, "name:") {
		t.Errorf("expected YAML key 'name:' in output, got: %s", out)
	}
}
