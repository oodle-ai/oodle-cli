package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var codeLibraryColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "VERSION", Field: "Version"},
	{Header: "DESCRIPTION", Field: "Description"},
	{Header: "UPDATED", Field: "UpdatedAt"},
}

var codeLibraryDetailColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "VERSION", Field: "Version"},
	{Header: "USED BY", Field: "UsedBy"},
	{Header: "UPDATED", Field: "UpdatedAt"},
}

var codeLibraryVersionColumns = []output.Column{
	{Header: "VERSION", Field: "Version"},
	{Header: "CREATED", Field: "CreatedAt"},
	{Header: "CREATED BY", Field: "CreatedBy"},
}

// codeLibraryDetailRow is the table form of one library. The
// source is left out: it does not fit a table row, and
// `-o yaml` prints it as a block.
type codeLibraryDetailRow struct {
	Name      string
	Id        string
	Version   int
	UsedBy    string
	UpdatedAt string
}

func newGenAICodeLibrariesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "code-libraries",
		Aliases: []string{"code-library", "shared-libraries"},
		Short:   "Manage shared Python libraries for code evaluators",
		Long: `Manage shared libraries: Python modules that you write one
time and import from many code evaluators. The Oodle UI shows
them under Shared libraries. Code imports a library by its
name in the "shared" package:

  from shared.acme_text import clean, is_polite

A library is plain Python. It can import oodle_eval and other
shared libraries, with absolute imports only. Each change of
the source adds a version. A code template runs the latest
version, or the version that its libraryPins field sets.

The name cannot change after create, because the code that
imports the library uses it. A name is a lower-case Python
identifier of at most 64 characters.

Commands that take <library> accept the id or the name.
Writes need the same code-eval feature flag and plan as code
templates.`,
	}

	cmd.AddCommand(newGenAICodeLibrariesListCmd())
	cmd.AddCommand(newGenAICodeLibrariesGetCmd())
	cmd.AddCommand(newGenAICodeLibrariesCreateCmd())
	cmd.AddCommand(newGenAICodeLibrariesUpdateCmd())
	cmd.AddCommand(newGenAICodeLibrariesDeleteCmd())
	cmd.AddCommand(newGenAICodeLibrariesVersionsCmd())

	return cmd
}

func listCodeLibraries(cmd *cobra.Command) ([]client.CodeLibrary, error) {
	resp, err := getClient(cmd).Inner.ListGenaiCodeLibrariesWithResponse(
		cmd.Context(), getInstance(cmd),
	)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	if err := genaiCheck(
		resp.StatusCode(), resp.HTTPResponse, resp.Body,
	); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, errEmptyResponse
	}
	return deref(resp.JSON200.Data), nil
}

// uuidPattern matches a library id. A library name cannot have
// a hyphen, so the two never overlap.
var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// resolveCodeLibraryID returns the id for an id or a name. The
// API addresses a library only by id, but people and the code
// that imports a library know it by name.
func resolveCodeLibraryID(cmd *cobra.Command, ref string) (string, error) {
	if uuidPattern.MatchString(ref) {
		return ref, nil
	}
	libs, err := listCodeLibraries(cmd)
	if err != nil {
		return "", err
	}
	for _, l := range libs {
		if l.Name == ref || l.Id == ref {
			return l.Id, nil
		}
	}
	return "", fmt.Errorf(
		"no shared library %q; run `oodle genai code-libraries list` "+
			"to list them", ref,
	)
}

func newGenAICodeLibrariesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List shared libraries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			libs, err := listCodeLibraries(cmd)
			if err != nil {
				return err
			}
			if isTabular(cmd) {
				return printGenAI(cmd, libs, codeLibraryColumns)
			}
			return printPlain(cmd, libs)
		},
	}
}

func newGenAICodeLibrariesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <library>",
		Short: "Get a shared library and what imports it",
		Long: `Get a shared library by id or name. The result includes the
source and usedBy: the code templates and the other shared
libraries that import the library. Each entry has a kind,
"template" or "library".

To edit a library, write it to a file, change it, then update:

  oodle genai code-libraries get acme_text -o yaml > lib.yaml
  $EDITOR lib.yaml
  oodle genai code-libraries update acme_text -f lib.yaml`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveCodeLibraryID(cmd, args[0])
			if err != nil {
				return err
			}
			resp, err := getClient(cmd).Inner.GetGenaiCodeLibraryWithResponse(
				cmd.Context(), getInstance(cmd), id,
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
			if isTabular(cmd) {
				used := make([]string, 0)
				for _, r := range deref(lib.UsedBy) {
					if k := deref(r.Kind); k != "" {
						used = append(used, k+" "+r.Name)
					} else {
						used = append(used, r.Name)
					}
				}
				return printGenAI(cmd, codeLibraryDetailRow{
					Name:      lib.Name,
					Id:        lib.Id,
					Version:   lib.Version,
					UsedBy:    strings.Join(used, ", "),
					UpdatedAt: lib.UpdatedAt,
				}, codeLibraryDetailColumns)
			}
			return printPlain(cmd, lib)
		},
	}
}

