package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// anomaliesWindow is the time on each side of --time that the anomaly scan
// reads.
const anomaliesWindow = time.Hour

// anomaliesDefaultCompare is the default comparison offset: the same time
// one day earlier.
const anomaliesDefaultCompare = "24h"

// anomaliesRequest is the body of the anomaly scan route.
type anomaliesRequest struct {
	Instance             string  `json:"instance"`
	ClusterName          string  `json:"clusterName"`
	StartTimeEpochSec    int64   `json:"startTimeEpochSec"`
	EndTimeEpochSec      int64   `json:"endTimeEpochSec"`
	Namespace            string  `json:"namespace"`
	Service              string  `json:"service"`
	Pod                  string  `json:"pod"`
	CompareWithOffsetSec []int64 `json:"compareWithOffsetSec"`
}

// anomaliesResponse holds the fields of the response that the table shows.
// JSON output prints the body without change.
type anomaliesResponse struct {
	Anomalies []struct {
		Title         string `json:"title"`
		Type          int    `json:"type"`
		Category      int    `json:"category"`
		Description   string `json:"description"`
		AnomalySeries []struct {
			Occurrence string `json:"occurrence"`
		} `json:"anomaly_series"`
	} `json:"anomalies"`
}

type anomalyRow struct {
	Title       string
	Category    string
	Type        string
	Series      int
	NewSeries   int
	Description string
}

var anomalyColumns = []output.Column{
	{Header: "TITLE", Field: "Title"},
	{Header: "CATEGORY", Field: "Category"},
	{Header: "TYPE", Field: "Type"},
	{Header: "SERIES", Field: "Series"},
	{Header: "NEW SERIES", Field: "NewSeries"},
	{Header: "DESCRIPTION", Field: "Description"},
}

// anomalyTypeName and anomalyCategoryName give the names of the enum values
// in the response.
func anomalyTypeName(v int) string {
	switch v {
	case 1:
		return "large_change"
	case 2:
		return "new"
	}
	return "unknown"
}

func anomalyCategoryName(v int) string {
	switch v {
	case 1:
		return "infrastructure"
	case 2:
		return "application"
	}
	return "unknown"
}

func anomalyRows(resp anomaliesResponse) []anomalyRow {
	rows := make([]anomalyRow, 0, len(resp.Anomalies))
	for _, a := range resp.Anomalies {
		newSeries := 0
		for _, s := range a.AnomalySeries {
			if s.Occurrence == "new" {
				newSeries++
			}
		}
		rows = append(rows, anomalyRow{
			Title:       a.Title,
			Category:    anomalyCategoryName(a.Category),
			Type:        anomalyTypeName(a.Type),
			Series:      len(a.AnomalySeries),
			NewSeries:   newSeries,
			Description: a.Description,
		})
	}
	return rows
}

// parseCompareOffsets parses a list of positive durations such as 24h or 7d
// and returns them in seconds.
func parseCompareOffsets(values []string) ([]int64, error) {
	out := make([]int64, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		d, err := parseRelativeDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid --compare %q: use a positive duration such as 24h or 7d", v)
		}
		out = append(out, int64(d/time.Second))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--compare needs at least one duration, such as 24h")
	}
	return out, nil
}

// newAnomaliesCmd returns the `oodle anomalies` command tree.
func newAnomaliesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "anomalies",
		Aliases: []string{"anomaly"},
		Short:   "Find metric anomalies around a point in time",
	}
	cmd.AddCommand(newAnomaliesListCmd())
	return cmd
}

func newAnomaliesListCmd() *cobra.Command {
	var (
		timeStr   string
		cluster   string
		namespace string
		service   string
		pod       string
		compare   []string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List metric anomalies from one hour before to one hour after a time",
		Long: `List metric anomalies from one hour before to one hour after --time.

The scan checks a set of service and infrastructure metrics, for example
error rates and restarts. It compares their values with the values at each
--compare offset. The default offset is one day. Add 7d to compare
with the same time one week earlier.

Each row is one anomaly. SERIES is the number of series that cross the
anomaly threshold now. NEW SERIES is the number of those series whose value
is very different from their value at each --compare offset. The other
series had a similar value before, so they are less likely to be the cause
of a new problem. Use -o json to get each series with its labels and values,
and the PromQL query of the anomaly.`,
		Example: `  # Anomalies in the last hour, compared with one day earlier
  oodle anomalies list

  # Anomalies around an incident in one namespace and service
  oodle anomalies list --time 2026-01-13T10:30:00Z --namespace production --service checkout

  # Compare with one day and one week earlier
  oodle anomalies list --compare 24h,7d -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			offsets, err := parseCompareOffsets(compare)
			if err != nil {
				return err
			}
			t, err := parseTimeFlagSec(timeStr)
			if err != nil {
				return fmt.Errorf("--time: %w", err)
			}
			win := int64(anomaliesWindow / time.Second)
			req := anomaliesRequest{
				Instance:             getInstance(cmd),
				ClusterName:          cluster,
				StartTimeEpochSec:    t - win,
				EndTimeEpochSec:      t + win,
				Namespace:            namespace,
				Service:              service,
				Pod:                  pod,
				CompareWithOffsetSec: offsets,
			}
			body, err := instancePostJSON(cmd, "jarvis/anomalies", req)
			if err != nil {
				return err
			}
			var resp anomaliesResponse
			decodeErr := json.Unmarshal(body, &resp)
			if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
				return output.Print(cmd.OutOrStdout(), format, anomalyRows(resp), anomalyColumns)
			}); err != nil {
				return err
			}
			if decodeErr == nil && len(resp.Anomalies) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"No anomalies between %s and %s. Only this window is read; use --time to move it, or remove the filters.\n",
					time.Unix(req.StartTimeEpochSec, 0).UTC().Format(time.RFC3339),
					time.Unix(req.EndTimeEpochSec, 0).UTC().Format(time.RFC3339))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&timeStr, "time", defaultEndValue, "Center of the two-hour window (relative like -3h, 'now', RFC3339, or epoch s/ms/µs/ns)")
	cmd.Flags().StringVar(&cluster, "cluster", "", "Only read this cluster")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Only read this namespace")
	cmd.Flags().StringVar(&service, "service", "", "Only read this service or container")
	cmd.Flags().StringVar(&pod, "pod", "", "Only read this pod")
	cmd.Flags().StringSliceVar(&compare, "compare", []string{anomaliesDefaultCompare}, "Offsets to compare with, such as 24h or 7d (comma-separated or repeated)")
	return cmd
}
