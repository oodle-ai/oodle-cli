package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
)

func TestParseScheduleInterval(t *testing.T) {
	tests := []struct {
		in        string
		wantValue int
		wantUnit  string
		wantErr   bool
	}{
		{in: "30m", wantValue: 30, wantUnit: "minutes"},
		{in: "6h", wantValue: 6, wantUnit: "hours"},
		{in: "1d", wantValue: 1, wantUnit: "days"},
		{in: " 12h ", wantValue: 12, wantUnit: "hours"},
		{in: "365d", wantValue: 365, wantUnit: "days"},
		// A compound duration has no representation in the
		// API's value+unit pair, so it is refused rather than
		// rounded to a cadence nobody asked for.
		{in: "1h30m", wantErr: true},
		{in: "6", wantErr: true},
		{in: "h", wantErr: true},
		{in: "6w", wantErr: true},
		{in: "0h", wantErr: true},
		{in: "-6h", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		value, unit, err := parseScheduleInterval(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf(
					"parseScheduleInterval(%q) = %d %q, want error",
					tt.in, value, unit,
				)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseScheduleInterval(%q): %v", tt.in, err)
			continue
		}
		if value != tt.wantValue || unit != tt.wantUnit {
			t.Errorf(
				"parseScheduleInterval(%q) = %d %q, want %d %q",
				tt.in, value, unit, tt.wantValue, tt.wantUnit,
			)
		}
	}
}

// scheduleShapeCmd returns a command carrying the flags
// applyScheduleShape reads, with the named ones marked as
// supplied. The function branches on Changed(), so a test that
// only sets the variables would not exercise it.
func scheduleShapeCmd(t *testing.T, changed ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "set"}
	var (
		weekdays    []string
		daysOfMonth []string
	)
	cmd.Flags().StringSliceVar(&weekdays, "weekday", nil, "")
	cmd.Flags().StringSliceVar(&daysOfMonth, "day-of-month", nil, "")
	for _, name := range changed {
		if err := cmd.Flags().Set(name, "monday"); err != nil {
			t.Fatalf("Set(%s): %v", name, err)
		}
	}
	return cmd
}

func TestApplyScheduleShapeInfersMode(t *testing.T) {
	var body client.SetGenaiDatasetScheduleJSONRequestBody
	err := applyScheduleShape(
		&body, scheduleShapeCmd(t), "", "6h", nil, "", nil, nil,
	)
	if err != nil {
		t.Fatalf("applyScheduleShape: %v", err)
	}
	if body.Mode == nil || *body.Mode != scheduleModeInterval {
		t.Errorf("Mode = %v, want %q", body.Mode, scheduleModeInterval)
	}
	if body.IntervalValue == nil || *body.IntervalValue != 6 {
		t.Errorf("IntervalValue = %v, want 6", body.IntervalValue)
	}
	if body.IntervalUnit == nil || *body.IntervalUnit != "hours" {
		t.Errorf("IntervalUnit = %v, want hours", body.IntervalUnit)
	}

	body = client.SetGenaiDatasetScheduleJSONRequestBody{}
	err = applyScheduleShape(
		&body, scheduleShapeCmd(t), "", "",
		[]string{"09:00"}, "America/Los_Angeles", nil, nil,
	)
	if err != nil {
		t.Fatalf("applyScheduleShape: %v", err)
	}
	if body.Mode == nil || *body.Mode != scheduleModeCalendar {
		t.Errorf("Mode = %v, want %q", body.Mode, scheduleModeCalendar)
	}
	if body.Timezone == nil || *body.Timezone != "America/Los_Angeles" {
		t.Errorf("Timezone = %v, want America/Los_Angeles", body.Timezone)
	}
}