// codeLibraryInput is what create and update read. -f gives a
// file with any of these fields; the flags replace them.
type codeLibraryInput struct {
	file        string
	source      string
	name        string
	description string
	// restore is a version whose source becomes the new latest
	// version. Only update has the flag.
	restore int
}

func (in *codeLibraryInput) addTo(cmd *cobra.Command, withName bool) {
	cmd.Flags().StringVarP(
		&in.file, "file", "f", "",
		"JSON or YAML file with name, description and sourceCode",
	)
	cmd.Flags().StringVar(
		&in.source, "source", "",
		"Python file to use as the source, or - to read standard "+
			"input (replaces sourceCode from -f)",
	)
	if withName {
		cmd.Flags().StringVar(
			&in.name, "name", "",
			"Library name (default: the name from -f, else the "+
				"--source file name without .py)",
		)
	}
	cmd.Flags().StringVar(
		&in.description, "description", "",
		"Description (replaces the description from -f)",
	)
}

// read merges the file and the flags into a create body. The
// fields of an update body are a subset, so update uses the
// same merge. sourceGiven is true when -f has a sourceCode field
// or --source is set. Update needs it to tell "no change of the
// source" from "an empty source", which the server must not get
// as an empty PATCH that succeeds and changes nothing.
func (in *codeLibraryInput) read(
	cmd *cobra.Command,
) (body client.CreateCodeLibraryRequest, sourceGiven bool, err error) {
	if in.file != "" {
		if err := readInputFile(in.file, &body); err != nil {
			return body, false, err
		}
		var probe struct {
			SourceCode *string `json:"sourceCode"`
		}
		if err := readInputFile(in.file, &probe); err != nil {
			return body, false, err
		}
		sourceGiven = probe.SourceCode != nil
	}
	if in.source != "" {
		var src []byte
		if in.source == "-" {
			src, err = io.ReadAll(cmd.InOrStdin())
		} else {
			src, err = os.ReadFile(in.source)
		}
		if err != nil {
			return body, false, fmt.Errorf("reading %s: %w", in.source, err)
		}
		body.SourceCode = string(src)
		sourceGiven = true
		if body.Name == "" && in.name == "" && in.source != "-" {
			body.Name = strings.TrimSuffix(
				filepath.Base(in.source), filepath.Ext(in.source),
			)
		}
	}
	if in.name != "" {
		body.Name = in.name
	}
	if cmd.Flags().Changed("description") {
		d := in.description
		body.Description = &d
	}
	if sourceGiven && strings.TrimSpace(body.SourceCode) == "" {
		return body, true, fmt.Errorf(
			"the source is empty: a library needs Python source",
		)
	}
	return body, sourceGiven, nil
}

func newGenAICodeLibrariesCreateCmd() *cobra.Command {
	var in codeLibraryInput
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a shared library",
		Long: `Create a shared library from a Python file, a JSON or YAML
file, or both.

From a Python file (the name is the file name without .py).
With --source - the source comes from standard input, and
--name or the file must give the name:

  oodle genai code-libraries create --source acme_text.py \
    --description "Text helpers for the support agent"

From a YAML file, with the source as a block:

  name: acme_text
  description: Text helpers for the support agent
  sourceCode: |
    def clean(text):
        return " ".join(text.split())

  oodle genai code-libraries create -f acme_text.yaml

The flags replace the values from -f.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if in.file == "" && in.source == "" {
				return fmt.Errorf("one of --file or --source is required")
			}
			body, _, err := in.read(cmd)
			if err != nil {
				return err
			}
			if body.Name == "" {
				return fmt.Errorf("the library needs a name: set --name or name in the file")
			}
			if body.SourceCode == "" {
				return fmt.Errorf("the library needs source: set --source or sourceCode in the file")
			}
			resp, err := getClient(cmd).Inner.CreateGenaiCodeLibraryWithResponse(
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
			if resp.JSON201 == nil {
				return errEmptyResponse
			}
			return printCodeLibrary(cmd, resp.JSON201)
		},
	}
	in.addTo(cmd, true)
	return cmd
}

func newGenAICodeLibrariesUpdateCmd() *cobra.Command {
	var in codeLibraryInput
	cmd := &cobra.Command{
		Use:   "update <library>",
		Short: "Update the source or description of a shared library",
		Long: `Update a shared library. Fields left out are not changed.
A change of the source adds a version. Templates that pin an
older version continue to run that version.

  oodle genai code-libraries update acme_text --source acme_text.py
  oodle genai code-libraries update acme_text -f lib.yaml

--restore <version> saves the source of an older version as a
new version. The history is kept: nothing is deleted, and
templates that run the latest version run the restored code:

  oodle genai code-libraries update acme_text --restore 3

The name cannot change: a name in the file is ignored.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			restoring := cmd.Flags().Changed("restore")
			if restoring && (in.file != "" || in.source != "") {
				return fmt.Errorf(
					"--restore sets the source: do not also give --file or --source",
				)
			}
			if !restoring && in.file == "" && in.source == "" &&
				!cmd.Flags().Changed("description") {
				return fmt.Errorf(
					"one of --file, --source, --description or --restore is required",
				)
			}
			if restoring && in.restore < 1 {
				return fmt.Errorf("--restore needs a version number of 1 or more")
			}
			merged, sourceGiven, err := in.read(cmd)
			if err != nil {
				return err
			}
			var id string
			if restoring {
				if id, err = resolveCodeLibraryID(cmd, args[0]); err != nil {
					return err
				}
				v, err := getCodeLibraryVersion(cmd, id, in.restore)
				if err != nil {
					return err
				}
				merged.SourceCode = deref(v.SourceCode)
				sourceGiven = true
				if strings.TrimSpace(merged.SourceCode) == "" {
					return fmt.Errorf("version %d has no source", in.restore)
				}
			}
			if !sourceGiven && merged.Description == nil {
				return fmt.Errorf(
					"nothing to update: give sourceCode or description " +
						"in the file, --source, or --description",
				)
			}
			if id == "" {
				if id, err = resolveCodeLibraryID(cmd, args[0]); err != nil {
					return err
				}
			}
			body := client.UpdateGenaiCodeLibraryJSONRequestBody{
				Description: merged.Description,
			}
			if sourceGiven {
				body.SourceCode = &merged.SourceCode
			}
			resp, err := getClient(cmd).Inner.UpdateGenaiCodeLibraryWithResponse(
				cmd.Context(), getInstance(cmd), id, body,
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
			return printCodeLibrary(cmd, resp.JSON200)
		},
	}
	in.addTo(cmd, false)
	cmd.Flags().IntVar(
		&in.restore, "restore", 0,
		"Save the source of this older version as a new version",
	)
	return cmd
}

