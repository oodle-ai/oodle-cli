package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/grafana"
	"github.com/oodle-ai/oodle-cli/internal/newrelic"
)

// newNewRelicCmd returns the `oodle newrelic` command tree.
func newNewRelicCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "newrelic",
		Short: "Export New Relic dashboards and alerts",
	}
	cmd.AddCommand(newNewRelicExportCmd())
	return cmd
}

func newNewRelicExportCmd() *cobra.Command {
	var nrURL, nrKey, outDir string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export all New Relic dashboards and alerts to a directory and .tar.gz",
		Long: `Export every dashboard and alert (policies and NRQL conditions) visible to a
New Relic user key, as JSON files in a local directory, then compress that
directory into a .tar.gz next to it.

The key is read from --newrelic-api-key or NEW_RELIC_API_KEY. No Oodle
credentials are needed.`,
		Example: `  oodle newrelic export --newrelic-api-key NRAK-XXXX
  oodle newrelic export --newrelic-api-key NRAK-XXXX --url ` + newrelic.EUURL,
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			skipConfigAnnotation: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			if nrKey == "" {
				nrKey = os.Getenv("NEW_RELIC_API_KEY")
			}

			c, err := newrelic.NewClient(nrURL, nrKey)
			if err == nil {
				var email string
				if email, err = c.Validate(ctx); err == nil {
					fmt.Fprintf(out, "Authenticated to %s as %s\n", nrURL, email)
				}
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "New Relic validation failed: %v\n\n%s\n", err, newrelic.KeyHelp)
				return fmt.Errorf("invalid New Relic URL or API key")
			}

			if outDir == "" {
				outDir = "newrelic-export-" + time.Now().Format("20060102-150405")
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}

			fmt.Fprintf(out, "Exporting into %s ...\n", outDir)
			r, err := c.Export(ctx, outDir, out)
			if err != nil {
				return err
			}

			tarPath := filepath.Clean(outDir) + ".tar.gz"
			if err := grafana.CompressDirectory(filepath.Clean(outDir), tarPath); err != nil {
				return fmt.Errorf("compressing export: %w", err)
			}

			fmt.Fprintf(out, "\nExport report:\n")
			fmt.Fprintf(out, "  Dashboards exported:       %d\n", r.Dashboards)
			fmt.Fprintf(out, "  Alert policies exported:   %d\n", r.Policies)
			fmt.Fprintf(out, "  Alert conditions exported: %d (NRQL)\n", r.Conditions)
			fmt.Fprintf(out, "  Accounts scanned:          %d\n", r.Accounts)
			if len(r.AccountErrs) > 0 {
				fmt.Fprintf(out, "  Accounts with alert errors: %d\n    %s\n", len(r.AccountErrs), strings.Join(r.AccountErrs, "\n    "))
			}
			fmt.Fprintf(out, "  Directory: %s\n  Archive:   %s\n", outDir, tarPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&nrKey, "newrelic-api-key", "", "New Relic user API key, NRAK-... (or NEW_RELIC_API_KEY)")
	cmd.Flags().StringVar(&nrURL, "url", newrelic.DefaultURL, "NerdGraph URL; EU accounts use "+newrelic.EUURL)
	cmd.Flags().StringVar(&outDir, "out-dir", "", "Output directory (default newrelic-export-<timestamp>)")
	return cmd
}
