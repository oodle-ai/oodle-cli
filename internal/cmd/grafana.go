package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/grafana"
)

// newGrafanaCmd returns the `oodle grafana` command tree, which migrates a
// Grafana instance's dashboards, folders, data sources and alerts into Oodle.
func newGrafanaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grafana",
		Short: "Migrate Grafana dashboards, folders, data sources and alerts into Oodle",
	}
	cmd.AddCommand(newGrafanaMigrateCmd())
	return cmd
}

func newGrafanaMigrateCmd() *cobra.Command {
	var (
		grafanaURL   string
		grafanaToken string
		includeTags  []string
		overwrite    bool
		runImport    bool
	)

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Export assets from a Grafana instance into Oodle for review and import",
		Long: `Export a Grafana instance into Oodle.

The export runs entirely from your machine, so it works even when Grafana is
only reachable locally (e.g. behind a VPN). It exports dashboards, folders,
data sources and alert rules, uploads them to Oodle, and prints a link where
you choose what to import.

Pass --import to skip the review and import everything straight away.

Oodle credentials are taken from your CLI configuration ('oodle configure' or
the OODLE_API_KEY / OODLE_INSTANCE / OODLE_DEPLOYMENT environment variables).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			cfg := c.Config
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			grafanaURL = strings.TrimRight(grafanaURL, "/")

			// 1. Export assets from Grafana to a local tarball.
			exporter := grafana.NewExporter(grafana.ExportConfig{
				GrafanaURL:   grafanaURL,
				GrafanaToken: grafanaToken,
				IncludeTags:  includeTags,
			})
			fmt.Fprintf(out, "Exporting assets from %s ...\n", grafanaURL)
			result, err := exporter.ExportWithProgress(
				ctx,
				func(p grafana.ExportProgress) {
					if p.Total > 0 {
						fmt.Fprintf(
							out,
							"  %s: %d/%d\n",
							p.CurrentStep,
							p.Completed,
							p.Total,
						)
					}
				},
			)
			if err != nil {
				return fmt.Errorf("exporting from Grafana: %w", err)
			}
			defer exporter.Cleanup(result)

			// 2. Upload the tarball to Oodle object storage.
			oc := grafana.NewOodleClient(c)
			fmt.Fprintln(out, "Uploading export to Oodle ...")
			uploadedPath, err := oc.UploadTar(ctx, result.TarFilePath)
			if err != nil {
				return fmt.Errorf("uploading export: %w", err)
			}

			// 3. Record the migration so it is reviewable in the Oodle UI.
			migrationID, err := oc.Register(ctx, uploadedPath, grafanaURL)
			if err != nil {
				return fmt.Errorf("registering migration: %w", err)
			}
			fmt.Fprintf(out, "Registered migration %s\n", migrationID)

			// Deep-links the Grafana integration tile straight to this
			// migration, where the user picks what to import.
			reviewURL := fmt.Sprintf(
				"%s/integrations?search=grafa&integration=GRAFANA&migration=%s",
				strings.TrimRight(cfg.APIURL, "/"),
				migrationID,
			)

			if !runImport {
				fmt.Fprintf(
					out,
					"\nUpload complete. Choose what to import in Oodle:\n  %s\n",
					reviewURL,
				)
				return nil
			}

			// 4. Import into Oodle.
			fmt.Fprintln(out, "Importing into Oodle ...")
			status, err := oc.Import(ctx, migrationID, overwrite)
			if err != nil {
				return fmt.Errorf("importing migration: %w", err)
			}

			printMigrationSummary(out, status)
			fmt.Fprintf(out, "\nDone. View results in Oodle:\n  %s\n", reviewURL)
			return nil
		},
	}

	cmd.Flags().StringVar(&grafanaURL, "grafana-url", "", "Grafana base URL (required)")
	cmd.Flags().StringVar(&grafanaToken, "grafana-token", "", "Grafana service account token (required)")
	cmd.Flags().StringSliceVar(&includeTags, "include-tags", nil, "Only migrate dashboards with these tags (comma-separated); empty migrates all")
	cmd.Flags().BoolVar(&overwrite, "overwrite", true, "Overwrite existing dashboards and data sources")
	cmd.Flags().BoolVar(&runImport, "import", false, "Import everything immediately instead of choosing what to import in the Oodle UI")
	_ = cmd.MarkFlagRequired("grafana-url")
	_ = cmd.MarkFlagRequired("grafana-token")

	return cmd
}

// migrationCategory summarizes the outcome of one asset type for reporting.
type migrationCategory struct {
	label string
	items []grafana.MigratedItem
}

func printMigrationSummary(out io.Writer, status *grafana.ImportStatus) {
	categories := []migrationCategory{
		{"Data sources", status.MigratedDataSources},
		{"Folders", status.MigratedFolders},
		{"Dashboards", status.MigratedDashboards},
		{"Alert rules", status.MigratedAlertRules},
	}

	fmt.Fprintln(out, "\nMigration summary:")
	for _, cat := range categories {
		if len(cat.items) == 0 {
			continue
		}
		imported, failed := 0, 0
		for _, it := range cat.items {
			switch it.State {
			case "IMPORT_FAILED":
				failed++
			default:
				// CREATED, IMPORT_SUCCEEDED, IMPORT_PARTIAL and
				// EXPORT_SUCCEEDED are all non-failures.
				imported++
			}
		}
		fmt.Fprintf(
			out,
			"  %-13s %d ok, %d failed (of %d)\n",
			cat.label+":",
			imported,
			failed,
			len(cat.items),
		)
		// Surface individual failures so the user can act on them.
		for _, it := range cat.items {
			if it.State == "IMPORT_FAILED" {
				reason := it.FailureReason
				if it.Message != "" {
					reason = it.Message
				}
				fmt.Fprintf(out, "      - %s: %s\n", it.Name, reason)
			}
		}
	}
}
