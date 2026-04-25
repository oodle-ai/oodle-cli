package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build-time variables, set via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the oodle CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "oodle version %s (commit: %s, built: %s)\n", version, commit, date)
			return nil
		},
	}
}
