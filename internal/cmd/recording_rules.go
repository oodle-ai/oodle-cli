package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// The server's largest page. List reads every page.
const recordingRulesPageSize = 1000

// newRecordingRulesCmd returns the `oodle recording-rules` command tree.
func newRecordingRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "recording-rules",
		Aliases: []string{"rr", "recording-rule"},
		Short:   "Manage recording rules",
		Long: `Manage recording rules. A recording rule evaluates a PromQL expression on
an interval and stores the result as a new metric.

Use 'apply' to load Prometheus rule groups, such as the output of
'sloth generate', and keep Oodle in step with them.`,
	}
	cmd.AddCommand(newRecordingRulesListCmd())
	cmd.AddCommand(newRecordingRulesGetCmd())
	cmd.AddCommand(newRecordingRulesStatusCmd())
	cmd.AddCommand(newRecordingRulesCreateCmd())
	cmd.AddCommand(newRecordingRulesUpdateCmd())
	cmd.AddCommand(newRecordingRulesDeleteCmd())
	cmd.AddCommand(newRecordingRulesApplyCmd())
	return cmd
}

func recordingRuleColumns() []output.Column {
	return []output.Column{
		{Header: "ID", Field: "Id"},
		{Header: "METRIC", Field: "MetricName"},
		{Header: "GROUP", Field: "ExecutionGroup"},
		{Header: "INTERVAL", Field: "IntervalSecs"},
	}
}

// listAllRecordingRules reads every page that matches the filter.
func listAllRecordingRules(
	ctx context.Context,
	c *api.Client,
	instance string,
	params client.ListRecordingRulesParams,
) ([]client.RecordingRule, error) {
	var rules []client.RecordingRule
	size := recordingRulesPageSize
	params.PageMaxSize = &size
	for {
		resp, err := c.Inner.ListRecordingRulesWithResponse(ctx, instance, &params)
		if err != nil {
			return nil, fmt.Errorf("API request failed: %w", err)
		}
		if resp.StatusCode() >= 300 {
			return nil, api.CheckResponse(resp.HTTPResponse, resp.Body)
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("unexpected empty response")
		}
		if resp.JSON200.RecordingRules != nil {
			rules = append(rules, *resp.JSON200.RecordingRules...)
		}
		next := resp.JSON200.Page.NextToken
		if next == nil || *next == "" {
			return rules, nil
		}
		params.PageToken = next
	}
}

func newRecordingRulesListCmd() *cobra.Command {
	var groups, ids, names []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recording rules",
		Example: `  oodle recording-rules list
  oodle recording-rules list --execution-group sloth-slo-sli-recordings-postbox-api-availability`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := client.ListRecordingRulesParams{}
			if len(groups) > 0 {
				v := strings.Join(groups, ",")
				params.ExecutionGroups = &v
			}
			if len(ids) > 0 {
				v := strings.Join(ids, ",")
				params.Ids = &v
			}
			if len(names) > 0 {
				v := strings.Join(names, ",")
				params.Names = &v
			}
			rules, err := listAllRecordingRules(cmd.Context(), getClient(cmd), getInstance(cmd), params)
			if err != nil {
				return err
			}
			if rules == nil {
				rules = []client.RecordingRule{}
			}
			return output.Print(cmd.OutOrStdout(), getOutputFormat(cmd), rules, recordingRuleColumns())
		},
	}
	cmd.Flags().StringSliceVar(&groups, "execution-group", nil, "Show only rules in these execution groups")
	cmd.Flags().StringSliceVar(&ids, "id", nil, "Show only rules with these IDs")
	cmd.Flags().StringSliceVar(&names, "name", nil, "Show only rules with these names")
	return cmd
}

func newRecordingRulesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a recording rule",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient(cmd).Inner.GetRecordingRuleWithResponse(cmd.Context(), getInstance(cmd), args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil || resp.JSON200.RecordingRule == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printBodyOrTable(cmd, resp.Body, []client.RecordingRule{*resp.JSON200.RecordingRule}, recordingRuleColumns())
		},
	}
}

func newRecordingRulesStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <id>",
		Short: "Show the last evaluation of a recording rule",
		Long: `Show the last evaluation of a recording rule: its health, when it ran,
how long it took, and the last error. A rule that has not run yet has the
health "unknown".`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient(cmd).Inner.GetRecordingRuleStatusWithResponse(cmd.Context(), getInstance(cmd), args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printBodyOrTable(cmd, resp.Body, []client.RecordingRuleStatus{resp.JSON200.Status}, []output.Column{
				{Header: "ID", Field: "Id"},
				{Header: "HEALTH", Field: "Health"},
				{Header: "LAST EVALUATION", Field: "LastEvaluation"},
				{Header: "SECONDS", Field: "EvaluationTimeSecs"},
				{Header: "LAST ERROR", Field: "LastError"},
			})
		},
	}
}

// readRecordingRuleFile reads one rule. The file holds the rule itself, or
// a request body with the rule under "recording_rule".
func readRecordingRuleFile(path string) (map[string]any, error) {
	raw, err := readInputFileJSON(path)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if inner, ok := obj["recording_rule"].(map[string]any); ok {
		return inner, nil
	}
	return obj, nil
}

func newRecordingRulesCreateCmd() *cobra.Command {
	var file string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a recording rule from a JSON or YAML file",
		Long: `Create a recording rule from a JSON or YAML file.

The file holds name, metric_name and prometheus_expr, and optionally
interval_secs, execution_group and label_policy.add. Oodle gives the rule
its ID. Run 'oodle recording-rules get <id> -o yaml' on a rule for a
template.`,
		Example: `  oodle recording-rules create -f rule.yaml
  oodle recording-rules create -f rule.yaml --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := readRecordingRuleFile(file)
			if err != nil {
				return err
			}
			body, err := json.Marshal(map[string]any{"recording_rule": rule, "dry_run": dryRun})
			if err != nil {
				return err
			}
			resp, err := getClient(cmd).Inner.CreateRecordingRuleWithBodyWithResponse(cmd.Context(), getInstance(cmd), "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "The recording rule is valid. Nothing was stored.")
				return nil
			}
			if resp.JSON200 == nil || resp.JSON200.RecordingRule == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printBodyOrTable(cmd, resp.Body, []client.RecordingRule{*resp.JSON200.RecordingRule}, recordingRuleColumns())
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the recording rule (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Check the rule and store nothing")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newRecordingRulesUpdateCmd() *cobra.Command {
	var file string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Replace a recording rule with the rule in a JSON or YAML file",
		Long: `Replace a recording rule with the rule in a JSON or YAML file.

The file replaces the whole rule. To change one field, run
'oodle recording-rules get <id> -o yaml', edit the output, and give it here.
The ID cannot change.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := readRecordingRuleFile(file)
			if err != nil {
				return err
			}
			body, err := json.Marshal(map[string]any{
				"recording_rule": rule,
				"dry_run":        dryRun,
			})
			if err != nil {
				return err
			}
			resp, err := getClient(cmd).Inner.UpdateRecordingRuleWithBodyWithResponse(cmd.Context(), getInstance(cmd), args[0], "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "The recording rule is valid. Nothing was stored.")
				return nil
			}
			if resp.JSON200 == nil || resp.JSON200.RecordingRule == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return printBodyOrTable(cmd, resp.Body, []client.RecordingRule{*resp.JSON200.RecordingRule}, recordingRuleColumns())
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the recording rule (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Check the rule and store nothing")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newRecordingRulesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a recording rule",
		Long: `Delete a recording rule. Its metric stops updating. The series that it
already wrote stay until their retention ends.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirmAction(fmt.Sprintf("Delete recording rule %q?", args[0]), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := getClient(cmd).Inner.DeleteRecordingRuleWithResponse(cmd.Context(), getInstance(cmd), args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted recording rule %s\n", args[0])
			return nil
		},
	}
}

// promRuleFile is the Prometheus rule file format, which Sloth and most
// rule generators print.
type promRuleFile struct {
	Groups []promRuleGroup `yaml:"groups"`
}

type promRuleGroup struct {
	Name     string     `yaml:"name"`
	Interval string     `yaml:"interval"`
	Rules    []promRule `yaml:"rules"`
}

type promRule struct {
	Record string            `yaml:"record"`
	Alert  string            `yaml:"alert"`
	Expr   string            `yaml:"expr"`
	Labels map[string]string `yaml:"labels"`
}

// plannedRule is one rule of an apply, in the order of the file.
type plannedRule struct {
	// Key matches the rule to a stored rule; see applyKey.
	Key  string
	Body map[string]any
}

// applyKey names a rule by its group, its metric, and how many rules
// before it in the group write the same metric. The server makes rule
// IDs, so apply finds the stored rule of a file rule by this key on every
// apply of the same file. A change of the expression or the labels keeps
// the key, so the rule is updated in place and keeps its place in the
// group.
func applyKey(group, record string, n int) string {
	if n == 0 {
		return group + "/" + record
	}
	return fmt.Sprintf("%s/%s#%d", group, record, n+1)
}

// planRecordingRules turns rule groups into recording rules. Alerting
// rules are an error: they belong in monitors.
func planRecordingRules(data []byte) ([]plannedRule, []string, error) {
	var file promRuleFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, nil, fmt.Errorf("parsing rule groups: %w", err)
	}
	if len(file.Groups) == 0 {
		return nil, nil, fmt.Errorf("the file has no rule groups; it needs a top-level 'groups:' list")
	}

	var planned []plannedRule
	var groups []string
	seenGroups := map[string]bool{}
	for _, g := range file.Groups {
		if g.Name == "" {
			return nil, nil, fmt.Errorf("a rule group has no name")
		}
		if seenGroups[g.Name] {
			return nil, nil, fmt.Errorf("rule group %q appears twice", g.Name)
		}
		seenGroups[g.Name] = true
		groups = append(groups, g.Name)

		interval := 60
		if g.Interval != "" {
			d, err := time.ParseDuration(g.Interval)
			if err != nil {
				return nil, nil, fmt.Errorf("rule group %q: invalid interval %q: %w", g.Name, g.Interval, err)
			}
			interval = int(d.Seconds())
		}

		seen := map[string]int{}
		for i, r := range g.Rules {
			if r.Alert != "" {
				return nil, nil, fmt.Errorf("rule group %q rule %d is an alerting rule (%q); create a monitor for it instead", g.Name, i+1, r.Alert)
			}
			if r.Record == "" {
				return nil, nil, fmt.Errorf("rule group %q rule %d has no 'record'", g.Name, i+1)
			}
			// The record comes first: rules of one group share the
			// group name, and a list that cuts long names would show
			// only that shared part.
			body := map[string]any{
				"name":            fmt.Sprintf("%s (%s)", r.Record, g.Name),
				"metric_name":     r.Record,
				"prometheus_expr": strings.TrimSpace(r.Expr),
				"interval_secs":   interval,
				"execution_group": g.Name,
			}
			if len(r.Labels) > 0 {
				body["label_policy"] = map[string]any{"add": r.Labels}
			}
			key := applyKey(g.Name, r.Record, seen[r.Record])
			seen[r.Record]++
			planned = append(planned, plannedRule{Key: key, Body: body})
		}
	}
	return planned, groups, nil
}

// storedByKey keys the stored rules of the file's groups as applyKey
// does. Rules of a group that write one metric are counted in the order
// that the group runs them: by creation time, then by ID. A stored rule
// whose key is not in the file, for example a copy left by a create
// whose answer was lost, is one that --prune deletes.
func storedByKey(rules []client.RecordingRule) map[string]client.RecordingRule {
	sorted := append([]client.RecordingRule(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti, tj := sorted[i].CreatedAt, sorted[j].CreatedAt
		if ti != nil && tj != nil && !ti.Equal(*tj) {
			return ti.Before(*tj)
		}
		return deref(sorted[i].Id) < deref(sorted[j].Id)
	})
	byKey := map[string]client.RecordingRule{}
	seen := map[string]int{}
	for _, r := range sorted {
		group := deref(r.ExecutionGroup)
		k := group + "/" + r.MetricName
		byKey[applyKey(group, r.MetricName, seen[k])] = r
		seen[k]++
	}
	return byKey
}

func readApplyInput(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(cmd.InOrStdin())
	}
	return os.ReadFile(path)
}

func newRecordingRulesApplyCmd() *cobra.Command {
	var file string
	var dryRun, prune bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Create or update recording rules from Prometheus rule groups",
		Long: `Create or update recording rules from a Prometheus rule file: a YAML file
with a top-level 'groups:' list, each group with a name, an optional interval
and recording rules (record, expr, labels). 'sloth generate' prints this
format.

Each group becomes an execution group, so its rules run in the file's order.
Apply finds the stored rule of each file rule by its group and its record
(and, when a group records one metric more than once, by its position among
those rules). It updates that rule, or creates the rule when there is none.
So a later apply of the same file updates the rules and does not create them
again.

With --prune, apply also deletes the rules that are in one of the file's
groups but no longer in the file. Rules in other groups do not change.

New rules run in the file's order. A rule that already exists keeps its
place in its group.`,
		Example: `  oodle recording-rules apply -f rules.yaml --dry-run
  sloth generate -i slo.yaml --disable-alerts | oodle recording-rules apply -f - --prune`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := readApplyInput(cmd, file)
			if err != nil {
				return fmt.Errorf("reading %s: %w", file, err)
			}
			planned, groups, err := planRecordingRules(data)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			c := getClient(cmd)
			instance := getInstance(cmd)
			out := cmd.OutOrStdout()

			joined := strings.Join(groups, ",")
			existing, err := listAllRecordingRules(ctx, c, instance, client.ListRecordingRulesParams{ExecutionGroups: &joined})
			if err != nil {
				return err
			}
			byKey := storedByKey(existing)

			created, updated := 0, 0
			wanted := map[string]bool{}
			for _, p := range planned {
				body, err := json.Marshal(map[string]any{
					"recording_rule": p.Body,
					"dry_run":        dryRun,
				})
				if err != nil {
					return err
				}
				stored, ok := byKey[p.Key]
				if ok && stored.Id != nil {
					wanted[*stored.Id] = true
					resp, err := c.Inner.UpdateRecordingRuleWithBodyWithResponse(ctx, instance, *stored.Id, "application/json", bytes.NewReader(body))
					if err != nil {
						return fmt.Errorf("applying %s: API request failed: %w", p.Key, err)
					}
					if resp.StatusCode() >= 300 {
						return fmt.Errorf("applying %s: %w", p.Key, api.CheckResponse(resp.HTTPResponse, resp.Body))
					}
					updated++
					continue
				}
				resp, err := c.Inner.CreateRecordingRuleWithBodyWithResponse(ctx, instance, "application/json", bytes.NewReader(body))
				if err != nil {
					return fmt.Errorf("applying %s: API request failed: %w", p.Key, err)
				}
				if resp.StatusCode() >= 300 {
					return fmt.Errorf("applying %s: %w", p.Key, api.CheckResponse(resp.HTTPResponse, resp.Body))
				}
				created++
			}

			type staleRule struct{ id, key string }
			var stale []staleRule
			if prune {
				for key, r := range byKey {
					if r.Id != nil && !wanted[*r.Id] {
						stale = append(stale, staleRule{id: *r.Id, key: key})
					}
				}
				sort.Slice(stale, func(i, j int) bool { return stale[i].key < stale[j].key })
			}

			for _, r := range stale {
				if dryRun {
					continue
				}
				resp, err := c.Inner.DeleteRecordingRuleWithResponse(ctx, instance, r.id)
				if err != nil {
					return fmt.Errorf("deleting %s: API request failed: %w", r.key, err)
				}
				// A rule that is gone already needs no delete.
				if resp.StatusCode() >= 300 && resp.StatusCode() != http.StatusNotFound {
					return fmt.Errorf("deleting %s: %w", r.key, api.CheckResponse(resp.HTTPResponse, resp.Body))
				}
			}

			verb, createdWord, updatedWord, deleted := "Applied", "created", "updated", "deleted"
			if dryRun {
				verb, createdWord, updatedWord, deleted = "Checked", "would create", "would update", "would delete"
			}
			fmt.Fprintf(out, "%s %d recording rules in %d groups (%s %d, %s %d)",
				verb, len(planned), len(groups), createdWord, created, updatedWord, updated)
			if prune {
				fmt.Fprintf(out, "; %s %d", deleted, len(stale))
			}
			fmt.Fprintln(out, ".")
			for _, r := range stale {
				fmt.Fprintf(out, "  %s %s (%s)\n", deleted, r.key, r.id)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to the rule file, or - for stdin (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Check every rule and change nothing")
	cmd.Flags().BoolVar(&prune, "prune", false, "Delete rules of the file's groups that are no longer in the file")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
