package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// TestNewAzureCmd_Structure pins the subcommand layout so accidental
// renames/removes are caught here, not at the user's terminal.
func TestNewAzureCmd_Structure(t *testing.T) {
	cmd := newAzureCmd()
	if cmd.Use != "azure" {
		t.Errorf("Use = %q, want %q", cmd.Use, "azure")
	}
	want := []string{"add", "delete", "list", "update"}
	got := subcommandNames(cmd)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("subcommands = %v, want %v", got, want)
	}
}

// TestAzureCmd_RegisteredUnderIntegrations ensures we wired newAzureCmd into
// the integrations group rather than leaving it orphaned.
func TestAzureCmd_RegisteredUnderIntegrations(t *testing.T) {
	cmd := newIntegrationsCmd()
	if findSubcommand(cmd, "azure") == nil {
		t.Error("integrations: missing subcommand 'azure'")
	}
}

func TestAzureAddCmd_FlagsRequired(t *testing.T) {
	// No flags supplied → the missing-flag check fires before any HTTP call,
	// so we can test it with a nil client by jumping straight to RunE.
	cmd := newAzureAddCmd()
	cmd.SetContext(baseAzureCtx())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatal("expected error when required flags are missing")
	}
	for _, f := range []string{"--subscription-name", "--tenant-id", "--subscription-id", "--client-id"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q does not mention %s", err.Error(), f)
		}
	}
}

func TestAzureAddCmd_HappyPath(t *testing.T) {
	var captured client.CreateIntegrationRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"int-azure-1","type":"AZURE_METRICS","status":"NOT_CONNECTED"}`)
	}))
	defer srv.Close()

	cmd := newAzureAddCmd()
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--subscription-name", "OodleAI POC",
		"--tenant-id", "tenant-abc",
		"--subscription-id", "sub-xyz",
		"--client-id", "client-123",
		"--client-secret", "s3cr3t",
		"--services", "compute_virtualmachines,storage_storageaccounts",
		"--tag", "env=prod",
		"--resource-groups", "rg-one,rg-two",
		"--resource-name-regex", "^prod-",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.Type != azureIntegrationType {
		t.Errorf("Type = %q, want %q", captured.Type, azureIntegrationType)
	}
	// Status must be omitted so the server applies the type's initial status.
	if captured.Status != nil {
		t.Errorf("Status = %v, want nil (server default)", *captured.Status)
	}
	az := decodeAzureCreate(t, captured)
	if az.SubscriptionName == nil || *az.SubscriptionName != "OodleAI POC" {
		t.Errorf("SubscriptionName = %v", az.SubscriptionName)
	}
	if az.TenantId == nil || *az.TenantId != "tenant-abc" {
		t.Errorf("TenantId = %v", az.TenantId)
	}
	if az.SubscriptionId == nil || *az.SubscriptionId != "sub-xyz" {
		t.Errorf("SubscriptionId = %v", az.SubscriptionId)
	}
	if az.ClientId == nil || *az.ClientId != "client-123" {
		t.Errorf("ClientId = %v", az.ClientId)
	}
	if az.ClientSecret == nil || *az.ClientSecret != "s3cr3t" {
		t.Errorf("ClientSecret = %v", az.ClientSecret)
	}
	if az.ResourceNameRegex == nil || *az.ResourceNameRegex != "^prod-" {
		t.Errorf("ResourceNameRegex = %v", az.ResourceNameRegex)
	}
	if az.ResourceGroups == nil || len(*az.ResourceGroups) != 2 || (*az.ResourceGroups)[0] != "rg-one" {
		t.Errorf("ResourceGroups = %v", az.ResourceGroups)
	}
	if az.ServiceFilters == nil || len(*az.ServiceFilters) != 1 {
		t.Fatalf("ServiceFilters = %v, want exactly one group", az.ServiceFilters)
	}
	first := (*az.ServiceFilters)[0]
	if first.ServiceIds == nil || len(*first.ServiceIds) != 2 || (*first.ServiceIds)[0] != "compute_virtualmachines" {
		t.Errorf("ServiceIds = %v", first.ServiceIds)
	}
	if first.Tags == nil || (*first.Tags)["env"] != "prod" {
		t.Errorf("Tags = %v", first.Tags)
	}
}

// TestAzureAddCmd_SecretFromEnv covers the env-var fallback, which is the
// path CI uses to keep the secret out of argv.
func TestAzureAddCmd_SecretFromEnv(t *testing.T) {
	t.Setenv(azureClientSecretEnv, "from-env")

	var captured client.CreateIntegrationRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"int-azure-1","type":"AZURE_METRICS"}`)
	}))
	defer srv.Close()

	cmd := newAzureAddCmd()
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--subscription-name", "n", "--tenant-id", "t",
		"--subscription-id", "s", "--client-id", "c",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	az := decodeAzureCreate(t, captured)
	if az.ClientSecret == nil || *az.ClientSecret != "from-env" {
		t.Errorf("ClientSecret = %v, want from-env", az.ClientSecret)
	}
}

