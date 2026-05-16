package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultAPIURL is the default Oodle API URL.
const DefaultAPIURL = "https://us1.oodle.ai"

// Config holds the resolved Oodle CLI configuration.
type Config struct {
	APIKey            string `yaml:"api_key,omitempty"`
	OAuthAccessToken  string `yaml:"oauth_access_token,omitempty"`
	OAuthRefreshToken string `yaml:"oauth_refresh_token,omitempty"`
	OAuthTokenExpiry  string `yaml:"oauth_token_expiry,omitempty"`
	OAuthClientID     string `yaml:"oauth_client_id,omitempty"`
	OAuthAuthServer   string `yaml:"oauth_auth_server,omitempty"`
	Instance          string `yaml:"instance"`
	APIURL            string `yaml:"api_url"`
}

// LoadConfig resolves configuration with the precedence:
//
//	CLI flags > env vars > config file > defaults.
//
// Empty strings in flag arguments are treated as "not set".
func LoadConfig(flagAPIKey, flagInstance, flagAPIURL string) (*Config, error) {
	cfg := &Config{}

	// Start from config file (lowest precedence above defaults).
	fileCfg, err := loadConfigFile()
	if err != nil {
		return nil, err
	}
	if fileCfg != nil {
		cfg.APIKey = fileCfg.APIKey
		cfg.OAuthAccessToken = fileCfg.OAuthAccessToken
		cfg.OAuthRefreshToken = fileCfg.OAuthRefreshToken
		cfg.OAuthTokenExpiry = fileCfg.OAuthTokenExpiry
		cfg.OAuthClientID = fileCfg.OAuthClientID
		cfg.OAuthAuthServer = fileCfg.OAuthAuthServer
		cfg.Instance = fileCfg.Instance
		cfg.APIURL = fileCfg.APIURL
	}

	// Env vars override file.
	if v := os.Getenv("OODLE_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("OODLE_OAUTH_ACCESS_TOKEN"); v != "" {
		cfg.OAuthAccessToken = v
	}
	if v := os.Getenv("OODLE_OAUTH_REFRESH_TOKEN"); v != "" {
		cfg.OAuthRefreshToken = v
	}
	if v := os.Getenv("OODLE_INSTANCE"); v != "" {
		cfg.Instance = v
	}
	if url := resolveAPIURLEnv(); url != "" {
		cfg.APIURL = url
	}

	// Flags override env vars.
	if flagAPIKey != "" {
		cfg.APIKey = flagAPIKey
	}
	if flagInstance != "" {
		cfg.Instance = flagInstance
	}
	if flagAPIURL != "" {
		cfg.APIURL = flagAPIURL
	}

	// Defaults.
	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}

	if cfg.APIKey == "" && cfg.OAuthAccessToken == "" {
		return nil, fmt.Errorf("No authentication configured. Set OODLE_API_KEY or OODLE_OAUTH_ACCESS_TOKEN, use --api-key flag, or run 'oodle configure'/'oodle auth login'")
	}
	if cfg.Instance == "" {
		return nil, fmt.Errorf("No instance configured. Set OODLE_INSTANCE environment variable, use --instance flag, or run 'oodle configure'")
	}

	return cfg, nil
}

// ResolveAPIURL returns the Oodle API URL from environment variables
// (OODLE_DEPLOYMENT > OODLE_API_URL > OODLE_URL), the config file, or the
// default URL. This is useful for commands that need an API URL without full
// config resolution (e.g. unauthenticated endpoints).
func ResolveAPIURL() string {
	if url := resolveAPIURLEnv(); url != "" {
		return url
	}
	if fileCfg, err := loadConfigFile(); err == nil && fileCfg != nil && fileCfg.APIURL != "" {
		return strings.TrimRight(fileCfg.APIURL, "/")
	}
	return DefaultAPIURL
}

func resolveAPIURLEnv() string {
	if v := os.Getenv("OODLE_DEPLOYMENT"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := os.Getenv("OODLE_API_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := os.Getenv("OODLE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return ""
}

// OAuthExpiryTime parses OAuthTokenExpiry when set.
func (c *Config) OAuthExpiryTime() (time.Time, bool) {
	if c == nil || strings.TrimSpace(c.OAuthTokenExpiry) == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, c.OAuthTokenExpiry)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ConfigPath returns the path to the configuration file. The path can be
// overridden by the OODLE_CONFIG environment variable.
func ConfigPath() (string, error) {
	if p := os.Getenv("OODLE_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".oodle", "config.yaml"), nil
}

// loadConfigFile reads the config file if it exists. Returns (nil, nil) when
// the file does not exist.
func loadConfigFile() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes the config to the config file path, creating parent directories
// (mode 0700) and writing the file with mode 0600.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory %s: %w", dir, err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config file %s: %w", path, err)
	}
	return nil
}
