package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultAPIURL is the default Oodle API URL.
const DefaultAPIURL = "https://us1.oodle.ai"

// Config holds the resolved Oodle CLI configuration.
type Config struct {
	APIKey   string `yaml:"api_key"`
	Instance string `yaml:"instance"`
	APIURL   string `yaml:"api_url"`
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
		cfg.Instance = fileCfg.Instance
		cfg.APIURL = fileCfg.APIURL
	}

	// Env vars override file.
	if v := os.Getenv("OODLE_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("OODLE_INSTANCE"); v != "" {
		cfg.Instance = v
	}
	// OODLE_DEPLOYMENT takes precedence over OODLE_API_URL (alias).
	if v := os.Getenv("OODLE_API_URL"); v != "" {
		cfg.APIURL = v
	}
	if v := os.Getenv("OODLE_DEPLOYMENT"); v != "" {
		cfg.APIURL = v
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

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("No API key configured. Set OODLE_API_KEY environment variable, use --api-key flag, or run 'oodle configure'")
	}
	if cfg.Instance == "" {
		return nil, fmt.Errorf("No instance configured. Set OODLE_INSTANCE environment variable, use --instance flag, or run 'oodle configure'")
	}

	return cfg, nil
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
