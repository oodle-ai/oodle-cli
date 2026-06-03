package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// TestNewAwsCmd_Structure pins the subcommand layout so accidental
// renames/removes are caught here, not at the user's terminal.
func TestNewAwsCmd_Structure(t *testing.T) {
	cmd := newAwsCmd()
	if cmd.Use != "aws" {
		t.Errorf("Use = %q, want %q", cmd.Use, "aws")
	}
	want := []string{"add", "delete", "generate-setup-url", "list", "update"}
	got := subcommandNames(cmd)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("subcommands = %v, want %v", got, want)
	}
}

// TestAwsCmd_RegisteredUnderIntegrations ensures we wired newAwsCmd into the
// integrations group rather than leaving it orphaned.
func TestAwsCmd_RegisteredUnderIntegrations(t *testing.T) {
	cmd := newIntegrationsCmd()
	if findSubcommand(cmd, "aws") == nil {
		t.Error("integrations: missing subcommand 'aws'")
	}
}

func TestAwsAddCmd_FlagsRequired(t *testing.T) {
	// No flags supplied → the missing-flag check fires before any HTTP call,
	// so we can test it with a nil client by jumping straight to RunE.
	cmd := newAwsAddCmd()
	cmd.SetContext(baseAwsCtx())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatal("expected error when required flags are missing")
	}
	for _, f := range []string{"--account-id", "--role-arn", "--regions", "--namespaces"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q does not mention %s", err.Error(), f)
		}
	}
}

func TestAwsAddCmd_HappyPath(t *testing.T) {
	var captured client.CreateIntegrationRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// External-id lookup: respond with no existing integrations so
			// the CLI generates a fresh UUIDv7.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"int-123","type":"CLOUDWATCH_METRIC_PULL","status":"INACTIVE"}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	cmd := newAwsAddCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--account-id", "123456789012",
		"--role-arn", "arn:aws:iam::123456789012:role/OodleIntegrationRole",
		"--regions", "us-west-2,us-east-1",
		"--namespaces", "AWS/EC2,AWS/RDS",
		"--tag", "Environment=prod",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.Type != awsIntegrationType {
		t.Errorf("Type = %q, want %q", captured.Type, awsIntegrationType)
	}
	if captured.Status == nil || *captured.Status != "INACTIVE" {
		t.Errorf("Status = %v, want INACTIVE", captured.Status)
	}
	wrapper, err := captured.TypeSpecificData.AsCloudWatchMetricPullIntegrationWrapper()
	if err != nil {
		t.Fatalf("decoding wrapper: %v", err)
	}
	cw := wrapper.CloudWatchMetricPullIntegration
	if cw == nil {
		t.Fatal("CloudWatch payload missing")
	}
	if cw.AccountId == nil || *cw.AccountId != "123456789012" {
		t.Errorf("AccountId = %v, want 123456789012", cw.AccountId)
	}
	if cw.RoleArn == nil || *cw.RoleArn != "arn:aws:iam::123456789012:role/OodleIntegrationRole" {
		t.Errorf("RoleArn = %v", cw.RoleArn)
	}
	if cw.ExternalId == nil || *cw.ExternalId == "" {
		t.Error("expected generated ExternalId, got empty")
	}
	if cw.Regions == nil || len(*cw.Regions) != 2 || (*cw.Regions)[0] != "us-west-2" {
		t.Errorf("Regions = %v", cw.Regions)
	}
	if cw.ResourceTypesSearchTagsList == nil || len(*cw.ResourceTypesSearchTagsList) != 1 {
		t.Fatalf("ResourceTypesSearchTagsList = %v", cw.ResourceTypesSearchTagsList)
	}
	first := (*cw.ResourceTypesSearchTagsList)[0]
	if first.ResourceTypes == nil || len(*first.ResourceTypes) != 2 {
		t.Errorf("ResourceTypes = %v", first.ResourceTypes)
	}
	if first.SearchTags == nil || len(*first.SearchTags) != 1 {
		t.Fatalf("SearchTags = %v", first.SearchTags)
	}
	if got := (*first.SearchTags)[0]; got.Key == nil || *got.Key != "Environment" || got.Value == nil || *got.Value != "prod" {
		t.Errorf("tag = %v=%v, want Environment=prod", got.Key, got.Value)
	}
}

