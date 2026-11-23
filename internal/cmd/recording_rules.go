package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
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

// The server accepts slugs of at most this many characters.
const maxRecordingRuleSlug = 200

var slugUnsafeRE = regexp.MustCompile(`[^a-z0-9_.-]+`)

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
		{Header: "SLUG", Field: "Slug"},
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
	var groups, slugs, names []string
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
			if len(slugs) > 0 {
				v := strings.Join(slugs, ",")
				params.Slugs = &v
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
	cmd.Flags().StringSliceVar(&slugs, "slug", nil, "Show only rules with these slugs")
	cmd.Flags().StringSliceVar(&names, "name", nil, "Show only rules with these names")
	return cmd
}

func newRecordingRulesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <slug>",
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
		Use:   "status <slug>",
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
				{Header: "SLUG", Field: "Slug"},
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

The file holds name, metric_name and prometheus_expr, and optionally slug,
interval_secs, execution_group and label_policy.add. Run
'oodle recording-rules get <slug> -o yaml' on a rule for a template.`,
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
	var dryRun, createIfMissing bool
	cmd := &cobra.Command{
		Use:   "update <slug>",
		Short: "Replace a recording rule with the rule in a JSON or YAML file",
		Long: `Replace a recording rule with the rule in a JSON or YAML file.

The file replaces the whole rule. To change one field, run
'oodle recording-rules get <slug> -o yaml', edit the output, and give it here.
The slug cannot change.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := readRecordingRuleFile(file)
			if err != nil {
				return err
			}
			body, err := json.Marshal(map[string]any{
				"recording_rule":    rule,
				"dry_run":           dryRun,
				"create_if_missing": createIfMissing,
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
	cmd.Flags().BoolVar(&createIfMissing, "create-if-missing", false, "Create the rule if no rule has the slug")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newRecordingRulesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <slug>",
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
	Slug string
	Body map[string]any
}

// ruleSlug makes the slug of a rule from its group and its metric. The
// slug must be the same on every apply of the same file, so that apply
// updates rules and does not create them again. A rule's position is not
// in the slug, so adding a rule to a group does not rename the others.
func ruleSlug(group, record string, seen map[string]int) string {
	base := strings.Trim(slugUnsafeRE.ReplaceAllString(strings.ToLower(group+"."+record), "-"), "-_.")
	// Rules of one group can write the same metric, for example with
	// different labels. The second gets a number.
	seen[base]++
	if n := seen[base]; n > 1 {
		base = fmt.Sprintf("%s.%d", base, n)
	}
	if len(base) <= maxRecordingRuleSlug {
		return base
	}
	sum := sha256.Sum256([]byte(base))
	suffix := "-" + hex.EncodeToString(sum[:])[:12]
	return strings.TrimRight(base[:maxRecordingRuleSlug-len(suffix)], "-_.") + suffix
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
			body := map[string]any{
				"name":            fmt.Sprintf("%s %s", g.Name, r.Record),
				"metric_name":     r.Record,
				"prometheus_expr": strings.TrimSpace(r.Expr),
				"interval_secs":   interval,
				"execution_group": g.Name,
			}
			if len(r.Labels) > 0 {
				body["label_policy"] = map[string]any{"add": r.Labels}
			}
			slug := ruleSlug(g.Name, r.Record, seen)
			body["slug"] = slug
			planned = append(planned, plannedRule{Slug: slug, Body: body})
		}
	}
	return planned, groups, nil
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
Each rule gets the slug <group>.<record>, so a later apply of the same file
updates the rules and does not create them again.

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

			var stale []string
			if prune {
				joined := strings.Join(groups, ",")
				existing, err := listAllRecordingRules(ctx, c, instance, client.ListRecordingRulesParams{ExecutionGroups: &joined})
				if err != nil {
					return err
				}
				wanted := map[string]bool{}
				for _, p := range planned {
					wanted[p.Slug] = true
				}
				for _, r := range existing {
					if r.Slug != nil && !wanted[*r.Slug] {
						stale = append(stale, *r.Slug)
					}
				}
				sort.Strings(stale)
			}

			for _, p := range planned {
				body, err := json.Marshal(map[string]any{
					"recording_rule":    p.Body,
					"create_if_missing": true,
					"dry_run":           dryRun,
				})
				if err != nil {
					return err
				}
				resp, err := c.Inner.UpdateRecordingRuleWithBodyWithResponse(ctx, instance, p.Slug, "application/json", bytes.NewReader(body))
				if err != nil {
					return fmt.Errorf("applying %s: API request failed: %w", p.Slug, err)
				}
				if resp.StatusCode() >= 300 {
					return fmt.Errorf("applying %s: %w", p.Slug, api.CheckResponse(resp.HTTPResponse, resp.Body))
				}
			}

			for _, slug := range stale {
				if dryRun {
					continue
				}
				resp, err := c.Inner.DeleteRecordingRuleWithResponse(ctx, instance, slug)
				if err != nil {
					return fmt.Errorf("deleting %s: API request failed: %w", slug, err)
				}
				// A rule that is gone already needs no delete.
				if resp.StatusCode() >= 300 && resp.StatusCode() != http.StatusNotFound {
					return fmt.Errorf("deleting %s: %w", slug, api.CheckResponse(resp.HTTPResponse, resp.Body))
				}
			}

			verb := "Applied"
			deleted := "deleted"
			if dryRun {
				verb = "Checked"
				deleted = "would delete"
			}
			fmt.Fprintf(out, "%s %d recording rules in %d groups", verb, len(planned), len(groups))
			if prune {
				fmt.Fprintf(out, "; %s %d", deleted, len(stale))
			}
			fmt.Fprintln(out, ".")
			for _, slug := range stale {
				fmt.Fprintf(out, "  %s %s\n", deleted, slug)
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