// TestAzureAddCmd_FlagBeatsEnv pins the precedence order.
func TestAzureAddCmd_FlagBeatsEnv(t *testing.T) {
	t.Setenv(azureClientSecretEnv, "from-env")

	var captured client.CreateIntegrationRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"int-azure-1","type":"AZURE_METRICS"}`)
	}))
	defer srv.Close()

	cmd := newAzureAddCmd()
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--subscription-name", "n", "--tenant-id", "t",
		"--subscription-id", "s", "--client-id", "c",
		"--client-secret", "from-flag",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	az := decodeAzureCreate(t, captured)
	if az.ClientSecret == nil || *az.ClientSecret != "from-flag" {
		t.Errorf("ClientSecret = %v, want from-flag", az.ClientSecret)
	}
}

// TestAzureAddCmd_NoSecretNonInteractive asserts we fail with an actionable
// message rather than sending a credential-less payload the server would
// silently accept as an unconfigured tile.
func TestAzureAddCmd_NoSecretNonInteractive(t *testing.T) {
	cmd := newAzureAddCmd()
	cmd.SetContext(baseAzureCtx())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--subscription-name", "n", "--tenant-id", "t",
		"--subscription-id", "s", "--client-id", "c",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no secret is available")
	}
	if !strings.Contains(err.Error(), azureClientSecretEnv) {
		t.Errorf("error %q does not mention %s", err.Error(), azureClientSecretEnv)
	}
}

func TestAzureAddCmd_InvalidTag(t *testing.T) {
	cmd := newAzureAddCmd()
	cmd.SetContext(baseAzureCtx())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--subscription-name", "n", "--tenant-id", "t",
		"--subscription-id", "s", "--client-id", "c",
		"--client-secret", "x",
		"--tag", "novalue",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "expected key=value") {
		t.Fatalf("err = %v, want key=value complaint", err)
	}
}

func TestAzureListCmd_FiltersByType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"id":"aws-1","type":"CLOUDWATCH_METRIC_PULL","status":"ACTIVE"},
			{"id":"az-1","name":"OodleAI POC","type":"AZURE_METRICS","status":"RECEIVING",
			 "typeSpecificData":{"azureMetricsIntegration":{
			   "subscriptionId":"sub-xyz","clientSecret":"********",
			   "serviceFilters":[{"serviceIds":["compute_virtualmachines"]}]}}}
		]`)
	}))
	defer srv.Close()

	var out bytes.Buffer
	cmd := newAzureListCmd()
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var rows []azureListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("parsing output %q: %v", out.String(), err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want only the AZURE_METRICS entry", rows)
	}
	want := azureListRow{
		ID:           "az-1",
		Name:         "OodleAI POC",
		Subscription: "sub-xyz",
		Services:     "compute_virtualmachines",
		Status:       "RECEIVING",
	}
	if rows[0] != want {
		t.Errorf("row = %+v, want %+v", rows[0], want)
	}
}

