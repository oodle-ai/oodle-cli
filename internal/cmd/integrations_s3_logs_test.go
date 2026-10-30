package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

type s3LogsServer struct {
	records []map[string]interface{}
	created []map[string]interface{}
}

func (s *s3LogsServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/api/instance/test-instance/integrations" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(s.records)
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("bad body: %v", err)
			}
			s.created = append(s.created, req)
			req["id"] = "new-id"
			_ = json.NewEncoder(w).Encode(req)
		}
	})
}

func s3Record(id, region, externalID, nonce, roleArn string) map[string]interface{} {
	return map[string]interface{}{
		"id":   id,
		"type": "LOGS_S3",
		"typeSpecificData": map[string]interface{}{
			"s3LogPullIntegration": map[string]interface{}{
				"externalId": externalID, "nonce": nonce, "region": region, "roleArn": roleArn,
			},
		},
	}
}

func runS3LogsAdd(t *testing.T, srv *httptest.Server, f output.Format, args ...string) (string, error) {
	t.Helper()
	orig := s3LogsStackSuffix
	s3LogsStackSuffix = func() string { return "123" }
	defer func() { s3LogsStackSuffix = orig }()
	cmd := newS3LogsAddCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(configuredCtx(t, srv.URL, f))
	return out.String(), err
}

func TestS3LogsAdd_CreatesRecordAndPrintsValues(t *testing.T) {
	s := &s3LogsServer{records: []map[string]interface{}{
		// A connected record gives its ExternalId but is not used again.
		s3Record("old", "us-east-1", "ext-1", "n-old", "arn:aws:iam::1:role/x"),
	}}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	out, err := runS3LogsAdd(t, srv, output.FormatJSON, "--region", "us-east-1", "--bucket-patterns", " a , b/logs/ ")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if len(s.created) != 1 {
		t.Fatalf("created %d records, want 1", len(s.created))
	}
	req := s.created[0]
	if req["type"] != "LOGS_S3" || req["status"] != "NOT_CONNECTED" {
		t.Errorf("request = %v", req)
	}
	s3 := req["typeSpecificData"].(map[string]interface{})["s3LogPullIntegration"].(map[string]interface{})
	if s3["externalId"] != "ext-1" || s3["region"] != "us-east-1" || s3["nonce"] == "" {
		t.Errorf("s3 data = %v", s3)
	}

	var got s3LogsSetup
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parsing output: %v\n%s", err, out)
	}
	if got.IntegrationID != "new-id" || got.Reused || got.StackName != "oodle-s3-logs-123" {
		t.Errorf("setup = %+v", got)
	}
	if got.Parameters["ExternalId"] != "ext-1" || got.Parameters["Nonce"] != s3["nonce"] ||
		got.Parameters["S3BucketPatterns"] != "a,b/logs/" {
		t.Errorf("parameters = %v", got.Parameters)
	}
	if _, ok := got.Parameters["SnsTopicArn"]; ok {
		t.Errorf("SnsTopicArn is set without --sns-topic-arn")
	}
	cb, err := url.Parse(got.Parameters["OodleCallbackUrl"])
	if err != nil || cb.Path != "/v1/api/integrations/s3-logs/callback" ||
		cb.Query().Get("instance") != "test-instance" || cb.Query().Get("integrationId") != "new-id" {
		t.Errorf("callback = %q", got.Parameters["OodleCallbackUrl"])
	}
	for _, want := range []string{"--capabilities CAPABILITY_NAMED_IAM CAPABILITY_AUTO_EXPAND", `ParameterValue="a,b/logs/"`, "--region us-east-1"} {
		if !strings.Contains(got.CreateStackCmd, want) {
			t.Errorf("create-stack command does not contain %q:\n%s", want, got.CreateStackCmd)
		}
	}
	if !strings.Contains(got.ConsoleURL, "param_ExternalId=ext-1") || !strings.Contains(got.ConsoleURL, "region=us-east-1") {
		t.Errorf("console URL = %s", got.ConsoleURL)
	}
}

func TestS3LogsAdd_ReusesPendingRecord(t *testing.T) {
	s := &s3LogsServer{records: []map[string]interface{}{
		s3Record("pending", "eu-west-1", "ext-2", "n-2", ""),
	}}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	out, err := runS3LogsAdd(t, srv, output.FormatTable, "--region", "eu-west-1", "--bucket-patterns", "a",
		"--sns-topic-arn", "arn:aws:sns:eu-west-1:1:t")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if len(s.created) != 0 {
		t.Errorf("created a record although one waits for its stack")
	}
	for _, want := range []string{"Integration: pending (exists", "Nonce:            n-2", "SnsTopicArn:", "wait stack-create-complete"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}

	// --new always adds a record.
	if _, err := runS3LogsAdd(t, srv, output.FormatJSON, "--region", "eu-west-1", "--bucket-patterns", "a", "--new"); err != nil {
		t.Fatal(err)
	}
	if len(s.created) != 1 {
		t.Errorf("--new created %d records, want 1", len(s.created))
	}
}

func TestS3LogsAdd_FlagsRequired(t *testing.T) {
	s := &s3LogsServer{}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	if _, err := runS3LogsAdd(t, srv, output.FormatJSON, "--region", "us-east-1"); err == nil {
		t.Error("expected an error without --bucket-patterns")
	}
	if _, err := runS3LogsAdd(t, srv, output.FormatJSON, "--region", "us-east-1", "--bucket-patterns", " , "); err == nil {
		t.Error("expected an error for empty patterns")
	}
}
