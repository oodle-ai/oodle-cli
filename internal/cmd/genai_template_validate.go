package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
)

// errTemplateNotValid is returned after the problems are
// printed, so that a script or CI step fails on a template that
// a save would refuse.
var errTemplateNotValid = errors.New("the template is not valid")

func newGenAITemplatesValidateCmd() *cobra.Command {
	var (
		file       string
		dir        string
		ruleParams string
		ruleName   string
		libraries  string
	)
	cmd := &cobra.Command{
		Use:   "validate (-f <file> | -d <dir>)",
		Short: "Check a template file without saving it",
		Long: `Check a template file with every check that a create or an
update runs, and save nothing. The file is the one that
` + "`templates create -f`" + ` reads.

The result tells what the code needs: the evaluators whose
scores it reads, and the shared libraries that it imports, with
the version that would run. The command exits with an error when
the template is not valid, so a CI step can run it:

  oodle genai templates validate -f refund.yaml

--rule-params gives a JSON or YAML file with an evaluator's
values for the settings. The server checks them against the
template's params:

  oodle genai templates validate -f refund.yaml \
    --rule-params params.yaml

--libraries gives a directory of draft shared libraries, or one
.py file. Each file <name>.py is checked in place of the stored
library shared.<name>, so a library change can be checked
before it is saved:

  oodle genai templates validate -f refund.yaml --libraries shared/

-d gives a directory that ` + "`templates pull`" + ` wrote in place of
-f: the code of evaluate.py, the fields of template.yaml, and
the new and changed files of shared/ as draft libraries:

  oodle genai templates validate -d ./refund-check

--rule-name gives the name of the evaluator that would run the
code. Then a code that reads the evaluator's own scores, or a
cycle of reads, is also a problem. The name in the template file
is the template name, so the command does not send it.

This command prints text unless you set -o json or -o yaml.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (file == "") == (dir == "") {
				return fmt.Errorf("give one of -f <file> or -d <dir>")
			}
			var body client.ValidateGenaiEvaluatorJSONRequestBody
			if dir != "" {
				ws, err := loadWorkspace(dir)
				if err != nil {
					return err
				}
				if err := ws.templateBody(&body); err != nil {
					return err
				}
				created, changed := ws.changedLibraries()
				if drafts := append(created, changed...); len(drafts) > 0 {
					libs := map[string]string{}
					for _, name := range drafts {
						libs[name] = ws.shared[name]
					}
					body.Libraries = &libs
				}
				file = filepath.Join(dir, wsTemplate)
			} else if err := readInputFile(file, &body); err != nil {
				return err
			}
			body.Name = optStr(ruleName)
			if ruleParams != "" {
				var values map[string]any
				if err := readInputFile(ruleParams, &values); err != nil {
					return err
				}
				body.RuleParams = &values
			}
			if libraries != "" {
				drafts, err := readDraftLibraries(libraries)
				if err != nil {
					return err
				}
				if body.Libraries != nil {
					for name, src := range *body.Libraries {
						if _, ok := drafts[name]; !ok {
							drafts[name] = src
						}
					}
				}
				body.Libraries = &drafts
			}
			if body.Type == "" {
				return fmt.Errorf(
					"%s has no type: set llm, code or output_comparer", file,
				)
			}

			resp, err := getClient(cmd).Inner.ValidateGenaiEvaluatorWithResponse(
				cmd.Context(), getInstance(cmd), body,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			result := resp.JSON200
			if explicitStructuredOutput(cmd) {
				if err := printPlain(cmd, result); err != nil {
					return err
				}
			} else {
				writeValidation(cmd.OutOrStdout(), result)
			}
			if !result.Valid {
				// Cobra would print the usage after an error. The
				// problems are the useful part, and they are
				// already printed.
				cmd.SilenceUsage = true
				return errTemplateNotValid
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the template",
	)
	cmd.Flags().StringVarP(
		&dir, "dir", "d", "",
		"Directory that `templates pull` wrote, in place of -f",
	)
	cmd.Flags().StringVar(
		&ruleParams, "rule-params", "",
		"JSON or YAML file with an evaluator's values for the settings",
	)
	cmd.Flags().StringVar(
		&ruleName, "rule-name", "",
		"Name of the evaluator that would run the code",
	)
	cmd.Flags().StringVar(
		&libraries, "libraries", "",
		"Directory of <name>.py draft shared libraries, or one .py file",
	)
	return cmd
}

// readDraftLibraries reads each <name>.py of a directory, or one
// .py file, as name to source. A name that the server would
// refuse is an error here, before the request.
func readDraftLibraries(path string) (map[string]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	files := []string{path}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		files = nil
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") &&
				e.Name() != "__init__.py" {
				files = append(files, filepath.Join(path, e.Name()))
			}
		}
	}
	out := map[string]string{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".py")
		if !strings.HasSuffix(f, ".py") || !libraryNamePattern.MatchString(name) ||
			name == "oodle_eval" {
			return nil, fmt.Errorf(
				"%s: a library file is <name>.py, where the name is a "+
					"lower-case Python identifier and not oodle_eval", f,
			)
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		out[name] = string(b)
	}
	return out, nil
}

func writeValidation(w io.Writer, r *client.ValidateEvalTemplateResponse) {
	if r.Valid {
		fmt.Fprintln(w, "Valid.")
	} else {
		fmt.Fprintln(w, "Not valid:")
		for _, p := range deref(r.Problems) {
			if p.Field != "" {
				fmt.Fprintf(w, "  - %s: %s\n", p.Field, p.Message)
			} else {
				fmt.Fprintf(w, "  - %s\n", p.Message)
			}
		}
	}
	if refs := deref(r.ScoreInputs); len(refs) > 0 {
		names := make([]string, len(refs))
		for i, ref := range refs {
			names[i] = fmt.Sprintf("%s (%s)", ref.Name, ref.Id)
		}
		fmt.Fprintf(w, "Reads scores from: %s\n", strings.Join(names, ", "))
	}
	if libs := deref(r.Libraries); len(libs) > 0 {
		names := make([]string, len(libs))
		for i, l := range libs {
			if deref(l.Draft) {
				names[i] = l.Name + " (draft)"
			} else {
				names[i] = fmt.Sprintf("%s v%d", l.Name, l.Version)
			}
		}
		fmt.Fprintf(w, "Shared libraries: %s\n", strings.Join(names, ", "))
	}
	if notes := deref(r.Notes); len(notes) > 0 {
		fmt.Fprintln(w, "Notes:")
		for _, n := range notes {
			fmt.Fprintf(w, "  - %s\n", n)
		}
	}
}