func printCodeLibrary(cmd *cobra.Command, lib *client.CodeLibrary) error {
	return printGenAIObject(cmd, lib, codeLibraryColumns)
}

func newGenAICodeLibrariesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <library>",
		Short: "Delete a shared library and all its versions",
		Long: `Delete a shared library and all its versions.

Oodle refuses the delete (409) while a code template or another
shared library imports the library, and names them with their
kind. Remove the import from each of them, then delete the
library.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveCodeLibraryID(cmd, args[0])
			if err != nil {
				return err
			}
			if !confirmAction(fmt.Sprintf(
				"Delete shared library %q and all its versions?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := getClient(cmd).Inner.DeleteGenaiCodeLibraryWithResponse(
				cmd.Context(), getInstance(cmd), id,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				// A 409 is not a failure of the request: the
				// user must change the importers first. The
				// remedy tells them how.
				var apiErr *api.APIError
				if resp.StatusCode() == http.StatusConflict &&
					errors.As(err, &apiErr) && apiErr.Remedy == "" {
					lib := "shared." + args[0]
					if uuidPattern.MatchString(args[0]) {
						lib = "the library"
					}
					apiErr.Remedy = "remove the import of " + lib +
						" from each template and library that uses it, " +
						"then delete again"
				}
				return err
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"Deleted shared library %s\n", args[0],
			)
			return nil
		},
	}
}

func newGenAICodeLibrariesVersionsCmd() *cobra.Command {
	var version int
	cmd := &cobra.Command{
		Use:   "versions <library>",
		Short: "List the versions of a shared library, or print one",
		Long: `List the versions of a shared library. With --version, print
the source of that version as Python, so that you can compare
it or write it to a file:

  oodle genai code-libraries versions acme_text
  oodle genai code-libraries versions acme_text --version 3 > v3.py

With --version and -o json or -o yaml, print the version with
its metadata.

To restore an older version, save its source as a new version.
The versions after it stay in the history:

  oodle genai code-libraries update acme_text --restore 3

This is the same as:

  oodle genai code-libraries versions acme_text --version 3 > v3.py
  oodle genai code-libraries update acme_text --source v3.py`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveCodeLibraryID(cmd, args[0])
			if err != nil {
				return err
			}
			c := getClient(cmd)
			if !cmd.Flags().Changed("version") {
				resp, err := c.Inner.ListGenaiCodeLibraryVersionsWithResponse(
					cmd.Context(), getInstance(cmd), id,
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
				versions := deref(resp.JSON200.Data)
				if isTabular(cmd) {
					return printGenAI(cmd, versions, codeLibraryVersionColumns)
				}
				return printPlain(cmd, versions)
			}

			resp, err := c.Inner.GetGenaiCodeLibraryVersionWithResponse(
				cmd.Context(), getInstance(cmd), id, version,
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
			if explicitStructuredOutput(cmd) {
				return printPlain(cmd, resp.JSON200)
			}
			src := deref(resp.JSON200.SourceCode)
			if src != "" && !strings.HasSuffix(src, "\n") {
				src += "\n"
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), src)
			return err
		},
	}
	cmd.Flags().IntVar(
		&version, "version", 0,
		"Print the source of this version",
	)
	return cmd
}

// isTabular is true for table and CSV output, where the
// commands print columns instead of the full object.
func isTabular(cmd *cobra.Command) bool {
	switch getOutputFormat(cmd) {
	case output.FormatTable, output.FormatCSV:
		return true
	}
	return false
}
