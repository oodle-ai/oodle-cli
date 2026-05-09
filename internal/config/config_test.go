package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withCleanEnv unsets all OODLE_* env vars for the duration of the test and
// restores the previous values afterwards.
func withCleanEnv(t *testing.T) {
	t.Helper()
	keys := []string{"OODLE_API_KEY", "OODLE_OAUTH_ACCESS_TOKEN", "OODLE_INSTANCE", "OODLE_URL", "OODLE_API_URL", "OODLE_DEPLOYMENT", "OODLE_CONFIG"}
	for _, k := range keys {
		old, ok := os.LookupEnv(k)
		os.Unsetenv(k)
		if ok {
			t.Cleanup(func() { os.Setenv(k, old) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
	}
	// Point HOME at a tmp dir so a real ~/.oodle/config.yaml doesn't leak in.
	tmp := t.TempDir()
	oldHome, hadHome := os.LookupEnv("HOME")
	os.Setenv("HOME", tmp)
	if hadHome {
		t.Cleanup(func() { os.Setenv("HOME", oldHome) })
	} else {
		t.Cleanup(func() { os.Unsetenv("HOME") })
	}
}

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadConfig_FlagsOverrideEnv(t *testing.T) {
	withCleanEnv(t)
	os.Setenv("OODLE_API_KEY", "env-key")
	os.Setenv("OODLE_INSTANCE", "env-instance")
	os.Setenv("OODLE_API_URL", "https://env.example.com")

	cfg, err := LoadConfig("flag-key", "flag-instance", "https://flag.example.com")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIKey != "flag-key" {
		t.Errorf("APIKey = %q, want flag-key", cfg.APIKey)
	}
	if cfg.Instance != "flag-instance" {
		t.Errorf("Instance = %q, want flag-instance", cfg.Instance)
	}
	if cfg.APIURL != "https://flag.example.com" {
		t.Errorf("APIURL = %q, want https://flag.example.com", cfg.APIURL)
	}
}

func TestLoadConfig_EnvOverridesFile(t *testing.T) {
	withCleanEnv(t)
	path := writeConfigFile(t, "api_key: file-key\ninstance: file-instance\napi_url: https://file.example.com\n")
	os.Setenv("OODLE_CONFIG", path)
	os.Setenv("OODLE_API_KEY", "env-key")
	os.Setenv("OODLE_INSTANCE", "env-instance")
	os.Setenv("OODLE_API_URL", "https://env.example.com")

	cfg, err := LoadConfig("", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIKey != "env-key" || cfg.Instance != "env-instance" || cfg.APIURL != "https://env.example.com" {
		t.Errorf("unexpected cfg: %+v", cfg)
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	withCleanEnv(t)
	path := writeConfigFile(t, "api_key: file-key\ninstance: file-instance\napi_url: https://file.example.com\n")
	os.Setenv("OODLE_CONFIG", path)

	cfg, err := LoadConfig("", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIKey != "file-key" || cfg.Instance != "file-instance" || cfg.APIURL != "https://file.example.com" {
		t.Errorf("unexpected cfg: %+v", cfg)
	}
}

func TestLoadConfig_FromFileWithOAuthToken(t *testing.T) {
	withCleanEnv(t)
	path := writeConfigFile(t, "oauth_access_token: oauth-token\noauth_refresh_token: refresh-token\ninstance: file-instance\napi_url: https://file.example.com\n")
	os.Setenv("OODLE_CONFIG", path)

	cfg, err := LoadConfig("", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.OAuthAccessToken != "oauth-token" {
		t.Fatalf("OAuthAccessToken = %q, want oauth-token", cfg.OAuthAccessToken)
	}
	if cfg.OAuthRefreshToken != "refresh-token" {
		t.Fatalf("OAuthRefreshToken = %q, want refresh-token", cfg.OAuthRefreshToken)
	}
	if cfg.Instance != "file-instance" || cfg.APIURL != "https://file.example.com" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestLoadConfig_MissingAuthentication(t *testing.T) {
	withCleanEnv(t)
	os.Setenv("OODLE_INSTANCE", "x")
	_, err := LoadConfig("", "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "No authentication configured") {
		t.Errorf("error %q does not mention missing authentication", err.Error())
	}
	if !strings.Contains(err.Error(), "OODLE_API_KEY") {
		t.Errorf("error %q does not mention OODLE_API_KEY", err.Error())
	}
}

func TestLoadConfig_MissingInstance(t *testing.T) {
	withCleanEnv(t)
	os.Setenv("OODLE_API_KEY", "k")
	_, err := LoadConfig("", "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "No instance configured") {
		t.Errorf("error %q does not mention missing instance", err.Error())
	}
}

func TestLoadConfig_DefaultAPIURL(t *testing.T) {
	withCleanEnv(t)
	cfg, err := LoadConfig("k", "i", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIURL != DefaultAPIURL {
		t.Errorf("APIURL = %q, want %q", cfg.APIURL, DefaultAPIURL)
	}
}

func TestLoadConfig_DeploymentEnvVar(t *testing.T) {
	withCleanEnv(t)
	os.Setenv("OODLE_API_KEY", "k")
	os.Setenv("OODLE_INSTANCE", "i")
	os.Setenv("OODLE_DEPLOYMENT", "https://deploy.example.com")
	cfg, err := LoadConfig("", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIURL != "https://deploy.example.com" {
		t.Errorf("APIURL = %q, want from OODLE_DEPLOYMENT", cfg.APIURL)
	}
}

func TestLoadConfig_APIURLEnvVar(t *testing.T) {
	withCleanEnv(t)
	os.Setenv("OODLE_API_KEY", "k")
	os.Setenv("OODLE_INSTANCE", "i")
	os.Setenv("OODLE_API_URL", "https://apiurl.example.com")
	cfg, err := LoadConfig("", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIURL != "https://apiurl.example.com" {
		t.Errorf("APIURL = %q, want from OODLE_API_URL", cfg.APIURL)
	}
}

func TestLoadConfig_DeploymentBeatsAPIURL(t *testing.T) {
	withCleanEnv(t)
	os.Setenv("OODLE_API_KEY", "k")
	os.Setenv("OODLE_INSTANCE", "i")
	os.Setenv("OODLE_API_URL", "https://apiurl.example.com")
	os.Setenv("OODLE_DEPLOYMENT", "https://deploy.example.com")
	cfg, err := LoadConfig("", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIURL != "https://deploy.example.com" {
		t.Errorf("APIURL = %q, OODLE_DEPLOYMENT should win", cfg.APIURL)
	}
}

func TestLoadConfig_OodleConfigOverride(t *testing.T) {
	withCleanEnv(t)
	path := writeConfigFile(t, "api_key: special-key\ninstance: special-instance\n")
	os.Setenv("OODLE_CONFIG", path)
	cfg, err := LoadConfig("", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIKey != "special-key" || cfg.Instance != "special-instance" {
		t.Errorf("unexpected cfg: %+v", cfg)
	}
	got, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if got != path {
		t.Errorf("ConfigPath = %q, want %q", got, path)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	withCleanEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "config.yaml")
	os.Setenv("OODLE_CONFIG", path)

	c := &Config{APIKey: "k", Instance: "i", APIURL: "https://x.example.com"}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}
	cfg, err := LoadConfig("", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if *cfg != *c {
		t.Errorf("round trip mismatch: got %+v want %+v", cfg, c)
	}
}

func TestResolveAPIURL_Default(t *testing.T) {
	withCleanEnv(t)
	got := ResolveAPIURL()
	if got != DefaultAPIURL {
		t.Errorf("ResolveAPIURL() = %q, want %q", got, DefaultAPIURL)
	}
}

func TestResolveAPIURL_EnvVars(t *testing.T) {
	withCleanEnv(t)

	// OODLE_URL is lowest priority among the URL env vars.
	os.Setenv("OODLE_URL", "https://url.example.com")
	if got := ResolveAPIURL(); got != "https://url.example.com" {
		t.Errorf("OODLE_URL: got %q", got)
	}

	// OODLE_API_URL overrides OODLE_URL.
	os.Setenv("OODLE_API_URL", "https://apiurl.example.com")
	if got := ResolveAPIURL(); got != "https://apiurl.example.com" {
		t.Errorf("OODLE_API_URL: got %q", got)
	}

	// OODLE_DEPLOYMENT overrides OODLE_API_URL.
	os.Setenv("OODLE_DEPLOYMENT", "https://deploy.example.com")
	if got := ResolveAPIURL(); got != "https://deploy.example.com" {
		t.Errorf("OODLE_DEPLOYMENT: got %q", got)
	}

	// Verify OODLE_DEPLOYMENT wins even without the others.
	os.Unsetenv("OODLE_URL")
	os.Unsetenv("OODLE_API_URL")
	if got := ResolveAPIURL(); got != "https://deploy.example.com" {
		t.Errorf("OODLE_DEPLOYMENT alone: got %q", got)
	}
}

func TestResolveAPIURL_TrimsTrailingSlash(t *testing.T) {
	withCleanEnv(t)
	os.Setenv("OODLE_API_URL", "https://example.com/")
	if got := ResolveAPIURL(); got != "https://example.com" {
		t.Errorf("ResolveAPIURL() = %q, want trailing slash trimmed", got)
	}
}

func TestOAuthExpiryTime(t *testing.T) {
	now := "2026-05-02T15:04:05Z"
	cfg := &Config{OAuthTokenExpiry: now}
	parsed, ok := cfg.OAuthExpiryTime()
	if !ok {
		t.Fatal("OAuthExpiryTime should parse valid RFC3339 value")
	}
	if parsed.UTC().Format(time.RFC3339) != now {
		t.Fatalf("OAuthExpiryTime = %s, want %s", parsed.UTC().Format(time.RFC3339), now)
	}

	cfg.OAuthTokenExpiry = "not-a-time"
	if _, ok := cfg.OAuthExpiryTime(); ok {
		t.Fatal("OAuthExpiryTime should return ok=false for invalid value")
	}
}
