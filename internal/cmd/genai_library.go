package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var libraryFunctionColumns = []output.Column{
	{Header: "FUNCTION", Field: "Function"},
	{Header: "SUMMARY", Field: "Summary"},
}

// libraryFunctionRow is one line of the library table.
type libraryFunctionRow struct {
	Function string
	Summary  string
}

func newGenAILibraryCmd() *cobra.Command {
	var showSource bool
	cmd := &cobra.Command{
		Use:   "library [module.function]",
		Short: "Show the oodle_eval library that code evaluators import",
		Long: `Show the reference of the oodle_eval Python library. Code
evaluators import it to use the built-in checks, the text
helpers and the functions that combine scores:

  from oodle_eval.v1 import metrics, text, combine

With no argument, list each function and its summary. With a
function name, show its signature, its documentation, the
names of the scores it returns and its file:line in the
library. --source prints the Python source of the function
instead. The name can be
"module.function" or only "function". A module name lists the
functions of that module. A runtime class (Score,
EvaluationResult, Scores) or a ctx path (ctx.params) shows its
reference.

  oodle genai library
  oodle genai library metrics.keyword_check
  oodle genai library combine
  oodle genai library keyword_check --source
  oodle genai library files        # the library's source files
  oodle genai library -o json      # the full manifest

This command prints text unless you set -o. With -o csv it
prints the FUNCTION and SUMMARY columns; with -o json or
-o yaml it prints the manifest data.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.GetGenaiCodeEvalLibraryWithResponse(
				cmd.Context(), getInstance(cmd),
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
			lib := resp.JSON200
			format := textOutputFormat(cmd)
			switch format {
			case output.FormatTable, output.FormatCSV,
				output.FormatJSON, output.FormatYAML:
			default:
				return fmt.Errorf(
					"output format %q is not supported here: "+
						"use table, csv, json or yaml", format,
				)
			}

			if len(args) == 0 {
				if showSource {
					return fmt.Errorf(
						"--source needs a function, class or module name",
					)
				}
				if isStructured(format) {
					// The body as served keeps every manifest
					// field, also the fields that the generated
					// type does not declare.
					var raw any
					if err := json.Unmarshal(resp.Body, &raw); err != nil {
						return fmt.Errorf("decoding the manifest: %w", err)
					}
					return printGenAI(cmd, raw, nil)
				}
				return printLibraryTable(cmd, format, lib.Modules, "")
			}
			return showLibraryEntry(cmd, format, lib, args[0], showSource)
		},
	}
	cmd.Flags().BoolVar(
		&showSource, "source", false,
		"Print the Python source of the function, class or module",
	)
	cmd.AddCommand(newGenAILibraryFilesCmd())
	return cmd
}

// textOutputFormat returns the format that -o sets, and table
// when -o is not set. Commands that print text use it instead of
// the detected format, because the detected format is JSON for
// every pipe, and `oodle genai library | less` must still show
// text.
func textOutputFormat(cmd *cobra.Command) output.Format {
	f := cmd.Flags().Lookup("output")
	if f == nil || !f.Changed {
		return output.FormatTable
	}
	return getOutputFormat(cmd)
}

// explicitStructuredOutput is true when the user asked for
// -o json or -o yaml.
func explicitStructuredOutput(cmd *cobra.Command) bool {
	return isStructured(textOutputFormat(cmd))
}

func isStructured(f output.Format) bool {
	return f == output.FormatJSON || f == output.FormatYAML
}

func printLibraryTable(
	cmd *cobra.Command,
	format output.Format,
	modules *[]client.CodeEvalLibModule,
	only string,
) error {
	var rows []libraryFunctionRow
	for _, m := range deref(modules) {
		if only != "" && m.Name != only {
			continue
		}
		for _, f := range deref(m.Functions) {
			rows = append(rows, libraryFunctionRow{
				Function: m.Name + "." + f.Name,
				Summary:  deref(f.Summary),
			})
		}
	}
	return output.Print(
		cmd.OutOrStdout(), format, rows, libraryFunctionColumns,
	)
}

// printLibraryRow prints one entry as a one-row CSV, so that
// -o csv gives the same columns for one entry as for the list.
func printLibraryRow(cmd *cobra.Command, name, doc string) error {
	summary, _, _ := strings.Cut(strings.TrimSpace(doc), "\n")
	return output.Print(
		cmd.OutOrStdout(), output.FormatCSV,
		[]libraryFunctionRow{{Function: name, Summary: summary}},
		libraryFunctionColumns,
	)
}

// showLibraryEntry prints the one entry that name selects. The
// order of the checks is the order of specificity, so that a
// module name lists the module and does not match a function
// of the same name.
func showLibraryEntry(
	cmd *cobra.Command,
	format output.Format,
	lib *client.CodeEvalLibraryResponse,
	name string,
	showSource bool,
) error {
	structured := isStructured(format)
	csv := format == output.FormatCSV
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, lib.Package+".v1.")
	w := cmd.OutOrStdout()

	for _, m := range deref(lib.Modules) {
		if m.Name == name || m.Path == name {
			if showSource {
				file, err := moduleFile(cmd, m.Path)
				if err != nil {
					return err
				}
				return printLibraryFile(cmd, file, false)
			}
			if structured {
				return printPlain(cmd, m)
			}
			if csv {
				return printLibraryTable(cmd, format, lib.Modules, m.Name)
			}
			if doc := strings.TrimSpace(deref(m.Doc)); doc != "" {
				fmt.Fprintf(w, "%s\n\n", doc)
			}
			return printLibraryTable(cmd, output.FormatTable, lib.Modules, m.Name)
		}
	}

	type match struct {
		module string
		fn     client.CodeEvalLibFunction
	}
	var matches []match
	for _, m := range deref(lib.Modules) {
		for _, f := range deref(m.Functions) {
			if name != m.Name+"."+f.Name && name != f.Name {
				continue
			}
			// A package lists a function of its submodule too
			// (metrics and metrics.format both list
			// keyword_check). One definition is one match; the
			// shorter module name is the one to import from.
			dup := false
			for i, have := range matches {
				if deref(have.fn.File) != "" && deref(have.fn.File) == deref(f.File) &&
					deref(have.fn.Line) == deref(f.Line) {
					dup = true
					if len(m.Name) < len(have.module) {
						matches[i].module = m.Name
					}
				}
			}
			if !dup {
				matches = append(matches, match{m.Name, f})
			}
		}
	}
	switch {
	case len(matches) == 1:
		if showSource {
			return printLibrarySource(cmd,
				matches[0].fn.File, matches[0].fn.Line, name)
		}
		if structured {
			return printPlain(cmd, matches[0].fn)
		}
		if csv {
			return printLibraryRow(cmd,
				matches[0].module+"."+matches[0].fn.Name,
				deref(matches[0].fn.Summary))
		}
		return writeLibraryFunction(
			w, lib, matches[0].module, matches[0].fn,
		)
	case len(matches) > 1:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.module + "." + m.fn.Name
		}
		return fmt.Errorf(
			"%q is in more than one module: %s",
			name, strings.Join(names, ", "),
		)
	}

	for _, s := range deref(lib.Runtime) {
		if s.Name == name {
			if showSource {
				return printLibrarySource(cmd, s.File, s.Line, name)
			}
			if structured {
				return printPlain(cmd, s)
			}
			if csv {
				return printLibraryRow(cmd, s.Name, deref(s.Doc))
			}
			sig := deref(s.Signature)
			if sig == "" {
				sig = s.Name
			}
			fmt.Fprintf(w, "%s\n\n%s\n", sig, strings.TrimSpace(deref(s.Doc)))
			if methods := deref(s.Methods); len(methods) > 0 {
				fmt.Fprintln(w, "\nMethods:")
				for _, m := range methods {
					fmt.Fprintf(w, "  %s\n", m.Signature)
					if sum := deref(m.Summary); sum != "" {
						fmt.Fprintf(w, "      %s\n", sum)
					}
				}
			}
			if loc := sourceLocation(s.File, s.Line); loc != "" {
				fmt.Fprintf(w, "\nSource: %s\n", loc)
			}
			return nil
		}
	}
	for _, f := range deref(lib.Context) {
		if f.Path == name || strings.TrimSuffix(f.Path, "()") == name ||
			strings.HasPrefix(f.Path, name+"(") {
			if showSource {
				return fmt.Errorf("%s is a context value and has no source", f.Path)
			}
			if structured {
				return printPlain(cmd, f)
			}
			if csv {
				return printLibraryRow(cmd, f.Path, deref(f.Doc))
			}
			fmt.Fprintf(w, "%s\n\n%s\n", f.Path, strings.TrimSpace(deref(f.Doc)))
			return nil
		}
	}
	return fmt.Errorf(
		"no library entry %q; run `oodle genai library` to list them",
		name,
	)
}

func writeLibraryFunction(
	w io.Writer,
	lib *client.CodeEvalLibraryResponse,
	module string,
	f client.CodeEvalLibFunction,
) error {
	fmt.Fprintf(w, "%s.%s\n\n", module, f.Signature)
	doc := strings.TrimSpace(deref(f.Doc))
	if doc == "" {
		doc = deref(f.Summary)
	}
	if doc != "" {
		fmt.Fprintf(w, "%s\n\n", doc)
	}
	if scores := deref(f.Scores); len(scores) > 0 {
		fmt.Fprintf(w, "Scores: %s\n", strings.Join(scores, ", "))
	}
	if imp := deref(lib.Import); imp != "" {
		fmt.Fprintf(w, "Import: %s\n", imp)
	}
	if loc := sourceLocation(f.File, f.Line); loc != "" {
		fmt.Fprintf(w, "Source: %s\n", loc)
	}
	return nil
}

// sourceLocation writes file:line, or only the file when the
// line is not known.
func sourceLocation(file *string, line *int) string {
	f := deref(file)
	if f == "" {
		return ""
	}
	if l := deref(line); l > 0 {
		return fmt.Sprintf("%s:%d", f, l)
	}
	return f
}

// moduleFile finds the file of a module: <path>.py, or the
// __init__.py of a package, from the library's file list.
func moduleFile(cmd *cobra.Command, modulePath string) (string, error) {
	base := strings.ReplaceAll(modulePath, ".", "/")
	resp, err := getClient(cmd).Inner.ListGenaiCodeEvalLibraryFilesWithResponse(
		cmd.Context(), getInstance(cmd),
	)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	if err := genaiCheck(resp.StatusCode(), resp.HTTPResponse, resp.Body); err != nil {
		return "", err
	}
	if resp.JSON200 == nil {
		return "", errEmptyResponse
	}
	for _, want := range []string{base + ".py", base + "/__init__.py"} {
		for _, f := range deref(resp.JSON200.Files) {
			if f.Path == want {
				return want, nil
			}
		}
	}
	return "", fmt.Errorf(
		"no file for module %s: the library has neither %s.py nor %s/__init__.py",
		modulePath, base, base,
	)
}
