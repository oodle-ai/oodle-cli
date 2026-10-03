package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var libraryFileColumns = []output.Column{
	{Header: "PATH", Field: "Path"},
	{Header: "SIZE", Field: "Size"},
}

func newGenAILibraryFilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "files [path]",
		Short: "List the source files of the oodle_eval library, or print one",
		Long: `List the Python files of the oodle_eval library. With a path
from the list, print that file, so that you can read how a
built-in check works:

  oodle genai library files
  oodle genai library files oodle_eval/v1/metrics/format.py

The list prints as a table unless you set -o. A file prints as
Python unless you set -o json or -o yaml.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return printLibraryFile(cmd, args[0], explicitStructuredOutput(cmd))
			}
			resp, err := getClient(cmd).Inner.ListGenaiCodeEvalLibraryFilesWithResponse(
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
			files := deref(resp.JSON200.Files)
			format := textOutputFormat(cmd)
			if isStructured(format) {
				return printPlain(cmd, files)
			}
			return output.Print(
				cmd.OutOrStdout(), format, files, libraryFileColumns,
			)
		},
	}
}

func fetchLibraryFile(
	cmd *cobra.Command,
	path string,
) (*client.CodeEvalLibraryFileResponse, error) {
	resp, err := getClient(cmd).Inner.GetGenaiCodeEvalLibraryFileWithResponse(
		cmd.Context(), getInstance(cmd), path,
	)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	if err := genaiCheck(
		resp.StatusCode(), resp.HTTPResponse, resp.Body,
	); err != nil {
		// The path goes into the API error's own message, so the
		// output keeps one "Error:" prefix.
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			apiErr.Message = "library file " + path + ": " + apiErr.Message
			return nil, apiErr
		}
		return nil, fmt.Errorf("library file %s: %w", path, err)
	}
	if resp.JSON200 == nil {
		return nil, errEmptyResponse
	}
	return resp.JSON200, nil
}

// printLibraryFile prints one library file as Python, or as the
// API object when structured is true.
func printLibraryFile(cmd *cobra.Command, path string, structured bool) error {
	file, err := fetchLibraryFile(cmd, path)
	if err != nil {
		return err
	}
	if structured {
		return printPlain(cmd, file)
	}
	return writeSource(cmd, file.Source)
}

// printLibrarySource prints the definition at file:line: its
// decorators, the def or class line, and the body up to the next
// top-level statement.
func printLibrarySource(cmd *cobra.Command, file *string, line *int, name string) error {
	if deref(file) == "" || deref(line) <= 0 {
		return fmt.Errorf(
			"the library reference has no source location for %s", name,
		)
	}
	f, err := fetchLibraryFile(cmd, *file)
	if err != nil {
		return err
	}
	src, ok := sliceDefinition(f.Source, *line)
	if !ok {
		return fmt.Errorf(
			"%s:%d is not in the file; the reference and the file "+
				"do not agree", *file, *line,
		)
	}
	return writeSource(cmd, src)
}

func writeSource(cmd *cobra.Command, src string) error {
	if src != "" && !strings.HasSuffix(src, "\n") {
		src += "\n"
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), src)
	return err
}

// topLevelStart matches a column-0 line that starts a new
// top-level statement. A column-0 ")" or "):" that closes a
// multi-line signature does not match, so the signature stays
// with its definition.
var topLevelStart = regexp.MustCompile(
	`^(@|def\s|async\s+def\s|class\s|if\s|[A-Za-z_][A-Za-z0-9_]*\s*(:|=|\())`,
)

// sliceDefinition returns the definition that starts at the
// 1-based line: the decorators above it, and the lines up to the
// next top-level statement. Comments and blank lines at the end
// belong to the next definition, so they are left out.
func sliceDefinition(source string, line int) (string, bool) {
	lines := strings.Split(source, "\n")
	if line < 1 || line > len(lines) {
		return "", false
	}
	start := line - 1
	for start > 0 && strings.HasPrefix(lines[start-1], "@") {
		start--
	}
	end := len(lines)
	for i := line; i < len(lines); i++ {
		if topLevelStart.MatchString(lines[i]) {
			end = i
			break
		}
	}
	for end > line {
		t := lines[end-1]
		if strings.TrimSpace(t) == "" || strings.HasPrefix(t, "#") {
			end--
			continue
		}
		break
	}
	return strings.Join(lines[start:end], "\n") + "\n", true
}