func TestAzureListCmd_TableFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"az-1","name":"POC","type":"AZURE_METRICS","status":"RECEIVING",
		 "typeSpecificData":{"azureMetricsIntegration":{"subscriptionId":"sub-xyz"}}}]`)
	}))
	defer srv.Close()

	var out bytes.Buffer
	cmd := newAzureListCmd()
	cmd.SetContext(azureCtxWithFormat(t, srv.URL, output.FormatTable))
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Column headers must resolve against azureListRow's field names, which is
	// what output.Print reflects over.
	for _, want := range []string{"ID", "NAME", "SUBSCRIPTION", "SERVICES", "STATUS", "az-1", "sub-xyz"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table output missing %q:\n%s", want, out.String())
		}
	}
}

// TestAzureUpdateCmd_PreservesSecretAndUntouchedFields is the core of the
// patch contract: the server rewrites every Azure column from the request, so
// an omitted flag must still be sent with its existing value, and the masked
// secret must round-trip so the stored credential survives.
func TestAzureUpdateCmd_PreservesSecretAndUntouchedFields(t *testing.T) {
	var captured client.PatchIntegration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"az-1","type":"AZURE_METRICS","status":"RECEIVING",
			 "typeSpecificData":{"azureMetricsIntegration":{
			   "subscriptionName":"OodleAI POC","tenantId":"tenant-abc",
			   "subscriptionId":"sub-xyz","clientId":"client-123",
			   "clientSecret":"********","resourceGroups":["rg-one"],
			   "serviceFilters":[{"serviceIds":["compute_virtualmachines"]}]}}}`)
		case http.MethodPatch:
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"az-1","type":"AZURE_METRICS"}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	cmd := newAzureUpdateCmd()
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"az-1", "--services", "compute_virtualmachines,sql_servers"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.Type == nil || *captured.Type != azureIntegrationType {
		t.Errorf("Type = %v, want %q", captured.Type, azureIntegrationType)
	}
	az := decodeAzurePatch(t, captured)
	// The masked secret round-trips: the server reads it as "keep what I have".
	if az.ClientSecret == nil || *az.ClientSecret != azureSecretMask {
		t.Errorf("ClientSecret = %v, want the mask %q", az.ClientSecret, azureSecretMask)
	}
	// Untouched fields carry their existing values through the patch.
	if az.SubscriptionName == nil || *az.SubscriptionName != "OodleAI POC" {
		t.Errorf("SubscriptionName = %v, want carried through", az.SubscriptionName)
	}
	if az.TenantId == nil || *az.TenantId != "tenant-abc" {
		t.Errorf("TenantId = %v, want carried through", az.TenantId)
	}
	if az.ClientId == nil || *az.ClientId != "client-123" {
		t.Errorf("ClientId = %v, want carried through", az.ClientId)
	}
	if az.ResourceGroups == nil || len(*az.ResourceGroups) != 1 || (*az.ResourceGroups)[0] != "rg-one" {
		t.Errorf("ResourceGroups = %v, want carried through", az.ResourceGroups)
	}
	// The touched field is replaced.
	if az.ServiceFilters == nil || len(*az.ServiceFilters) != 1 {
		t.Fatalf("ServiceFilters = %v", az.ServiceFilters)
	}
	ids := (*az.ServiceFilters)[0].ServiceIds
	if ids == nil || len(*ids) != 2 || (*ids)[1] != "sql_servers" {
		t.Errorf("ServiceIds = %v, want the replacement pair", ids)
	}
}

func TestAzureUpdateCmd_ReplacesSecretWhenSupplied(t *testing.T) {
	var captured client.PatchIntegration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"az-1","type":"AZURE_METRICS",
			 "typeSpecificData":{"azureMetricsIntegration":{"clientSecret":"********"}}}`)
		case http.MethodPatch:
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"az-1","type":"AZURE_METRICS"}`)
		}
	}))
	defer srv.Close()

	cmd := newAzureUpdateCmd()
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"az-1", "--client-secret", "rotated"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	az := decodeAzurePatch(t, captured)
	if az.ClientSecret == nil || *az.ClientSecret != "rotated" {
		t.Errorf("ClientSecret = %v, want rotated", az.ClientSecret)
	}
}

