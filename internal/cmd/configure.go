package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/config"
)

func newConfigureCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure the Oodle CLI",
		Long: `Save Oodle CLI credentials to ~/.oodle/config.yaml.

In non-interactive mode (when --api-key, --instance, and --api-url are all
provided as flags), the configuration is written without prompting.

When run in a TTY without all flags provided, you will be prompted for any
missing values.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigure(cmd, flags)
		},
	}
	return cmd
}

func runConfigure(cmd *cobra.Command, flags *rootFlags) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	// Start with values from flags; fall back to env vars; then to existing
	// config file values.
	apiKey := firstNonEmpty(flags.apiKey, os.Getenv("OODLE_API_KEY"))
	instance := firstNonEmpty(flags.instance, os.Getenv("OODLE_INSTANCE"))
	apiURL := firstNonEmpty(flags.apiURL, os.Getenv("OODLE_DEPLOYMENT"), os.Getenv("OODLE_API_URL"))

	// Check for existing config to use as a fallback.
	if existing, err := loadExistingConfig(); err == nil && existing != nil {
		if apiKey == "" {
			apiKey = existing.APIKey
		}
		if instance == "" {
			instance = existing.Instance
		}
		if apiURL == "" {
			apiURL = existing.APIURL
		}
	}

	allFlagsProvided := flags.apiKey != "" && flags.instance != "" && flags.apiURL != ""
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))

	if !allFlagsProvided && isTTY {
		var err error
		apiKey, instance, apiURL, err = promptForConfig(cmd.InOrStdin(), out, apiKey, instance, apiURL)
		if err != nil {
			return err
		}
	}

	if apiKey == "" {
		return fmt.Errorf("API key is required (use --api-key or set OODLE_API_KEY)")
	}
	if instance == "" {
		return fmt.Errorf("instance is required (use --instance or set OODLE_INSTANCE)")
	}
	if apiURL == "" {
		apiURL = config.DefaultAPIURL
	}

	cfg := &config.Config{APIKey: apiKey, Instance: instance, APIURL: apiURL}
	if err := cfg.Save(); err != nil {
		return err
	}
	path, _ := config.ConfigPath()
	fmt.Fprintf(out, "Configuration saved to %s\n", path)

	// Validate by calling the API.
	if err := validateConfig(cmd.Context(), cfg, flags.retries); err != nil {
		fmt.Fprintf(errOut, "Warning: configuration saved but validation failed: %v\n", err)
		return nil
	}
	fmt.Fprintln(out, "Validated against Oodle API: OK")
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// loadExistingConfig reads ~/.oodle/config.yaml without applying env or flag
// overrides. Returns (nil, nil) when the file does not exist.
func loadExistingConfig() (*config.Config, error) {
	path, err := config.ConfigPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config.Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func promptForConfig(in io.Reader, out io.Writer, currentKey, currentInstance, currentURL string) (string, string, string, error) {
	r := bufio.NewReader(in)

	// API URL.
	defaultURL := currentURL
	if defaultURL == "" {
		defaultURL = config.DefaultAPIURL
	}
	fmt.Fprintf(out, "Oodle API URL [%s]: ", defaultURL)
	urlIn, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", "", "", err
	}
	urlIn = strings.TrimSpace(urlIn)
	if urlIn == "" {
		urlIn = defaultURL
	}

	// Instance.
	if currentInstance != "" {
		fmt.Fprintf(out, "Instance ID [%s]: ", currentInstance)
	} else {
		fmt.Fprint(out, "Instance ID: ")
	}
	instIn, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", "", "", err
	}
	instIn = strings.TrimSpace(instIn)
	if instIn == "" {
		instIn = currentInstance
	}

	// API key – read without echo if stdin is a TTY.
	keyIn := currentKey
	if term.IsTerminal(int(os.Stdin.Fd())) {
		prompt := "API key"
		if currentKey != "" {
			prompt += " (press Enter to keep existing)"
		}
		fmt.Fprintf(out, "%s: ", prompt)
		secret, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(out)
		if err != nil {
			return "", "", "", fmt.Errorf("reading API key: %w", err)
		}
		entered := strings.TrimSpace(string(secret))
		if entered != "" {
			keyIn = entered
		}
	} else {
		fmt.Fprint(out, "API key: ")
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", "", "", err
		}
		entered := strings.TrimSpace(line)
		if entered != "" {
			keyIn = entered
		}
	}

	return keyIn, instIn, urlIn, nil
}

// validateConfig calls a low-cost API endpoint to confirm credentials work.
func validateConfig(ctx context.Context, cfg *config.Config, retries int) error {
	c, err := api.NewClient(cfg, retries)
	if err != nil {
		return err
	}
	resp, err := c.Inner.ListApiKeysWithResponse(ctx, cfg.Instance)
	if err != nil {
		return err
	}
	if resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
		return nil
	}
	return api.CheckResponse(resp.HTTPResponse, resp.Body)
}


