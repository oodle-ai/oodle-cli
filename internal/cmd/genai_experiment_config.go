package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Experiment config keys. These are the keys the eval-worker
// reads, and they match the constants in
// api-server/apps/llmops/experiments/launch.go.
const (
	cfgKeyDatasetID         = "datasetId"
	cfgKeyRunName           = "runName"
	cfgKeyPromptName        = "promptName"
	cfgKeyPromptVersion     = "promptVersion"
	cfgKeyPromptLabel       = "promptLabel"
	cfgKeyPromptTemplate    = "promptTemplate"
	cfgKeyConnectionID      = "llmConnectionId"
	cfgKeyModel             = "model"
	cfgKeyEvaluatorIDs      = "evaluatorIds"
	cfgKeyOutputComparerIDs = "outputComparerIds"
	cfgKeyEvaluatorRules    = "evaluatorRules"
	cfgKeyComparerRules     = "outputComparerRules"
	cfgKeyEvalConnectionID  = "evalConnectionId"
)

// ruleTemplateIDKey and ruleModelKey are the fields of a rule entry
// the worker reads to decide which model judges with which template.
const (
	ruleTemplateIDKey = "templateId"
	ruleModelKey      = "model"
)

// experimentConfigFlags are the flags that describe *what* an
// experiment runs, as opposed to when.
//
// The same config drives a one-off `experiments run` and the
// recurring `datasets schedule set`, so the flags live here
// rather than on either command: a flag added for one and
// forgotten on the other produces a schedule that cannot
// express a run the CLI can already start.
type experimentConfigFlags struct {
	datasetID         string
	runName           string
	promptName        string
	promptVersion     int
	promptLabel       string
	promptTemplate    string
	connectionID      string
	model             string
	evaluatorIDs      []string
	outputComparerIDs []string
	evaluatorModel    string
	evalConnectionID  string
}

// addTo registers the config flags on cmd.
//
// withRunName is false for a schedule: the server numbers every
// firing on its own and ignores a name, so offering the flag
// there would promise something that does not happen.
func (f *experimentConfigFlags) addTo(
	cmd *cobra.Command, withRunName bool,
) {
	cmd.Flags().StringVar(
		&f.datasetID, "dataset-id", "",
		"Dataset to run over",
	)
	if withRunName {
		cmd.Flags().StringVar(
			&f.runName, "run-name", "",
			"Name for this run (default: next run number)",
		)
	}
	cmd.Flags().StringVar(
		&f.promptName, "prompt-name", "",
		"Managed prompt to run",
	)
	cmd.Flags().IntVar(
		&f.promptVersion, "prompt-version", 0,
		"Prompt version (default: resolve --prompt-label)",
	)
	cmd.Flags().StringVar(
		&f.promptLabel, "prompt-label", "",
		"Prompt label to resolve (default: production)",
	)
	cmd.Flags().StringVar(
		&f.promptTemplate, "prompt-template", "",
		"Literal prompt text, instead of a managed prompt",
	)
	cmd.Flags().StringVar(
		&f.connectionID, "connection-id", "",
		"LLM connection to generate with",
	)
	cmd.Flags().StringVar(
		&f.model, "model", "",
		"Model override for this run",
	)
	cmd.Flags().StringSliceVar(
		&f.evaluatorIDs, "evaluator-id", nil,
		"Evaluator to score the run with (repeatable)",
	)
	cmd.Flags().StringSliceVar(
		&f.outputComparerIDs, "output-comparer-id", nil,
		"Output comparer to score the run against each item's "+
			"expected output (repeatable)",
	)
	cmd.Flags().StringVar(
		&f.evaluatorModel, "evaluator-model", "",
		"Model the named evaluators and output comparers judge "+
			"with, overriding the template's and the eval "+
			"connection's default",
	)
	cmd.Flags().StringVar(
		&f.evalConnectionID, "eval-connection-id", "",
		"LLM connection the evaluators run against",
	)
}

// applyTo overlays the flags onto config, which starts as
// whatever a --file supplied. A flag left empty does not erase
// the file's value.
func (f *experimentConfigFlags) applyTo(config map[string]any) {
	setStr := func(key, val string) {
		if val != "" {
			config[key] = val
		}
	}
	setStr(cfgKeyDatasetID, f.datasetID)
	setStr(cfgKeyRunName, f.runName)
	setStr(cfgKeyPromptName, f.promptName)
	setStr(cfgKeyPromptLabel, f.promptLabel)
	setStr(cfgKeyPromptTemplate, f.promptTemplate)
	setStr(cfgKeyConnectionID, f.connectionID)
	setStr(cfgKeyModel, f.model)
	setStr(cfgKeyEvalConnectionID, f.evalConnectionID)
	if f.promptVersion > 0 {
		config[cfgKeyPromptVersion] = f.promptVersion
	}
	if len(f.evaluatorIDs) > 0 {
		config[cfgKeyEvaluatorIDs] = f.evaluatorIDs
	}
	if len(f.outputComparerIDs) > 0 {
		config[cfgKeyOutputComparerIDs] = f.outputComparerIDs
	}
	if f.evaluatorModel != "" {
		addRules(
			config, cfgKeyEvaluatorRules,
			f.evaluatorIDs, f.evaluatorModel,
		)
		addRules(
			config, cfgKeyComparerRules,
			f.outputComparerIDs, f.evaluatorModel,
		)
	}
}

// addRules gives each id named by flag a rule entry carrying the
// model, leaving alone any entry the file already supplied for it.
//
// A rule entry is the only way to say which model judges with which
// template, so an id named on its own takes whatever the template or
// the eval connection provides. This is what makes a run judge with
// a model of the caller's choosing without defining a rule first.
func addRules(
	config map[string]any,
	key string,
	ids []string,
	model string,
) {
	if len(ids) == 0 {
		return
	}

	existing, _ := config[key].([]any)
	covered := make(map[string]bool, len(existing))
	for _, entry := range existing {
		rule, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := rule[ruleTemplateIDKey].(string); ok {
			covered[id] = true
		}
	}

	rules := existing
	for _, id := range ids {
		if covered[id] {
			continue
		}
		rules = append(rules, map[string]any{
			ruleTemplateIDKey: id,
			ruleModelKey:      model,
		})
	}
	config[key] = rules
}

// validateExperimentConfig reports the mistakes the server
// would otherwise accept and a worker would fail on later.
//
// A queued job that cannot succeed looks healthy until someone
// reads its status, so the cost of catching it here is one
// error message against a run that produced nothing.
func validateExperimentConfig(config map[string]any) error {
	if _, ok := config[cfgKeyDatasetID]; !ok {
		return fmt.Errorf(
			"--dataset-id is required (or %s in --file)",
			cfgKeyDatasetID,
		)
	}
	if _, ok := config[cfgKeyConnectionID]; !ok {
		return fmt.Errorf(
			"--connection-id is required (or %s in --file)",
			cfgKeyConnectionID,
		)
	}
	// A run with no prompt has nothing to send. The server
	// accepts the job anyway and it fails later in the worker,
	// so catch it here — the manage_genai_experiments tool
	// already does.
	_, hasName := config[cfgKeyPromptName]
	_, hasTemplate := config[cfgKeyPromptTemplate]
	if !hasName && !hasTemplate {
		return fmt.Errorf(
			"--prompt-name or --prompt-template is required "+
				"(or %s / %s in --file)",
			cfgKeyPromptName, cfgKeyPromptTemplate,
		)
	}
	return nil
}