func TestApplyScheduleShapeRejects(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		every string
		times []string
		want  string
	}{
		{
			name:  "both shapes",
			every: "6h",
			times: []string{"09:00"},
			want:  "give one, not both",
		},
		{
			name: "unknown mode",
			mode: "cron",
			want: "--mode must be",
		},
		{
			name: "calendar without times",
			mode: scheduleModeCalendar,
			want: "needs at least one --time",
		},
		{
			name: "interval without every",
			mode: scheduleModeInterval,
			want: "needs --every",
		},
		{
			name:  "unparseable interval",
			every: "soon",
			want:  "invalid --every",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body client.SetGenaiDatasetScheduleJSONRequestBody
			err := applyScheduleShape(
				&body, scheduleShapeCmd(t), tt.mode, tt.every,
				tt.times, "", nil, nil,
			)
			if err == nil {
				t.Fatalf("applyScheduleShape: want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestApplyScheduleShapeKeepsUnsuppliedFilters pins that an
// omitted --weekday leaves the field alone. An empty list means
// "every day" to the server, so writing one unasked would widen
// a schedule set from a file that names weekdays.
func TestApplyScheduleShapeKeepsUnsuppliedFilters(t *testing.T) {
	from := []string{"monday"}
	body := client.SetGenaiDatasetScheduleJSONRequestBody{
		Weekdays: &from,
	}
	err := applyScheduleShape(
		&body, scheduleShapeCmd(t), "", "6h", nil, "", nil, nil,
	)
	if err != nil {
		t.Fatalf("applyScheduleShape: %v", err)
	}
	if body.Weekdays == nil || len(*body.Weekdays) != 1 {
		t.Errorf("Weekdays = %v, want the file's [monday]", body.Weekdays)
	}

	body = client.SetGenaiDatasetScheduleJSONRequestBody{Weekdays: &from}
	err = applyScheduleShape(
		&body, scheduleShapeCmd(t, "weekday"), "", "6h", nil, "",
		[]string{"friday"}, nil,
	)
	if err != nil {
		t.Fatalf("applyScheduleShape: %v", err)
	}
	if body.Weekdays == nil || (*body.Weekdays)[0] != "friday" {
		t.Errorf("Weekdays = %v, want the flag's [friday]", body.Weekdays)
	}
}

// TestScheduleSetSharesExperimentFlags pins that the schedule
// takes the same config flags as a one-off run, minus the run
// name the server ignores there.
func TestScheduleSetSharesExperimentFlags(t *testing.T) {
	root := NewRootCmd()
	set, _, err := root.Find(
		[]string{"genai", "datasets", "schedule", "set"},
	)
	if err != nil {
		t.Fatalf("Find(schedule set): %v", err)
	}
	run, _, err := root.Find([]string{"genai", "experiments", "run"})
	if err != nil {
		t.Fatalf("Find(experiments run): %v", err)
	}

	shared := []string{
		"dataset-id", "prompt-name", "prompt-version", "prompt-label",
		"prompt-template", "connection-id", "model", "evaluator-id",
		"output-comparer-id", "evaluator-model", "eval-connection-id",
	}
	for _, name := range shared {
		if run.Flags().Lookup(name) == nil {
			t.Errorf("experiments run has no --%s", name)
		}
		if set.Flags().Lookup(name) == nil {
			t.Errorf("schedule set has no --%s", name)
		}
	}
	if set.Flags().Lookup("run-name") != nil {
		t.Error(
			"schedule set offers --run-name, which the server " +
				"ignores: every firing is numbered on its own",
		)
	}
}

func TestExperimentConfigFlagsApplyTo(t *testing.T) {
	cfg := experimentConfigFlags{
		datasetID:         "ds",
		connectionID:      "conn",
		promptName:        "support-reply",
		promptVersion:     3,
		model:             "gpt-4o",
		evaluatorIDs:      []string{"judge"},
		outputComparerIDs: []string{"comparer"},
	}
	// A file's keys survive unless a flag replaces them.
	config := map[string]any{
		"evalConnectionId": "from-file",
		"model":            "from-file",
	}
	cfg.applyTo(config)

	if config[cfgKeyEvalConnectionID] != "from-file" {
		t.Errorf(
			"evalConnectionId = %v, want the file's value kept",
			config[cfgKeyEvalConnectionID],
		)
	}
	if config[cfgKeyModel] != "gpt-4o" {
		t.Errorf("model = %v, want the flag to win", config[cfgKeyModel])
	}
	if config[cfgKeyPromptVersion] != 3 {
		t.Errorf("promptVersion = %v, want 3", config[cfgKeyPromptVersion])
	}
	// The two id lists stay apart: the server rejects an
	// evaluator listed under the wrong one.
	ids, _ := config[cfgKeyEvaluatorIDs].([]string)
	if len(ids) != 1 || ids[0] != "judge" {
		t.Errorf("evaluatorIds = %v, want [judge]", config[cfgKeyEvaluatorIDs])
	}
	comparers, _ := config[cfgKeyOutputComparerIDs].([]string)
	if len(comparers) != 1 || comparers[0] != "comparer" {
		t.Errorf(
			"outputComparerIds = %v, want [comparer]",
			config[cfgKeyOutputComparerIDs],
		)
	}
}

// TestExperimentConfigFlagsAddRules pins that --evaluator-model
// reaches both id lists and does not overwrite what a file already
// said about an evaluator.
func TestExperimentConfigFlagsAddRules(t *testing.T) {
	cfg := experimentConfigFlags{
		evaluatorIDs:      []string{"judge-a", "judge-b"},
		outputComparerIDs: []string{"comparer"},
		evaluatorModel:    "gpt-4o-mini",
	}
	config := map[string]any{
		cfgKeyEvaluatorRules: []any{
			map[string]any{
				"templateId": "judge-a",
				"model":      "from-file",
			},
		},
	}
	cfg.applyTo(config)

	rules, _ := config[cfgKeyEvaluatorRules].([]any)
	if len(rules) != 2 {
		t.Fatalf("evaluatorRules = %v, want one per evaluator", rules)
	}
	byID := map[string]string{}
	for _, entry := range rules {
		rule, _ := entry.(map[string]any)
		id, _ := rule["templateId"].(string)
		model, _ := rule["model"].(string)
		byID[id] = model
	}
	if byID["judge-a"] != "from-file" {
		t.Errorf(
			"judge-a model = %q, want the file's entry kept",
			byID["judge-a"],
		)
	}
	if byID["judge-b"] != "gpt-4o-mini" {
		t.Errorf("judge-b model = %q, want gpt-4o-mini", byID["judge-b"])
	}

	comparerRules, _ := config[cfgKeyComparerRules].([]any)
	if len(comparerRules) != 1 {
		t.Fatalf(
			"outputComparerRules = %v, want one per comparer",
			comparerRules,
		)
	}

	// With no evaluators named by flag there is nothing to fill in,
	// so no empty rule list is written.
	bare := experimentConfigFlags{evaluatorModel: "gpt-4o-mini"}
	empty := map[string]any{}
	bare.applyTo(empty)
	if _, ok := empty[cfgKeyEvaluatorRules]; ok {
		t.Error("evaluatorRules written with no evaluator ids")
	}
}

func TestValidateExperimentConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "no dataset",
			config: map[string]any{},
			want:   "--dataset-id is required",
		},
		{
			name:   "no connection",
			config: map[string]any{"datasetId": "ds"},
			want:   "--connection-id is required",
		},
		{
			name: "no prompt",
			config: map[string]any{
				"datasetId": "ds", "llmConnectionId": "conn",
			},
			want: "--prompt-name or --prompt-template is required",
		},
		{
			name: "complete",
			config: map[string]any{
				"datasetId": "ds", "llmConnectionId": "conn",
				"promptTemplate": "Answer {{input}}",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExperimentConfig(tt.config)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validateExperimentConfig: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateExperimentConfig: want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}
