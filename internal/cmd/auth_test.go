package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/config"
)

func TestOAuthClientIDForDomain(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		wantID string
	}{
		{name: "us1 deployment domain", domain: us1DeploymentDomain, wantID: us1ClientID},
		{name: "us1 oauth deployment domain", domain: us1OAuthDeploymentDomain, wantID: us1ClientID},
		{name: "ap1 deployment domain", domain: ap1DeploymentDomain, wantID: ap1ClientID},
		{name: "ap1 oauth deployment domain", domain: ap1OAuthDeploymentDomain, wantID: ap1ClientID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := oauthClientIDForDomain(tt.domain)
			if err != nil {
				t.Fatalf("oauthClientIDForDomain returned error: %v", err)
			}
			if id != tt.wantID {
				t.Fatalf("oauthClientIDForDomain returned %q, want %q", id, tt.wantID)
			}
		})
	}
}

func TestOAuthClientIDForDomainUnsupported(t *testing.T) {
	_, err := oauthClientIDForDomain("example.com")
	if err == nil {
		t.Fatal("expected error for unsupported deployment domain")
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "us1", want: us1DeploymentDomain},
		{in: "US1", want: us1DeploymentDomain},
		{in: "us1.oodle.ai", want: us1DeploymentDomain},
		{in: "https://us1.oodle.ai", want: us1DeploymentDomain},
		{in: "ap1", want: ap1DeploymentDomain},
		{in: "AP1", want: ap1DeploymentDomain},
		{in: "ap1.oodle.ai", want: ap1DeploymentDomain},
		{in: "https://ap1.oodle.ai/", want: ap1DeploymentDomain},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normalizeDomain(tt.in)
			if err != nil {
				t.Fatalf("normalizeDomain returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeDomain(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestOAuthDeploymentDomainForDomain(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: us1DeploymentDomain, want: us1OAuthDeploymentDomain},
		{in: ap1DeploymentDomain, want: ap1OAuthDeploymentDomain},
		{in: us1OAuthDeploymentDomain, want: us1OAuthDeploymentDomain},
		{in: ap1OAuthDeploymentDomain, want: ap1OAuthDeploymentDomain},
	}
	for _, tt := range tests {
		if got := oauthDeploymentDomainForDomain(tt.in); got != tt.want {
			t.Fatalf("oauthDeploymentDomainForDomain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveInstanceForLogin_SingleInstanceAutoSelects(t *testing.T) {
	instances := []oauthOrgInstance{{ID: "oodle_internal", Name: "internal", Status: "ACTIVE"}}
	out := &bytes.Buffer{}
	got, err := resolveInstanceForLogin(strings.NewReader(""), out, instances, "")
	if err != nil {
		t.Fatalf("resolveInstanceForLogin returned error: %v", err)
	}
	if got != "oodle_internal" {
		t.Fatalf("resolveInstanceForLogin returned %q, want oodle_internal", got)
	}
}

func TestResolveInstanceForLogin_MultipleInstancesPromptsAndSelects(t *testing.T) {
	instances := []oauthOrgInstance{
		{ID: "a", Name: "alpha", Status: "ACTIVE"},
		{ID: "b", Name: "beta", Status: "ACTIVE"},
	}
	out := &bytes.Buffer{}
	got, err := resolveInstanceForLogin(strings.NewReader("b\n"), out, instances, "")
	if err != nil {
		t.Fatalf("resolveInstanceForLogin returned error: %v", err)
	}
	if got != "b" {
		t.Fatalf("resolveInstanceForLogin returned %q, want b", got)
	}
	if !strings.Contains(out.String(), "Available instances:") {
		t.Fatalf("expected available instances prompt, got: %q", out.String())
	}
}

func TestResolveInstanceForLogin_RejectsUnknownInstance(t *testing.T) {
	instances := []oauthOrgInstance{
		{ID: "a", Name: "alpha", Status: "ACTIVE"},
		{ID: "b", Name: "beta", Status: "ACTIVE"},
	}
	_, err := resolveInstanceForLogin(strings.NewReader("c\n"), &bytes.Buffer{}, instances, "")
	if err == nil {
		t.Fatal("expected error for unknown instance")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestFetchOAuthOrg_SendsSessionAsCookie(t *testing.T) {
	const token = "token-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/org" {
			t.Fatalf("request path = %q, want /v1/api/org", r.URL.Path)
		}
		if got := r.Header.Get("__oodle_session"); got != "" {
			t.Fatalf("__oodle_session header = %q, want empty", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization header = %q, want empty", got)
		}
		cookie, err := r.Cookie("__oodle_session")
		if err != nil {
			t.Fatalf("expected __oodle_session cookie: %v", err)
		}
		if cookie.Value != token {
			t.Fatalf("__oodle_session cookie value = %q, want %q", cookie.Value, token)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"org-1","name":"test-org","instances":[{"id":"inst-1","name":"one","status":"ACTIVE"}]}`))
	}))
	defer srv.Close()

	org, err := fetchOAuthOrg(context.Background(), srv.URL, token)
	if err != nil {
		t.Fatalf("fetchOAuthOrg returned error: %v", err)
	}
	if org.ID != "org-1" {
		t.Fatalf("org id = %q, want org-1", org.ID)
	}
	if len(org.Instances) != 1 || org.Instances[0].ID != "inst-1" {
		t.Fatalf("instances = %#v, want one instance with id inst-1", org.Instances)
	}
}

func TestRunAuthGetInstance_SavesSingleInstance(t *testing.T) {
	const token = "oauth-token"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/org" {
			t.Fatalf("request path = %q, want /v1/api/org", r.URL.Path)
		}
		cookie, err := r.Cookie("__oodle_session")
		if err != nil {
			t.Fatalf("expected __oodle_session cookie: %v", err)
		}
		if cookie.Value != token {
			t.Fatalf("__oodle_session cookie value = %q, want %q", cookie.Value, token)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"org-1","name":"test-org","instances":[{"id":"oodle_internal","name":"internal","status":"ACTIVE"}]}`))
	}))
	defer srv.Close()

	oldHTTPClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	t.Cleanup(func() { http.DefaultClient = oldHTTPClient })

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(strings.Join([]string{
		"oauth_access_token: " + token,
		"api_url: https://ap1.oodle.ai",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("OODLE_CONFIG", cfgPath)

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetContext(context.Background())

	flags := &rootFlags{apiURL: srv.URL}
	if err := runAuthGetInstance(cmd, flags, ""); err != nil {
		t.Fatalf("runAuthGetInstance returned error: %v", err)
	}

	saved, err := loadExistingConfig()
	if err != nil {
		t.Fatalf("loadExistingConfig returned error: %v", err)
	}
	if saved.Instance != "oodle_internal" {
		t.Fatalf("saved instance = %q, want oodle_internal", saved.Instance)
	}

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	expectedAPIURL := "https://" + u.Host
	if saved.APIURL != expectedAPIURL {
		t.Fatalf("saved api_url = %q, want %q", saved.APIURL, expectedAPIURL)
	}
	if !strings.Contains(out.String(), "Saved instance") {
		t.Fatalf("expected success output, got %q", out.String())
	}
}

func TestRunAuthGetInstance_RequiresSavedOAuthLogin(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("OODLE_CONFIG", cfgPath)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())

	err := runAuthGetInstance(cmd, &rootFlags{}, "")
	if err == nil {
		t.Fatal("expected error when OAuth login is missing")
	}
	if !strings.Contains(err.Error(), "OAuth login is required") {
		t.Fatalf("expected OAuth login error, got: %v", err)
	}
}

func TestListenOnAllowedOAuthCallbackPort(t *testing.T) {
	ln, err := listenOnAllowedOAuthCallbackPort()
	if err != nil {
		t.Fatalf("listenOnAllowedOAuthCallbackPort returned error: %v", err)
	}
	defer ln.Close()

	got := ln.Addr().String()
	if len(got) < len("127.0.0.1:9400") || got[:len("127.0.0.1:")] != "127.0.0.1:" {
		t.Fatalf("unexpected listener address: %s", got)
	}
}

func TestRunAuthLogout_RemovesOAuthTokens(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(strings.Join([]string{
		"oauth_access_token: access-token",
		"oauth_refresh_token: refresh-token",
		"instance: test-instance",
		"api_url: https://app-dev.oodle.ai",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("OODLE_CONFIG", cfgPath)

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := runAuthLogout(cmd); err != nil {
		t.Fatalf("runAuthLogout returned error: %v", err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(raw)
	if strings.Contains(content, "oauth_access_token:") {
		t.Fatalf("oauth_access_token should be removed, got:\n%s", content)
	}
	if strings.Contains(content, "oauth_refresh_token:") {
		t.Fatalf("oauth_refresh_token should be removed, got:\n%s", content)
	}
	if !strings.Contains(content, "instance: test-instance") {
		t.Fatalf("instance should be preserved, got:\n%s", content)
	}
}

func TestRunAuthLogout_NoOAuthTokens(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(strings.Join([]string{
		"api_key: key",
		"instance: test-instance",
		"api_url: https://app-dev.oodle.ai",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("OODLE_CONFIG", cfgPath)

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := runAuthLogout(cmd); err != nil {
		t.Fatalf("runAuthLogout returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "already logged out") {
		t.Fatalf("unexpected output: %s", buf.String())
	}
}

func TestPromptDeleteAPIKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "default no", in: "\n", want: false},
		{name: "explicit no", in: "no\n", want: false},
		{name: "yes short", in: "y\n", want: true},
		{name: "yes full", in: "yes\n", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			got, err := promptDeleteAPIKey(strings.NewReader(tt.in), out)
			if err != nil {
				t.Fatalf("promptDeleteAPIKey returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("promptDeleteAPIKey(%q) = %v, want %v", tt.in, got, tt.want)
			}
			if !strings.Contains(out.String(), "Do you want to delete it?") {
				t.Fatalf("expected prompt output, got: %q", out.String())
			}
		})
	}
}

func TestComputeAuthStatus_PrefersOAuthWhenBothPresent(t *testing.T) {
	now := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	existing := &config.Config{
		APIKey:            "api-key",
		OAuthAccessToken:  "oauth-access",
		OAuthRefreshToken: "refresh-token",
		OAuthTokenExpiry:  now,
		OAuthClientID:     "client-id",
		OAuthAuthServer:   "https://auth.example.com",
		Instance:          "inst",
		APIURL:            "https://app-dev.oodle.ai",
	}
	row := computeAuthStatus(existing, &rootFlags{})
	if row.PreferredAuth != "oauth" {
		t.Fatalf("PreferredAuth = %q, want oauth", row.PreferredAuth)
	}
	if !row.RefreshEnabled {
		t.Fatal("RefreshEnabled = false, want true")
	}
	if row.OAuthExpired {
		t.Fatal("OAuthExpired = true, want false")
	}
}

func TestComputeAuthStatus_EnvOverrides(t *testing.T) {
	t.Setenv("OODLE_API_KEY", "env-api")
	t.Setenv("OODLE_OAUTH_ACCESS_TOKEN", "env-oauth")
	t.Setenv("OODLE_OAUTH_REFRESH_TOKEN", "env-refresh")
	t.Setenv("OODLE_INSTANCE", "env-instance")
	t.Setenv("OODLE_DEPLOYMENT", "https://env.example.com")

	row := computeAuthStatus(nil, &rootFlags{})
	if row.PreferredAuth != "oauth" {
		t.Fatalf("PreferredAuth = %q, want oauth", row.PreferredAuth)
	}
	if row.Instance != "env-instance" {
		t.Fatalf("Instance = %q, want env-instance", row.Instance)
	}
	if row.APIURL != "https://env.example.com" {
		t.Fatalf("APIURL = %q, want https://env.example.com", row.APIURL)
	}
	if row.RefreshEnabled {
		t.Fatal("RefreshEnabled = true, want false (missing client metadata)")
	}
}
