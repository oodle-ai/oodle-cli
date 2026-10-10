package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// genaiLabelNames maps a trace label back to its built-in field name,
// so that the field list shows the names that the filter flags take.
var genaiLabelNames = func() map[string]string {
	m := make(map[string]string, len(genaiFilterLabels))
	for name, label := range genaiFilterLabels {
		m[label] = name
	}
	return m
}()

type genaiDataResponse struct {
	Data []string `json:"data"`
}

type genaiFieldRow struct {
	Field string `json:"field"`
	Kind  string `json:"kind"`
}

type genaiValueRow struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func newGenAIValuesCmd() *cobra.Command {
	var (
		filters  genaiFilterFlags
		startStr string
		endStr   string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "values [field...]",
		Short: "List the fields and values of GenAI spans, to use as filters",
		Long: `List the fields and values of GenAI spans. Only spans with
gen_ai.operation.name are read, so 'values service' lists only the
services that send LLM or agent spans.

Without a field, list the fields: the built-in names that the filter
flags of 'oodle genai traces' take (service, agent, model, tool,
operation, user, session, env, signal, score), and the custom span
attributes, which 'genai traces --attr key=value' takes.

With fields, list the values of each field. A built-in name maps to
its attribute, a name such as resource::region is used as it is, and
any other name is a span attribute. Filter flags narrow the spans that
are read, for example --agent planner to list only the tools of that
agent.

The default time range is the last 24 hours. The server can limit one
read to about one day before --end; for older data, move --end back.`,
		Example: `  oodle genai values
  oodle genai values service agent model
  oodle genai values tool --agent planner --start -6h
  oodle genai values tenant -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 {
				return fmt.Errorf("--limit must be 1 or more, got %d", limit)
			}
			start, err := parseTimeFlag(startStr)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			end, err := parseTimeFlag(endStr)
			if err != nil {
				return fmt.Errorf("--end: %w", err)
			}
			matchers, err := filters.matchers()
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(matchers)
			if err != nil {
				return fmt.Errorf("encoding filters: %w", err)
			}
			params := url.Values{}
			params.Set("start", strconv.FormatInt(start, 10))
			params.Set("end", strconv.FormatInt(end, 10))
			params.Set("filters", string(encoded))
			startT, endT := time.UnixMicro(start), time.UnixMicro(end)

			if len(args) == 0 {
				labels, err := genaiReadList(cmd, "traces/labels", params)
				if err != nil {
					return err
				}
				rows := genaiFieldRows(labels)
				if err := genaiPrintRows(cmd, rows, []output.Column{
					{Header: "FIELD", Field: "Field"},
					{Header: "KIND", Field: "Kind"},
				}); err != nil {
					return err
				}
				if len(rows) == 0 {
					hintNoData(cmd, "GenAI span fields", startT, endT)
				}
				return nil
			}

			var rows []genaiValueRow
			byField := map[string][]string{}
			for _, field := range args {
				label := genaiLabelFor(field)
				values, err := genaiReadList(cmd, "traces/labels/"+url.PathEscape(label)+"/values", params)
				if err != nil {
					return fmt.Errorf("%s: %w", field, err)
				}
				if len(values) > limit {
					fmt.Fprintf(cmd.ErrOrStderr(), "%s: showing %d of %d values. Raise --limit to see more.\n", field, limit, len(values))
					values = values[:limit]
				}
				byField[field] = values
				for _, v := range values {
					rows = append(rows, genaiValueRow{Field: field, Value: v})
				}
				if len(values) == 0 {
					hintNoData(cmd, "values for "+field, startT, endT)
					if _, ok := genaiFilterLabels[field]; !ok && !strings.Contains(field, "::") {
						fmt.Fprintf(cmd.ErrOrStderr(), "%q is read as the span attribute %s. Run 'oodle genai values' to list the fields.\n", field, label)
					}
				}
			}
			if !isTabular(cmd) {
				return printShaped(cmd, byField)
			}
			return genaiPrintRows(cmd, rows, []output.Column{
				{Header: "FIELD", Field: "Field"},
				{Header: "VALUE", Field: "Value"},
			})
		},
	}
	filters.addTo(cmd)
	cmd.Flags().StringVar(&startStr, "start", "-24h", "Start of the time range (relative like -1h, 'now', RFC3339, or epoch s/ms/µs/ns)")
	cmd.Flags().StringVar(&endStr, "end", "now", "End of the time range (relative like -1h, 'now', RFC3339, or epoch s/ms/µs/ns)")
	cmd.Flags().IntVar(&limit, "limit", 200, "Maximum number of values for each field")
	return cmd
}

// genaiReadList reads a {"data": [...]} list from a trace label route.
func genaiReadList(cmd *cobra.Command, route string, params url.Values) ([]string, error) {
	body, err := genaiGet(cmd, route, params, nil)
	if err != nil {
		return nil, err
	}
	var resp genaiDataResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return resp.Data, nil
}

// genaiFieldRows lists the built-in fields first, then the custom span
// attributes. Resource labels other than the built-in ones are left
// out, because the filter flags do not take them by a short name.
func genaiFieldRows(labels []string) []genaiFieldRow {
	builtin := map[string]bool{}
	custom := map[string]bool{}
	for _, l := range labels {
		if name, ok := genaiLabelNames[l]; ok {
			builtin[name] = true
		} else if rest, ok := strings.CutPrefix(l, "span::"); ok {
			custom[rest] = true
		}
	}
	rows := make([]genaiFieldRow, 0, len(builtin)+len(custom))
	for _, f := range sortedKeys(builtin) {
		rows = append(rows, genaiFieldRow{Field: f, Kind: "built-in"})
	}
	for _, f := range sortedKeys(custom) {
		rows = append(rows, genaiFieldRow{Field: f, Kind: "span attribute"})
	}
	return rows
}

func genaiPrintRows(cmd *cobra.Command, rows any, columns []output.Column) error {
	return output.Print(cmd.OutOrStdout(), getOutputFormat(cmd), rows, columns)
}