func TestAwsAddCmd_ReusesExistingExternalID(t *testing.T) {
	const existing = "existing-external-id-abc"
	var captured client.CreateIntegrationRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `[{
				"id":"prior","type":"CLOUDWATCH_METRIC_PULL",
				"typeSpecificData":{"cloudWatchMetricPullIntegration":{"externalId":%q}}
			}]`, existing)
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&captured)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"int-new"}`)
		}
	}))
	defer srv.Close()

	cmd := newAwsAddCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--account-id", "111111111111",
		"--role-arn", "arn:aws:iam::111111111111:role/OodleIntegrationRole",
		"--regions", "us-west-2",
		"--namespaces", "AWS/EC2",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wrapper, err := captured.TypeSpecificData.AsCloudWatchMetricPullIntegrationWrapper()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := wrapper.CloudWatchMetricPullIntegration.ExternalId
	if got == nil || *got != existing {
		t.Errorf("ExternalId = %v, want %q", got, existing)
	}
}

func TestAwsAddCmd_BackendErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"errors":[{"message":"Could not assume IAM role: AccessDenied","code":"INVALID_INPUT"}]}`)
		}
	}))
	defer srv.Close()

	cmd := newAwsAddCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--account-id", "123456789012",
		"--role-arn", "arn:aws:iam::123456789012:role/Broken",
		"--regions", "us-west-2",
		"--namespaces", "AWS/EC2",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error from the backend")
	}
	if !strings.Contains(err.Error(), "Could not assume IAM role") {
		t.Errorf("error %q does not include IAM failure message", err.Error())
	}
}

func TestAwsListCmd_FiltersAndProjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
		  {"id":"a","type":"GRAFANA","status":"ACTIVE"},
		  {"id":"b","type":"CLOUDWATCH_METRIC_PULL","status":"RECEIVING",
		   "typeSpecificData":{"cloudWatchMetricPullIntegration":{"accountId":"123","regions":["us-west-2","us-east-1"]}}},
		  {"id":"c","type":"CLOUDWATCH_METRIC_PULL","status":"INACTIVE",
		   "typeSpecificData":{"cloudWatchMetricPullIntegration":{"accountId":"456","regions":["eu-central-1"]}}}
		]`)
	}))
	defer srv.Close()

	cmd := newAwsListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetContext(awsCtxWithFormat(t, srv.URL, output.FormatJSON))

	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var rows []awsListRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v\noutput=%s", err, buf.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (Grafana entry should be filtered)", len(rows))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if rows[0].ID != "b" || rows[0].Account != "123" || rows[0].Regions != "us-west-2,us-east-1" || rows[0].Status != "RECEIVING" {
		t.Errorf("row[0] = %+v", rows[0])
	}
	if rows[1].ID != "c" || rows[1].Account != "456" || rows[1].Regions != "eu-central-1" {
		t.Errorf("row[1] = %+v", rows[1])
	}
}

func TestAwsUpdateCmd_OverlaysFields(t *testing.T) {
	var captured client.PatchIntegration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"id":"int-1","type":"CLOUDWATCH_METRIC_PULL","status":"RECEIVING",
				"typeSpecificData":{"cloudWatchMetricPullIntegration":{
				   "accountId":"123","externalId":"keep-me",
				   "roleArn":"arn:aws:iam::123:role/Old",
				   "regions":["us-west-2"],
				   "resourceTypesSearchTagsList":[{"resourceTypes":["AWS/EC2"]}]
				}}
			}`)
		case http.MethodPatch:
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode patch body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"int-1"}`)
		}
	}))
	defer srv.Close()

	cmd := newAwsUpdateCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetArgs([]string{
		"int-1",
		"--regions", "us-west-2,us-east-1",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wrapper, err := captured.TypeSpecificData.AsCloudWatchMetricPullIntegrationWrapper()
	if err != nil {
		t.Fatalf("decode wrapper: %v", err)
	}
	cw := wrapper.CloudWatchMetricPullIntegration
	if cw == nil {
		t.Fatal("CW payload missing")
	}
	// Regions overridden, everything else preserved.
	if cw.Regions == nil || len(*cw.Regions) != 2 {
		t.Errorf("Regions = %v, want 2 entries", cw.Regions)
	}
	if cw.ExternalId == nil || *cw.ExternalId != "keep-me" {
		t.Errorf("ExternalId = %v, want keep-me", cw.ExternalId)
	}
	if cw.RoleArn == nil || *cw.RoleArn != "arn:aws:iam::123:role/Old" {
		t.Errorf("RoleArn = %v, want preserved", cw.RoleArn)
	}
	// Resource types unchanged (no --namespaces / --tag supplied).
	if cw.ResourceTypesSearchTagsList == nil || len(*cw.ResourceTypesSearchTagsList) != 1 {
		t.Fatalf("ResourceTypesSearchTagsList = %v", cw.ResourceTypesSearchTagsList)
	}
	got := (*cw.ResourceTypesSearchTagsList)[0].ResourceTypes
	if got == nil || (*got)[0] != "AWS/EC2" {
		t.Errorf("ResourceTypes = %v, want preserved [AWS/EC2]", got)
	}
}

func TestAwsUpdateCmd_RefusesTagPatchOnMultiNamespace(t *testing.T) {
	patched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"id":"int-1","type":"CLOUDWATCH_METRIC_PULL",
				"typeSpecificData":{"cloudWatchMetricPullIntegration":{
				   "accountId":"123",
				   "resourceTypesSearchTagsList":[
				     {"resourceTypes":["AWS/EC2"]},
				     {"resourceTypes":["AWS/RDS"],"searchTags":[{"key":"Env","value":"prod"}]}
				   ]
				}}
			}`)
		case http.MethodPatch:
			patched = true
		}
	}))
	defer srv.Close()

	cmd := newAwsUpdateCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetArgs([]string{"int-1", "--tag", "Env=staging"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error refusing to patch tags on a multi-namespace integration")
	}
	if !strings.Contains(err.Error(), "--namespaces") {
		t.Errorf("error %q should mention --namespaces remediation", err.Error())
	}
	if patched {
		t.Error("PATCH should not have been sent")
	}
}

