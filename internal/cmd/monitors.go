package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// monitorListColumns describes the table layout for `monitors list`.
var monitorListColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "QUERY", Field: "PromqlQuery"},
	{Header: "INTERVAL", Field: "Interval"},
}

// monitorTriggerColumns describes the table layout for `monitors triggers`.
var monitorTriggerColumns = []output.Column{
	{Header: "MONITOR ID", Field: "MonitorID"},
	{Header: "SEVERITY", Field: "Severity"},
	{Header: "STARTED", Field: "StartsAt"},
	{Header: "ENDED", Field: "EndsAt"},
}

// newMonitorsCmd builds the `oodle monitors` command tree.
func newMonitorsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "monitors",
		Aliases: []string{"monitor", "mon"},
		Short:   "Manage monitors",
	}

	cmd.AddCommand(newMonitorsListCmd())
	cmd.AddCommand(newMonitorsGetCmd())
	cmd.AddCommand(newMonitorsCreateCmd())
	cmd.AddCommand(newMonitorsUpdateCmd())
	cmd.AddCommand(newMonitorsDeleteCmd())
	cmd.AddCommand(newMonitorsStateCmd())
	cmd.AddCommand(newMonitorsTriggersCmd())

	cmd.AddCommand(newMonitorsTemplateFilesCmd())

	return cmd
}

func newMonitorsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List monitors",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListMonitorsWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, monitorListColumns)
		},
	}
}

func newMonitorsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a monitor by ID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.GetMonitorsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			if format == output.FormatTable || format == output.FormatCSV {
				return output.Print(cmd.OutOrStdout(), format, []client.Monitor{*resp.JSON200}, monitorListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, monitorListColumns)
		},
	}
}

func newMonitorsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a monitor from a JSON/YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.CreateMonitorsJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}

			resp, err := c.Inner.CreateMonitorsWithResponse(cmd.Context(), instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			if format == output.FormatTable || format == output.FormatCSV {
				return output.Print(cmd.OutOrStdout(), format, []client.Monitor{*resp.JSON200}, monitorListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, monitorListColumns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with monitor definition")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newMonitorsUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a monitor from a JSON/YAML file",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.UpdateMonitorsByIdJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}

			resp, err := c.Inner.UpdateMonitorsByIdWithResponse(cmd.Context(), instance, args[0], body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			if format == output.FormatTable || format == output.FormatCSV {
				return output.Print(cmd.OutOrStdout(), format, []client.Monitor{*resp.JSON200}, monitorListColumns)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, monitorListColumns)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with monitor definition")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newMonitorsDeleteCmd() *cobra.Command {
	var idsFlag string
	cmd := &cobra.Command{
		Use:   "delete [<id>]",
		Short: "Delete a monitor (single ID) or multiple monitors via --ids",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate inputs up front so we can produce clear errors without
			// touching the API client (which may not be initialized in tests).
			bulkMode := strings.TrimSpace(idsFlag) != ""
			if bulkMode && len(args) > 0 {
				return fmt.Errorf("provide either a positional <id> or --ids, not both")
			}
			if !bulkMode && len(args) != 1 {
				return fmt.Errorf("either a positional <id> or --ids is required")
			}

			c := getClient(cmd)
			instance := getInstance(cmd)

			// Bulk delete via --ids.
			if bulkMode {
				parts := strings.Split(idsFlag, ",")
				ids := make([]string, 0, len(parts))
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						ids = append(ids, p)
					}
				}
				if len(ids) == 0 {
					return fmt.Errorf("--ids must contain at least one id")
				}
				prompt := fmt.Sprintf("Delete %d monitors (%s)?", len(ids), strings.Join(ids, ", "))
				if !confirmAction(prompt, forceFlag(cmd)) {
					return fmt.Errorf("aborted")
				}
				body := client.DeleteMonitorsJSONRequestBody{Ids: &ids}
				resp, err := c.Inner.DeleteMonitorsWithResponse(cmd.Context(), instance, body)
				if err != nil {
					return fmt.Errorf("API request failed: %w", err)
				}
				if resp.StatusCode() >= 300 {
					return api.CheckResponse(resp.HTTPResponse, resp.Body)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted %d monitors\n", len(ids))
				return nil
			}

			// Single delete via positional arg.
			id := args[0]
			if !confirmAction("Delete monitor "+id+"?", forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteMonitorsByIdWithResponse(cmd.Context(), instance, id)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted monitor %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&idsFlag, "ids", "", "Comma-separated list of monitor IDs to delete (bulk)")
	return cmd
}

func newMonitorsStateCmd() *cobra.Command {
	var (
		historyRange string
		startStr     string
		endStr       string
	)
	cmd := &cobra.Command{
		Use:   "state <id>",
		Short: "Get a monitor's state",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			params := &client.GetMonitorStateByIdParams{}
			switch {
			case cmd.Flags().Changed("start") || cmd.Flags().Changed("end"):
				// Compose the range from the CLI-wide --start/--end
				// syntax. The endpoint requires both bounds, so fill
				// the missing side from the defaults.
				if startStr == "" {
					startStr = defaultHistoryStartOffset
				}
				if endStr == "" {
					endStr = defaultEndValue
				}
				start, err := parseTimeFlagSec(startStr)
				if err != nil {
					return fmt.Errorf("--start: %w", err)
				}
				end, err := parseTimeFlagSec(endStr)
				if err != nil {
					return fmt.Errorf("--end: %w", err)
				}
				if start > end {
					return fmt.Errorf("--start (%d) is after --end (%d)", start, end)
				}
				hr := fmt.Sprintf("%d-%d", start, end)
				params.HistoryRange = &hr
			case strings.TrimSpace(historyRange) != "":
				hr, err := parseHistoryRange(historyRange)
				if err != nil {
					return err
				}
				params.HistoryRange = &hr
			}

			resp, err := c.Inner.GetMonitorStateByIdWithResponse(cmd.Context(), instance, args[0], params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
	cmd.Flags().StringVar(&historyRange, "history-range", "", "Raw time range for monitor history, \"<start>-<end>\" in epoch seconds (e.g. 1705036708-1705123108). Prefer --start/--end")
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the monitor history range (epoch seconds, 'now', or relative like -7d). Defaults to "+defaultHistoryStartOffset+" if only --end is given")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the monitor history range (epoch seconds, 'now', or relative like -1h). Defaults to "+defaultEndValue+" if only --start is given")
	cmd.MarkFlagsMutuallyExclusive("history-range", "start")
	cmd.MarkFlagsMutuallyExclusive("history-range", "end")
	return cmd
}

// parseHistoryRange validates the raw --history-range form, "<start>-<end>"
// in epoch seconds (e.g. 1705036708-1705123108), and returns it unchanged.
// Validating locally means a typo surfaces here rather than as an opaque
// server-side error.
func parseHistoryRange(value string) (string, error) {
	v := strings.TrimSpace(value)
	startStr, endStr, ok := strings.Cut(v, "-")
	if !ok {
		return "", historyRangeError(value)
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		return "", historyRangeError(value)
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil {
		return "", historyRangeError(value)
	}
	if start > end {
		return "", fmt.Errorf("invalid --history-range %q: start (%d) is after end (%d)", value, start, end)
	}
	return v, nil
}

func historyRangeError(value string) error {
	return fmt.Errorf(
		"invalid --history-range %q: expected \"<start>-<end>\" in epoch "+
			"seconds (e.g. 1705036708-1705123108); use --start/--end for "+
			"'now' or relative durations like -7d",
		value,
	)
}

func newMonitorsTriggersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "triggers",
		Short: "List monitor triggers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListMonitorTriggersWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, monitorTriggerColumns)
		},
	}
}

func newMonitorsTemplateFilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "template-files",
		Short: "Create monitor template files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.CreateTemplateFilesWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
}
