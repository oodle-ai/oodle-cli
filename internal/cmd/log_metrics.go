package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// logMetricsSchema describes the rule file. Both the group help and the
// create help show it, because users and agents read either one first.
const logMetricsSchema = `Rule fields:
  name               Name of the rule.
  filter             Which log lines the rule reads. One of:
                       {field, operator, value, jsonPath}   one condition
                       {"all": [filter, ...]}   every filter must match
                       {"any": [filter, ...]}   one or more filters must match
                       {"not": filter}          the filter must not match
                     Items in all, any and not use the same forms, so you
                     can nest them.
                     Operators: "is", "contains", "matches regex", "exists".
  labels             Labels to add to each metric. Each label has a name and
                     one of:
                       value: a static value
                       valueExtractor: {field, jsonPath, regex}
  metricDefinitions  The metrics to make. Each has a name and a type:
                       log_count  count of matching log lines
                       counter    sum of a numeric value
                       gauge      last numeric value
                       histogram  distribution of a numeric value
                     counter, gauge and histogram read the value from
                     "field", with an optional "jsonPath" or "regex".

A field is a top-level log field. Its name has only letters, digits and
underscores. Use jsonPath to read a nested value in a JSON field. A regex
uses Rust regex syntax. When a regex extracts a value, capture group 1 is the
value, so put one group around the part you need.

Metric names:
  Each metric is written as oodle_logs_<name>. A histogram also writes the
  _bucket, _sum and _count series.

Example: count failed logins and label each count with the user name that a
regex captures from the message field.

  {
    "name": "login-failures",
    "filter": {"all": [
      {"field": "service", "operator": "is", "value": "auth"},
      {"field": "message", "operator": "matches regex",
       "value": "login failed for user \\S+"}
    ]},
    "labels": [
      {"name": "user", "valueExtractor": {"field": "message",
        "regex": "login failed for user (\\S+)"}}
    ],
    "metricDefinitions": [{"name": "login_failures", "type": "log_count"}]
  }

A filter with one condition is the condition itself, for example
  "filter": {"field": "level", "operator": "is", "value": "error"}

This rule makes oodle_logs_login_failures. Query it with PromQL:

  sum by (user) (increase(oodle_logs_login_failures[5m]))`

const logMetricsLong = `Manage log metrics rules. A rule turns matching log lines into metrics.

To alert on logs:
  1. Create a log metrics rule:  oodle log-metrics create -f rule.json
  2. Create a monitor with a PromQL query on the metric that the rule makes,
     for example sum(increase(oodle_logs_login_failures[5m])) > 10.

A rule reads only logs that arrive after you create or change it. It does not
fill in metrics for older logs. You do not need a log transform or pipeline
change to use a rule.

` + logMetricsSchema

// logMetricsColumns are the table columns for log metrics rules.
var logMetricsColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
}

// logMetricsRow holds only the fields that the table shows. The filter is
// not decoded, because the generated type cannot hold every filter form.
type logMetricsRow struct {
	Name string `json:"name"`
	Id   string `json:"id"`
}

// printLogMetricsResponse prints a log metrics response body. JSON and YAML
// output use the body as the server sent it. Decoding it into the generated
// type first drops a filter that is one inline condition, and a user who
// then edits and updates the printed rule removes the filter on the server.
func printLogMetricsResponse(cmd *cobra.Command, httpResp *http.Response, list bool) error {
	defer func() { _ = httpResp.Body.Close() }()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if err := api.CheckResponse(httpResp, body); err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("unexpected empty response")
	}

	w := cmd.OutOrStdout()
	format := getOutputFormat(cmd)
	switch format {
	case output.FormatJSON, "":
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
		buf.WriteByte('\n')
		_, err := w.Write(buf.Bytes())
		return err
	case output.FormatYAML:
		var parsed any
		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
		return output.Print(w, format, parsed, nil)
	}

	var rows []logMetricsRow
	if list {
		if err := json.Unmarshal(body, &rows); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
	} else {
		var row logMetricsRow
		if err := json.Unmarshal(body, &row); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
		rows = []logMetricsRow{row}
	}
	return output.Print(w, format, rows, logMetricsColumns)
}

// newLogMetricsCmd returns the `oodle log-metrics` command tree.
func newLogMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "log-metrics",
		Aliases: []string{"lm", "logmetrics"},
		Short:   "Manage log-derived metrics",
		Long:    logMetricsLong,
	}

	cmd.AddCommand(newLogMetricsListCmd())
	cmd.AddCommand(newLogMetricsGetCmd())
	cmd.AddCommand(newLogMetricsCreateCmd())
	cmd.AddCommand(newLogMetricsUpdateCmd())
	cmd.AddCommand(newLogMetricsDeleteCmd())

	return cmd
}

func newLogMetricsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List log metrics rules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			httpResp, err := c.Inner.ListLogmetrics(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			return printLogMetricsResponse(cmd, httpResp, true)
		},
	}
}

func newLogMetricsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a log metrics rule by ID",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			httpResp, err := c.Inner.GetLogmetricsById(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			return printLogMetricsResponse(cmd, httpResp, false)
		},
	}
}

func newLogMetricsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a log metrics rule from a JSON or YAML file",
		Long: `Create a log metrics rule from a JSON or YAML file.

The file is sent to the server as it is (YAML is converted to JSON).

` + logMetricsSchema,
		Example: `  oodle log-metrics create -f login-failures.json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			body, err := readInputFileJSON(file)
			if err != nil {
				return err
			}
			httpResp, err := c.Inner.CreateLogmetricsWithBody(cmd.Context(), instance, "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			return printLogMetricsResponse(cmd, httpResp, false)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the log metrics rule (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newLogMetricsUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a log metrics rule from a JSON or YAML file",
		Long: `Update a log metrics rule from a JSON or YAML file.

The file replaces the rule. It is sent to the server as it is (YAML is
converted to JSON). To change one part of a rule, run
'oodle log-metrics get <id> -o json', edit the output, and give it here.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			body, err := readInputFileJSON(file)
			if err != nil {
				return err
			}
			httpResp, err := c.Inner.UpdateLogmetricsByIdWithBody(cmd.Context(), instance, args[0], "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			return printLogMetricsResponse(cmd, httpResp, false)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the updated log metrics rule (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newLogMetricsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a log metrics rule",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)

			if !confirmAction(fmt.Sprintf("Delete log metrics rule %q?", args[0]), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteLogmetricsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted log metrics rule %s\n", args[0])
			return nil
		},
	}
}
