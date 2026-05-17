package cmd

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/oodle-ai/oodle-cli/internal/config"
)

func TestOAuthClientIDForDomain(t *testing.T) {
	id, err := oauthClientIDForDomain("app-dev.oodle.ai")
	if err != nil {
		t.Fatalf("oauthClientIDForDomain returned error: %v", err)
	}
	if id != appDevClientID {
		t.Fatalf("oauthClientIDForDomain returned %q, want %q", id, appDevClientID)
	}
}

func TestOAuthClientIDForDomainUnsupported(t *testing.T) {
	_, err := oauthClientIDForDomain("us1.oodle.ai")
	if err == nil {
		t.Fatal("expected error for unsupported deployment domain")
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "app-dev.oodle.ai", want: "app-dev.oodle.ai"},
		{in: "https://app-dev.oodle.ai", want: "app-dev.oodle.ai"},
		{in: "https://APP-DEV.OODLE.AI/", want: "app-dev.oodle.ai"},
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

func TestResolveAuthLoginInstance_PrefersSelectedTokenInstanceOverExistingConfig(t *testing.T) {
	existing := &config.Config{Instance: "old-instance"}
	token := &oauth2.Token{AccessToken: testJWTWithClaims(`{"oodle_instance":"selected-instance"}`)}

	got, err := resolveAuthLoginInstance(strings.NewReader(""), &bytes.Buffer{}, "", existing, token)
	if err != nil {
		t.Fatalf("resolveAuthLoginInstance returned error: %v", err)
	}
	if got != "selected-instance" {
		t.Fatalf("resolveAuthLoginInstance = %q, want selected-instance", got)
	}
}

func TestResolveAuthLoginInstance_PrefersFlagOverSelectedTokenInstance(t *testing.T) {
	existing := &config.Config{Instance: "old-instance"}
	token := &oauth2.Token{AccessToken: testJWTWithClaims(`{"oodle_instance":"selected-instance"}`)}

	got, err := resolveAuthLoginInstance(strings.NewReader(""), &bytes.Buffer{}, "flag-instance", existing, token)
	if err != nil {
		t.Fatalf("resolveAuthLoginInstance returned error: %v", err)
	}
	if got != "flag-instance" {
		t.Fatalf("resolveAuthLoginInstance = %q, want flag-instance", got)
	}
}

func TestInstanceFromOAuthToken_UsesAccessTokenClaim(t *testing.T) {
	token := &oauth2.Token{AccessToken: testJWTWithClaims(`{"oodle_instance":"selected-instance"}`)}

	got := instanceFromOAuthToken(token)
	if got != "selected-instance" {
		t.Fatalf("instanceFromOAuthToken = %q, want selected-instance", got)
	}
}

func TestInstanceFromOAuthToken_FallsBackToIDTokenClaim(t *testing.T) {
	token := (&oauth2.Token{AccessToken: "opaque-access-token"}).WithExtra(map[string]any{
		"id_token": testJWTWithClaims(`{"org_name":"selected-from-id-token"}`),
	})

	got := instanceFromOAuthToken(token)
	if got != "selected-from-id-token" {
		t.Fatalf("instanceFromOAuthToken = %q, want selected-from-id-token", got)
	}
}

func TestInstanceFromOAuthToken_EmptyWhenNoInstanceClaim(t *testing.T) {
	token := (&oauth2.Token{AccessToken: testJWTWithClaims(`{"sub":"user"}`)}).WithExtra(map[string]any{
		"id_token": testJWTWithClaims(`{"email":"user@example.com"}`),
	})

	got := instanceFromOAuthToken(token)
	if got != "" {
		t.Fatalf("instanceFromOAuthToken = %q, want empty", got)
	}
}

func TestInstanceFromOAuthToken_UsesNumericOrgIDClaim(t *testing.T) {
	token := &oauth2.Token{AccessToken: testJWTWithClaims(`{"org_id":12345}`)}

	got := instanceFromOAuthToken(token)
	if got != "12345" {
		t.Fatalf("instanceFromOAuthToken = %q, want 12345", got)
	}
}

func TestResolveAuthLoginInstance_WhitespaceValuesFallBackToPrompt(t *testing.T) {
	existing := &config.Config{Instance: "   "}
	token := &oauth2.Token{AccessToken: testJWTWithClaims(`{"oodle_instance":"   "}`)}
	out := &bytes.Buffer{}

	got, err := resolveAuthLoginInstance(strings.NewReader("prompt-instance\n"), out, "   ", existing, token)
	if err != nil {
		t.Fatalf("resolveAuthLoginInstance returned error: %v", err)
	}
	if got != "prompt-instance" {
		t.Fatalf("resolveAuthLoginInstance = %q, want prompt-instance", got)
	}
	if !strings.Contains(out.String(), "Instance ID:") {
		t.Fatalf("expected prompt output, got %q", out.String())
	}
}

func testJWTWithClaims(claims string) string {
	return fmt.Sprintf(
		"%s.%s.signature",
		base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(claims)),
	)
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
