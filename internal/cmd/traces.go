package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newTracesCmd returns the `oodle traces` command tree.
func newTracesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "traces",
		Aliases: []string{"trace"},
		Short:   "Query traces with filters or TraceQL, and list trace labels",
		Long: `Query traces, trace labels, and label values.

  list, get              Find traces with simple filters, or get one by ID.
  labels, label-values   List trace label names and their values.
  traceql                Run TraceQL search and metrics queries.

Use 'oodle traces traceql --help' for TraceQL examples, and for how to alert
on trace data.`,
	}
	cmd.AddCommand(newTracesListCmd())
	cmd.AddCommand(newTracesGetCmd())
	cmd.AddCommand(newTracesLabelsCmd())
	cmd.AddCommand(newTracesLabelValuesCmd())
	cmd.AddCommand(newTracesTraceQLCmd())
	return cmd
}

func newTracesListCmd() *cobra.Command {
	var (
		startStr    string
		endStr      string
		service     string
		operation   string
		minDuration string
		maxDuration string
		tags        string
		search      string
		limit       int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List traces in a time range",
		Long: `List traces in a time range. --start and --end are required.

Filter with --service, --operation, --min-duration, --max-duration, --tags
and --search. For conditions on any span attribute, use
'oodle traces traceql search'.`,
		Example: `  oodle traces list --start -1h --end now --service api --limit 20
  oodle traces list --start -30m --end now --min-duration 2s -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			start, err := parseTimeFlag(startStr)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			end, err := parseTimeFlag(endStr)
			if err != nil {
				return fmt.Errorf("--end: %w", err)
			}

			params := &client.ListTracesParams{
				Start: start,
				End:   end,
			}
			if cmd.Flags().Changed("service") {
				v := service
				params.Service = &v
			}
			if cmd.Flags().Changed("operation") {
				v := operation
				params.Operation = &v
			}
			if cmd.Flags().Changed("min-duration") {
				v := minDuration
				params.MinDuration = &v
			}
			if cmd.Flags().Changed("max-duration") {
				v := maxDuration
				params.MaxDuration = &v
			}
			if cmd.Flags().Changed("tags") {
				v := tags
				params.Tags = &v
			}
			if cmd.Flags().Changed("search") {
				v := search
				params.Search = &v
			}
			if cmd.Flags().Changed("limit") {
				v := limit
				params.Limit = &v
			}

			resp, err := c.Inner.ListTracesWithResponse(cmd.Context(), instance, params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			// The response is map[string]interface{}; render as JSON/YAML for
			// structured formats and dump key/value pairs for tabular output.
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&service, "service", "", "Filter by service name")
	cmd.Flags().StringVar(&operation, "operation", "", "Filter by operation name")
	cmd.Flags().StringVar(&minDuration, "min-duration", "", "Minimum trace duration (e.g. 100ms, 1s)")
	cmd.Flags().StringVar(&maxDuration, "max-duration", "", "Maximum trace duration (e.g. 5s, 10s)")
	cmd.Flags().StringVar(&tags, "tags", "", `JSON-encoded map of tag filters (e.g. {"http.method":"GET"})`)
	cmd.Flags().StringVar(&search, "search", "", "Free-text search across trace data")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of traces to return")
	_ = cmd.MarkFlagRequired("start")
	_ = cmd.MarkFlagRequired("end")
	return cmd
}

func newTracesGetCmd() *cobra.Command {
	var (
		startStr string
		endStr   string
	)
	cmd := &cobra.Command{
		Use:     "get <trace_id>",
		Short:   "Get a trace by ID",
		Long:    "Get a trace by ID. --start and --end are required and must include the start time of the trace.",
		Example: `  oodle traces get 4bf92f3577b34da6a3ce929d0e0e4736 --start -1h --end now -o json`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			start, err := parseTimeFlag(startStr)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			end, err := parseTimeFlag(endStr)
			if err != nil {
				return fmt.Errorf("--end: %w", err)
			}

			params := &client.GetTracesByIdParams{
				Start: start,
				End:   end,
			}
			resp, err := c.Inner.GetTracesByIdWithResponse(cmd.Context(), instance, args[0], params)
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
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch microseconds, 'now', or relative like -1h)")
	_ = cmd.MarkFlagRequired("start")
	_ = cmd.MarkFlagRequired("end")
	return cmd
}

func newTracesLabelsCmd() *cobra.Command {
	var (
		startStr string
		endStr   string
	)
	cmd := &cobra.Command{
		Use:   "labels",
		Short: "List trace label names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			params := &client.ListLabelsParams{}
			if cmd.Flags().Changed("start") {
				start, err := parseTimeFlag(startStr)
				if err != nil {
					return fmt.Errorf("--start: %w", err)
				}
				params.Start = &start
			}
			if cmd.Flags().Changed("end") {
				end, err := parseTimeFlag(endStr)
				if err != nil {
					return fmt.Errorf("--end: %w", err)
				}
				params.End = &end
			}

			resp, err := c.Inner.ListLabelsWithResponse(cmd.Context(), instance, params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil || resp.JSON200.Data == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printStringSlice(cmd, format, *resp.JSON200.Data, "Label")
		},
	}
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch microseconds, 'now', or relative like -1h)")
	return cmd
}

func newTracesLabelValuesCmd() *cobra.Command {
	var (
		startStr string
		endStr   string
	)
	cmd := &cobra.Command{
		Use:   "label-values <label_name>",
		Short: "List values for a trace label",
		Long: `List values for a trace label.

Trace labels carry a scope prefix, for example resource::service.name or
span::http.method. Run 'oodle traces labels' to list them. A plain OpenTelemetry
name such as service.name resolves to the scoped label when exactly one
matches. An unknown label name fails with a suggestion.`,
		Example: `  oodle traces label-values resource::service.name --start -1h
  oodle traces label-values service.name`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			params := &client.GetTraceLabelValuesByIdParams{}
			if cmd.Flags().Changed("start") {
				start, err := parseTimeFlag(startStr)
				if err != nil {
					return fmt.Errorf("--start: %w", err)
				}
				params.Start = &start
			}
			if cmd.Flags().Changed("end") {
				end, err := parseTimeFlag(endStr)
				if err != nil {
					return fmt.Errorf("--end: %w", err)
				}
				params.End = &end
			}

			fetchValues := func(label string) ([]string, error) {
				resp, err := c.Inner.GetTraceLabelValuesByIdWithResponse(cmd.Context(), instance, label, params)
				if err != nil {
					return nil, fmt.Errorf("API request failed: %w", err)
				}
				if resp.StatusCode() >= 300 {
					return nil, api.CheckResponse(resp.HTTPResponse, resp.Body)
				}
				if resp.JSON200 == nil || resp.JSON200.Data == nil {
					return nil, fmt.Errorf("unexpected empty response")
				}
				return *resp.JSON200.Data, nil
			}

			label := args[0]
			values, err := fetchValues(label)
			if err != nil {
				return err
			}
			// The API returns [] for a label that does not exist. Look the
			// name up so a wrong name is not mistaken for missing data.
			if len(values) == 0 {
				labelsResp, err := c.Inner.ListLabelsWithResponse(cmd.Context(), instance, &client.ListLabelsParams{
					Start: params.Start,
					End:   params.End,
				})
				if err != nil {
					return fmt.Errorf("API request failed: %w", err)
				}
				if labelsResp.StatusCode() >= 300 {
					return api.CheckResponse(labelsResp.HTTPResponse, labelsResp.Body)
				}
				// With no labels at all in the range, there is no data to check against.
				if labelsResp.JSON200 != nil && labelsResp.JSON200.Data != nil && len(*labelsResp.JSON200.Data) > 0 {
					resolved, err := resolveTraceLabel(label, *labelsResp.JSON200.Data)
					if err != nil {
						return err
					}
					if resolved != label {
						fmt.Fprintf(cmd.ErrOrStderr(), "Using trace label %q for %q.\n", resolved, label)
						if values, err = fetchValues(resolved); err != nil {
							return err
						}
					}
				}
			}
			return printStringSlice(cmd, format, values, "Value")
		},
	}
	cmd.Flags().StringVar(&startStr, "start", "", "Start of the time range (epoch microseconds, 'now', or relative like -1h)")
	cmd.Flags().StringVar(&endStr, "end", "", "End of the time range (epoch microseconds, 'now', or relative like -1h)")
	return cmd
}

// traceLabelScopes are the prefixes, in lookup order, that the traces API puts
// in front of attribute names.
var traceLabelScopes = []string{"resource::", "span::", "events::"}

// traceLabelAliases maps shorthand names to their OpenTelemetry attribute.
var traceLabelAliases = map[string]string{
	"service": "service.name",
}

// resolveTraceLabel maps name to a label in known. An exact match wins. An
// unscoped name, or a shorthand alias, resolves to the scoped label when
// exactly one scope has it. Otherwise it returns an error that suggests the
// closest labels.
func resolveTraceLabel(name string, known []string) (string, error) {
	set := make(map[string]bool, len(known))
	for _, l := range known {
		set[l] = true
	}
	if set[name] {
		return name, nil
	}

	bases := []string{name}
	if alias, ok := traceLabelAliases[name]; ok {
		if set[alias] {
			return alias, nil
		}
		bases = append(bases, alias)
	}
	var matches []string
	for _, base := range bases {
		if strings.Contains(base, "::") {
			continue
		}
		for _, scope := range traceLabelScopes {
			if set[scope+base] {
				matches = append(matches, scope+base)
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("trace label %q is ambiguous; use one of: %s", name, strings.Join(matches, ", "))
	}

	msg := fmt.Sprintf("unknown trace label %q", name)
	if s := closestTraceLabels(name, known, 3); len(s) > 0 {
		msg += fmt.Sprintf("; did you mean %s?", strings.Join(s, ", "))
	}
	return "", fmt.Errorf("%s (run 'oodle traces labels' to list label names)", msg)
}

// closestTraceLabels returns up to n labels from known nearest to name by edit
// distance. The scope prefix is ignored when comparing, so "service.nam" still
// finds "resource::service.name".
func closestTraceLabels(name string, known []string, n int) []string {
	type candidate struct {
		label string
		dist  int
	}
	target := strings.ToLower(stripTraceScope(name))
	if alias, ok := traceLabelAliases[target]; ok {
		target = alias
	}
	maxDist := len(target)/3 + 1
	var cands []candidate
	for _, l := range known {
		d := levenshtein(target, strings.ToLower(stripTraceScope(l)))
		if d <= maxDist {
			cands = append(cands, candidate{l, d})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].label < cands[j].label
	})
	var out []string
	for i := 0; i < len(cands) && i < n; i++ {
		out = append(out, cands[i].label)
	}
	return out
}

// stripTraceScope removes everything up to the last "::" in a label name.
func stripTraceScope(label string) string {
	if i := strings.LastIndex(label, "::"); i >= 0 {
		return label[i+2:]
	}
	return label
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