// TestAzureUpdateCmd_RefusesTagOnlyPatchWithMultipleGroups guards the same
// silent-collapse hazard the AWS namespace patch guards against.
func TestAzureUpdateCmd_RefusesTagOnlyPatchWithMultipleGroups(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s: the patch must not be sent", r.Method)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"az-1","type":"AZURE_METRICS",
		 "typeSpecificData":{"azureMetricsIntegration":{"clientSecret":"********","serviceFilters":[
		   {"serviceIds":["compute_virtualmachines"],"tags":{"env":"prod"}},
		   {"serviceIds":["sql_servers"],"tags":{"env":"dev"}}]}}}`)
	}))
	defer srv.Close()

	cmd := newAzureUpdateCmd()
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"az-1", "--tag", "env=staging"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected refusal when patching tags across multiple filter groups")
	}
	if !strings.Contains(err.Error(), "--services") {
		t.Errorf("error %q should point at --services", err.Error())
	}
}

func TestAzureUpdateCmd_RejectsWrongType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"aws-1","type":"CLOUDWATCH_METRIC_PULL"}`)
	}))
	defer srv.Close()

	cmd := newAzureUpdateCmd()
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"aws-1", "--subscription-name", "nope"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), azureIntegrationType) {
		t.Fatalf("err = %v, want a type mismatch complaint", err)
	}
}

func TestAzureDeleteCmd(t *testing.T) {
	var deletedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s", r.Method)
			return
		}
		deletedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var out bytes.Buffer
	cmd := newAzureDeleteCmd()
	// The delete confirmation reads the root's persistent --force flag.
	cmd.Flags().Bool("force", true, "")
	cmd.SetContext(azureCtxWith(t, srv.URL))
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{"az-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(deletedPath, "/integrations/az-1") {
		t.Errorf("deleted path = %q", deletedPath)
	}
	if !strings.Contains(out.String(), "az-1") {
		t.Errorf("output = %q, want it to name the deleted id", out.String())
	}
}

// TestBuildAzureServiceFilters_OmittedWhenEmpty keeps an unfiltered integration
// from sending an empty group, which would read as "collect nothing" rather
// than "collect the defaults".
func TestBuildAzureServiceFilters_OmittedWhenEmpty(t *testing.T) {
	got, err := buildAzureServiceFilters(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ServiceFilters = %v, want nil so the server applies defaults", got)
	}
}

// --- test helpers ---

func baseAzureCtx() context.Context {
	ctx := context.Background()
	ctx = withInstance(ctx, "test-instance")
	ctx = withOutput(ctx, output.FormatJSON)
	return ctx
}

func azureCtxWith(t *testing.T, serverURL string) context.Context {
	t.Helper()
	return withClient(baseAzureCtx(), newTestClient(t, serverURL))
}

func azureCtxWithFormat(t *testing.T, serverURL string, f output.Format) context.Context {
	t.Helper()
	return withOutput(azureCtxWith(t, serverURL), f)
}

func decodeAzureCreate(t *testing.T, req client.CreateIntegrationRequest) client.AzureMetricsIntegration {
	t.Helper()
	if req.TypeSpecificData == nil {
		t.Fatal("typeSpecificData missing")
	}
	wrapper, err := req.TypeSpecificData.AsAzureMetricsIntegrationWrapper()
	if err != nil {
		t.Fatalf("decoding wrapper: %v", err)
	}
	if wrapper.AzureMetricsIntegration == nil {
		t.Fatal("azureMetricsIntegration payload missing")
	}
	return *wrapper.AzureMetricsIntegration
}

func decodeAzurePatch(t *testing.T, req client.PatchIntegration) client.AzureMetricsIntegration {
	t.Helper()
	if req.TypeSpecificData == nil {
		t.Fatal("typeSpecificData missing")
	}
	wrapper, err := req.TypeSpecificData.AsAzureMetricsIntegrationWrapper()
	if err != nil {
		t.Fatalf("decoding wrapper: %v", err)
	}
	if wrapper.AzureMetricsIntegration == nil {
		t.Fatal("azureMetricsIntegration payload missing")
	}
	return *wrapper.AzureMetricsIntegration
}