func TestAwsListCmd_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"b","type":"CLOUDWATCH_METRIC_PULL","status":"RECEIVING",
		   "typeSpecificData":{"cloudWatchMetricPullIntegration":{"accountId":"123","regions":["us-west-2"]}}}]`)
	}))
	defer srv.Close()

	cmd := newAwsListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetContext(awsCtxWithFormat(t, srv.URL, output.FormatTable))

	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, header := range []string{"ID", "ACCOUNT", "REGIONS", "STATUS"} {
		if !strings.Contains(out, header) {
			t.Errorf("missing column header %q in table output:\n%s", header, out)
		}
	}
	for _, want := range []string{"b", "123", "us-west-2", "RECEIVING"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing value %q in table output:\n%s", want, out)
		}
	}
}

func TestAwsDeleteCmd_RequiresID(t *testing.T) {
	cmd := newAwsDeleteCmd()
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error with zero args")
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("expected error with two args")
	}
	if err := cmd.Args(cmd, []string{"id-1"}); err != nil {
		t.Errorf("unexpected error with one arg: %v", err)
	}
}

func TestGenerateSetupURLCmd_BuildsExpectedURL(t *testing.T) {
	// Pin the random suffix so the URL is deterministic.
	prev := cfStackSuffix
	cfStackSuffix = func() string { return "abc123" }
	t.Cleanup(func() { cfStackSuffix = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	cmd := newAwsGenerateSetupURLCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetArgs([]string{
		"--account-id", "123456789012",
		"--external-id", "fixed-ext-id",
		"--cf-region", "us-west-2",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "External ID: fixed-ext-id") {
		t.Errorf("missing external id line in output:\n%s", out)
	}
	// Pull the URL line out and parse it.
	var urlLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "URL: ") {
			urlLine = strings.TrimPrefix(line, "URL: ")
			break
		}
	}
	if urlLine == "" {
		t.Fatalf("missing URL line in output:\n%s", out)
	}

	// Cross-check key pieces against the AWS CF URL format the UI uses.
	if !strings.HasPrefix(urlLine, "https://console.aws.amazon.com/cloudformation/home?region=us-west-2#/stacks/create/review?") {
		t.Errorf("URL prefix unexpected: %s", urlLine)
	}
	frag := strings.SplitN(urlLine, "?", 3)
	if len(frag) != 3 {
		t.Fatalf("URL did not contain expected fragment+query split: %s", urlLine)
	}
	q, err := url.ParseQuery(frag[2])
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	checks := map[string]string{
		"templateURL":             cfTemplateURL,
		"stackName":               "oodle-aws-integration-role-setup-abc123",
		"param_ExternalId":        "fixed-ext-id",
		"param_OodleAWSAccountId": defaultOodleAWSAccountID,
		"param_RoleName":          defaultIAMRoleName,
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
}

func TestParseTagFlags(t *testing.T) {
	tags, err := parseTagFlags([]string{"Env=prod", "Team=core"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if tags == nil || len(*tags) != 2 {
		t.Fatalf("got %v, want 2 tags", tags)
	}
	if _, err := parseTagFlags([]string{"no-equals"}); err == nil {
		t.Error("expected error for malformed tag")
	}
	got, err := parseTagFlags(nil)
	if err != nil || got != nil {
		t.Errorf("nil tags should yield (nil, nil), got (%v, %v)", got, err)
	}
}

// --- shared ctx builders ---

func baseAwsCtx() context.Context {
	ctx := context.Background()
	ctx = withInstance(ctx, "test-instance")
	ctx = withOutput(ctx, output.FormatJSON)
	return ctx
}

func awsCtxWith(t *testing.T, serverURL string) context.Context {
	t.Helper()
	c := newTestClient(t, serverURL)
	return withClient(baseAwsCtx(), c)
}

func awsCtxWithFormat(t *testing.T, serverURL string, f output.Format) context.Context {
	t.Helper()
	return withOutput(awsCtxWith(t, serverURL), f)
}
