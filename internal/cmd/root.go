package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/config"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// Persistent flag values; populated by cobra during parsing.
type rootFlags struct {
	apiKey   string
	instance string
	apiURL   string
	output   string
	retries  int
	force    bool
}

// commandsSkippingConfig are commands that must run without a fully-resolved
// config (since they may exist precisely to *create* one or have no need for
// API access).
var commandsSkippingConfig = map[string]bool{
	"configure":  true,
	"version":    true,
	"help":       true,
	"completion": true,
}

// NewRootCmd builds the root cobra command tree.
func NewRootCmd() *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:   "oodle",
		Short: "Oodle CLI – manage your Oodle observability platform",
		Long: `oodle is the command-line interface for the Oodle observability platform.

Configure credentials with 'oodle configure' or via the OODLE_API_KEY,
OODLE_INSTANCE, and OODLE_DEPLOYMENT environment variables.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if shouldSkipConfig(cmd) {
				// Even commands that skip config benefit from output format
				// detection (e.g. version with -o json).
				ctx := withOutput(cmd.Context(), output.DetectFormat(flags.output))
				cmd.SetContext(ctx)
				return nil
			}
			cfg, err := config.LoadConfig(flags.apiKey, flags.instance, flags.apiURL)
			if err != nil {
				return err
			}
			c, err := api.NewClient(cfg, flags.retries)
			if err != nil {
				return fmt.Errorf("creating API client: %w", err)
			}
			ctx := cmd.Context()
			ctx = withClient(ctx, c)
			ctx = withOutput(ctx, output.DetectFormat(flags.output))
			ctx = withInstance(ctx, cfg.Instance)
			cmd.SetContext(ctx)
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&flags.apiKey, "api-key", "", "Oodle API key (overrides OODLE_API_KEY)")
	pf.StringVar(&flags.instance, "instance", "", "Oodle instance ID (overrides OODLE_INSTANCE)")
	pf.StringVar(&flags.apiURL, "api-url", "", "Oodle API URL (overrides OODLE_DEPLOYMENT/OODLE_API_URL)")
	pf.StringVarP(&flags.output, "output", "o", "", "Output format: table, json, yaml, csv (auto-detected from TTY)")
	pf.IntVar(&flags.retries, "retries", 3, "Number of retries for transient API failures")
	pf.BoolVar(&flags.force, "force", false, "Skip confirmation prompts for destructive actions")

	root.AddCommand(newConfigureCmd(flags))
	root.AddCommand(newVersionCmd())
	root.AddCommand(newMonitorsCmd())
	root.AddCommand(newNotifiersCmd())
	root.AddCommand(newNotificationPoliciesCmd())
	root.AddCommand(newMutingRulesCmd())
	root.AddCommand(newLogMetricsCmd())
	root.AddCommand(newSyntheticMonitorsCmd())
	root.AddCommand(newDashboardsCmd())
	root.AddCommand(newFoldersCmd())
	root.AddCommand(newDropRulesCmd())
	root.AddCommand(newMetricsCmd())
	root.AddCommand(newTracesCmd())
	root.AddCommand(newApiKeysCmd())
	root.AddCommand(newUsersCmd())

	return root
}

// shouldSkipConfig returns true if the command (or any ancestor) is in the
// skip list.
func shouldSkipConfig(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if commandsSkippingConfig[c.Name()] {
			return true
		}
	}
	return false
}

// forceFlag walks up the command tree to find the persistent --force flag
// value. Sub-commands can use this to honor --force without re-declaring it.
func forceFlag(cmd *cobra.Command) bool {
	v, err := cmd.Flags().GetBool("force")
	if err != nil {
		return false
	}
	return v
}

// Execute is the package entry point invoked by main.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		// Strip leading "Error: " so we don't repeat it.
		msg := strings.TrimPrefix(err.Error(), "Error: ")
		fmt.Fprintln(os.Stderr, "Error: "+msg)
		os.Exit(1)
	}
}
