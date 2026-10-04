package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// The files of a workspace. The names are a contract with the
// user's editor and agent, so a change breaks existing
// workspaces.
const (
	wsEvaluate  = "evaluate.py"
	wsTemplate  = "template.yaml"
	wsShared    = "shared"
	wsLibrary   = "oodle_eval"
	wsBuiltins  = "__builtins__.pyi"
	wsPyright   = "pyrightconfig.json"
	wsReadme    = "README.md"
	wsMetaDir   = ".oodle"
	wsMeta      = ".oodle/template.yaml"
	wsLock      = ".oodle/shared.lock.yaml"
	wsNewSource = `from oodle_eval.v1 import metrics, text, combine


def evaluate(ctx: EvaluationContext):
    reply = text.reply(ctx)
    return EvaluationResult(scores=[
        Score(
            name="has_reply",
            value=bool(reply.strip()),
            data_type="BOOLEAN",
            higher_is_better=True,
        ),
    ])
`
)

// workspaceMeta records which template a workspace pushes to. ID
// is empty until the first push of a workspace made from a
// starter, a managed template or "new".
type workspaceMeta struct {
	ID   string `yaml:"id,omitempty"`
	From string `yaml:"from,omitempty"`
	// Files is the SHA256 of evaluate.py and template.yaml at the
	// last pull or push. A pull with --force uses it to find
	// local changes that it must not replace.
	Files map[string]string `yaml:"files,omitempty"`
}

// sharedLock records each shared library as pulled. Push uses it
// to find the files that the user changed (SHA256) and to refuse
// to replace a server change made after the pull (Latest).
type sharedLock struct {
	Libraries map[string]lockEntry `yaml:"libraries"`
}

type lockEntry struct {
	// Version is the version in the file: the pin, else the
	// latest.
	Version int `yaml:"version"`
	// Latest is the server's latest version at pull or push.
	Latest int    `yaml:"latest"`
	SHA256 string `yaml:"sha256"`
}

// templateFileKeys are the create-body fields that template.yaml
// holds, in the order a reader wants. sourceCode is not here:
// evaluate.py holds it.
var templateFileKeys = []string{
	"name", "type", "sourceCodeLanguage", "scoreType",
	"higherIsBetter", "cleanValue", "params", "libraryPins",
}

// libraryNamePattern is the server's rule for a library name. A
// file under shared/ with another name cannot be pushed, so push
// refuses it before any write.
var libraryNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// These follow the import rules of the server, so that the CLI
// finds the same imports that the server finds.
var (
	// `from shared import (a,\n b)` spans lines, so it is read
	// from the whole text before the source is split.
	fromSharedParenRE  = regexp.MustCompile(`(?s)\bfrom\s+shared\s+import\s*\(([^)]*)\)`)
	fromSharedModuleRE = regexp.MustCompile(`^from\s+shared\.([A-Za-z_]\w*)\s+import\b`)
	fromSharedRE       = regexp.MustCompile(`^from\s+shared\s+import\s+(.+)$`)
	importStmtRE       = regexp.MustCompile(`^import\s+(.+)$`)
	sharedModuleRE     = regexp.MustCompile(`^shared\.([A-Za-z_]\w*)`)
)

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newGenAITemplatesPullCmd() *cobra.Command {
	var force, discard bool
	cmd := &cobra.Command{
		Use:   "pull <template-id | starter:<id> | new> <dir>",
		Short: "Write a code template to a local directory to edit it",
		Long: `Write a code template to a directory as real files, so that an
editor or an agent (Claude Code, Cursor, VS Code) can work on
it with type checks and autocomplete:

  evaluate.py              the code; sourceCode of the template
  template.yaml            the other template fields: name,
                           params, libraryPins, scoreType ...
  shared/<name>.py         each shared library of the instance,
                           at the template's pin, else the latest
  oodle_eval/              the library reference source (read-only)
  __builtins__.pyi         the names the sandbox gives the code
  pyrightconfig.json       type checks for Pyright and Pylance
  README.md                the layout, the rules and the commands
  .oodle/                  what was pulled, for push

The source can be a template id, "starter:<starter-id>" for a
starter (see ` + "`templates starters`" + `), or "new" for an empty
evaluator. A managed template or a starter is pulled as a new
template: the first push creates it.

  oodle genai templates pull starter:keyword-check ./refund-check
  oodle genai templates pull <template-id> ./refund-check

The command refuses a directory that is not empty, unless you
set --force. --force replaces the files that pull writes and
keeps other files. It does not replace your changes: when
evaluate.py, template.yaml or a file in shared/ differs from the
last pull or push, the command lists those files and stops.
--discard-local replaces them too, and your changes are lost.

Nothing in the directory changes until every file is
downloaded, so a failed pull leaves the directory as it was.`,
		Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if discard {
				force = true
			}
			return pullWorkspace(cmd, args[0], args[1], force, discard)
		},
	}
	cmd.Flags().BoolVar(
		&force, "force", false,
		"Write into a directory that is not empty; keep local changes",
	)
	cmd.Flags().BoolVar(
		&discard, "discard-local", false,
		"With --force, also replace files that you changed since the pull",
	)
	return cmd
}

// pulledTemplate is the template part of a pull.
type pulledTemplate struct {
	fields map[string]any
	source string
	meta   workspaceMeta
}

// pythonKeywords cannot be module names: an import of one is a
// syntax error. The server refuses them too.
var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true,
	"class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true,
	"for": true, "from": true, "global": true, "if": true,
	"import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true,
	"raise": true, "return": true, "try": true, "while": true,
	"with": true, "yield": true,
}

// validLibraryName applies the server's rule for a library name.
// A name also becomes a file name under shared/, so a name that
// fails the rule is never used as a path.
func validLibraryName(name string) bool {
	return libraryNamePattern.MatchString(name) && !pythonKeywords[name] &&
		name != "oodle_eval" && name != "shared"
}

// serverRelPath checks a relative path that the server supplies
// before it names a file on the user's disk. It refuses a
// backslash (a separator on Windows), a drive or absolute path,
// and any empty, "." or ".." segment, so that the file stays
// under prefix on every platform.
func serverRelPath(p, prefix string) error {
	bad := p == "" || strings.ContainsAny(p, "\\:") ||
		strings.HasPrefix(p, "/") || filepath.IsAbs(p) ||
		!strings.HasPrefix(p, prefix+"/")
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			bad = true
		}
	}
	if bad {
		return fmt.Errorf("the server sent the path %q, which is not under %s/", p, prefix)
	}
	return nil
}

func pullWorkspace(cmd *cobra.Command, ref, dir string, force, discard bool) error {
	w := cmd.OutOrStdout()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) > 0 && !force {
		return fmt.Errorf("%s is not empty; use --force to write into it", dir)
	}
	tpl, err := resolvePullSource(cmd, ref)
	if err != nil {
		return err
	}

	// Every file goes to a staging directory next to dir first.
	// A failure while downloading then leaves dir as it was, and
	// the final moves are renames in one file system.
	parent := filepath.Dir(filepath.Clean(dir))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".oodle-pull-")
	if err != nil {
		return err
	}
	defer removeTree(stage)
	// MkdirTemp makes the directory private (0700). A new
	// workspace is this directory after the rename, so it gets
	// the mode of a directory that the user makes.
	if err := os.Chmod(stage, 0o755); err != nil {
		return err
	}

	// Shared libraries: the pinned version where the template
	// pins one, because that is the code that the template runs.
	libs, err := listCodeLibraries(cmd)
	if err != nil {
		return err
	}
	pins := templatePins(tpl.fields)
	lock := sharedLock{Libraries: map[string]lockEntry{}}
	if err := os.MkdirAll(filepath.Join(stage, wsShared), 0o755); err != nil {
		return err
	}
	for _, l := range libs {
		if !validLibraryName(l.Name) {
			fmt.Fprintf(w, "Warning: skipped the shared library %q: "+
				"the name is not a valid library name.\n", l.Name)
			continue
		}
		src, version := l.SourceCode, l.Version
		if pin, ok := pins[l.Name]; ok && pin != l.Version {
			v, err := getCodeLibraryVersion(cmd, l.Id, pin)
			if err != nil {
				return fmt.Errorf("shared library %s v%d: %w", l.Name, pin, err)
			}
			src, version = deref(v.SourceCode), v.Version
		}
		if err := writeFile(stage, wsShared+"/"+l.Name+".py", src); err != nil {
			return err
		}
		lock.Libraries[l.Name] = lockEntry{
			Version: version, Latest: l.Version, SHA256: sha(src),
		}
	}

	nFiles, err := pullLibraryReference(cmd, stage)
	if err != nil {
		return err
	}

	fileYAML, err := templateFileYAML(tpl.fields)
	if err != nil {
		return err
	}
	// The source is written as it is, byte for byte, so that a
	// push of an unchanged directory sends the same source back.
	tpl.meta.Files = map[string]string{
		wsEvaluate: sha(tpl.source), wsTemplate: sha(fileYAML),
	}
	metaYAML, err := yaml.Marshal(tpl.meta)
	if err != nil {
		return err
	}
	lockYAML, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	files := []struct{ name, content string }{
		{wsEvaluate, tpl.source},
		{wsTemplate, fileYAML},
		{wsBuiltins, builtinsStub},
		{wsPyright, pyrightConfig},
		{wsReadme, workspaceReadme},
		{wsMeta, string(metaYAML)},
		{wsLock, string(lockYAML)},
	}
	for _, f := range files {
		if err := writeFile(stage, f.name, f.content); err != nil {
			return err
		}
	}

	if len(entries) == 0 {
		// An empty directory that exists is replaced whole.
		if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(stage, dir); err != nil {
			return err
		}
	} else if err := installOver(cmd, stage, dir, lock, discard); err != nil {
		return err
	}

	fmt.Fprintf(w,
		"Pulled %q into %s: %s, %s, %d shared libraries, %d library files.\n",
		tpl.fields["name"], dir, wsEvaluate, wsTemplate, len(lock.Libraries), nFiles,
	)
	if tpl.meta.ID == "" {
		fmt.Fprintln(w, "This is a new template: the first push creates it.")
	}
	return nil
}

// localEdits lists the files of dir that differ from the last
// pull or push, and that the new pull would replace or remove. A
// file with no record (dir was not made by pull) counts as an
// edit, because nothing shows that it is safe to replace.
func localEdits(dir string, newLock sharedLock) (edited []string, stale []string) {
	var meta workspaceMeta
	if b, err := os.ReadFile(filepath.Join(dir, wsMeta)); err == nil {
		_ = yaml.Unmarshal(b, &meta)
	}
	var old sharedLock
	if b, err := os.ReadFile(filepath.Join(dir, wsLock)); err == nil {
		_ = yaml.Unmarshal(b, &old)
	}
	for _, name := range []string{wsEvaluate, wsTemplate} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if meta.Files[name] == "" || meta.Files[name] != sha(string(b)) {
			edited = append(edited, name)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, wsShared))
	for _, e := range entries {
		file := e.Name()
		if e.IsDir() || !strings.HasSuffix(file, ".py") || file == "__init__.py" {
			continue
		}
		name := strings.TrimSuffix(file, ".py")
		b, err := os.ReadFile(filepath.Join(dir, wsShared, file))
		if err != nil {
			continue
		}
		entry, pulled := old.Libraries[name]
		_, incoming := newLock.Libraries[name]
		changed := !pulled || entry.SHA256 != sha(string(b))
		switch {
		case changed && (incoming || pulled):
			// The pull would replace the edit, or the library is
			// gone from the server and the edit would stay behind
			// as a new library.
			edited = append(edited, wsShared+"/"+file)
		case !changed && !incoming:
			// Deleted on the server, and not changed here.
			stale = append(stale, wsShared+"/"+file)
		}
	}
	return edited, stale
}

// installOver moves the staged files into a directory that is
// not empty.
func installOver(cmd *cobra.Command, stage, dir string, lock sharedLock, discard bool) error {
	w := cmd.OutOrStdout()
	edited, stale := localEdits(dir, lock)
	if len(edited) > 0 && !discard {
		return fmt.Errorf(
			"these files changed since the last pull or push:\n  %s\n"+
				"Nothing changed. Push them, or copy them to keep them, then pull "+
				"with --discard-local to replace them with the server's version",
			strings.Join(edited, "\n  "),
		)
	}
	// The reference is replaced whole, so that a file that the
	// library no longer has does not stay behind.
	removeTree(filepath.Join(dir, wsLibrary))
	err := filepath.WalkDir(stage, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(stage, p)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			if rel == wsLibrary {
				if err := os.Rename(p, target); err != nil {
					return err
				}
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		_ = os.Chmod(target, 0o644)
		return os.Rename(p, target)
	})
	if err != nil {
		return err
	}
	for _, rel := range stale {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			return err
		}
		fmt.Fprintf(w, "Removed %s: the library is not on the server now.\n", rel)
	}
	for _, rel := range edited {
		name := strings.TrimSuffix(strings.TrimPrefix(rel, wsShared+"/"), ".py")
		if strings.HasPrefix(rel, wsShared+"/") {
			if _, ok := lock.Libraries[name]; !ok {
				_ = os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
			}
		}
		fmt.Fprintf(w, "Replaced your change in %s.\n", rel)
	}
	// A new file that the server does not have stays: it is a
	// library that the user has not pushed yet.
	entries, _ := os.ReadDir(filepath.Join(dir, wsShared))
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".py")
		if strings.HasSuffix(e.Name(), ".py") && e.Name() != "__init__.py" {
			if _, ok := lock.Libraries[name]; !ok {
				fmt.Fprintf(w,
					"Note: %s/%s is not on the server; the next push creates it.\n",
					wsShared, e.Name())
			}
		}
	}
	return nil
}

// removeTree removes a directory with read-only files in it.
// Windows cannot remove a read-only file, so each file is made
// writable first.
func removeTree(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

func resolvePullSource(cmd *cobra.Command, ref string) (*pulledTemplate, error) {
	switch {
	case ref == "new":
		return &pulledTemplate{
			fields: map[string]any{
				"name": "New code evaluator", "type": "code",
				"sourceCodeLanguage": "python",
			},
			source: wsNewSource,
			meta:   workspaceMeta{From: "new"},
		}, nil
	case strings.HasPrefix(ref, "starter:"):
		id := strings.TrimPrefix(ref, "starter:")
		resp, err := getClient(cmd).Inner.ListGenaiCodeEvalStartersWithResponse(
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
		for _, s := range deref(resp.JSON200.Data) {
			if s.Id != id {
				continue
			}
			fields := map[string]any{
				"name": s.Name, "type": "code", "sourceCodeLanguage": "python",
			}
			if st := starterScoreType(s); st != "" {
				fields["scoreType"] = st
			}
			if hib := s.PrimaryHigherIsBetter; hib != nil {
				fields["higherIsBetter"] = *hib
			}
			if params := deref(s.Params); len(params) > 0 {
				plain, err := toPlainValue(params)
				if err != nil {
					return nil, err
				}
				fields["params"] = plain
			}
			return &pulledTemplate{
				fields: fields, source: s.SourceCode,
				meta: workspaceMeta{From: ref},
			}, nil
		}
		return nil, fmt.Errorf(
			"no starter %q; run `oodle genai templates starters` to list them", id,
		)
	}

	resp, err := getClient(cmd).Inner.GetGenaiEvaluatorWithResponse(
		cmd.Context(), getInstance(cmd), ref,
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
	t := resp.JSON200
	if t.Type != "code" {
		return nil, fmt.Errorf(
			"template %s is of type %q; pull works for code templates only",
			ref, t.Type,
		)
	}
	plain, err := toPlainValue(t)
	if err != nil {
		return nil, err
	}
	all, _ := plain.(map[string]any)
	fields := map[string]any{}
	for _, k := range templateFileKeys {
		if v, ok := all[k]; ok && v != nil {
			fields[k] = v
		}
	}
	meta := workspaceMeta{ID: t.Id}
	// A managed template is read-only, so a push makes a new
	// template from it.
	if strings.HasPrefix(t.Id, "oodle-managed-") {
		meta = workspaceMeta{From: t.Id}
	}
	return &pulledTemplate{fields: fields, source: deref(t.SourceCode), meta: meta}, nil
}

func getCodeLibraryVersion(
	cmd *cobra.Command,
	id string,
	version int,
) (*client.CodeLibraryVersion, error) {
	resp, err := getClient(cmd).Inner.GetGenaiCodeLibraryVersionWithResponse(
		cmd.Context(), getInstance(cmd), id, version,
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
	return resp.JSON200, nil
}

// pullLibraryReference writes the oodle_eval source files and
// makes them read-only: they are what the sandbox runs, and an
// edit there changes nothing on the server.
func pullLibraryReference(cmd *cobra.Command, dir string) (int, error) {
	resp, err := getClient(cmd).Inner.ListGenaiCodeEvalLibraryFilesWithResponse(
		cmd.Context(), getInstance(cmd),
	)
	if err != nil {
		return 0, fmt.Errorf("API request failed: %w", err)
	}
	if err := genaiCheck(
		resp.StatusCode(), resp.HTTPResponse, resp.Body,
	); err != nil {
		return 0, err
	}
	if resp.JSON200 == nil {
		return 0, errEmptyResponse
	}
	n := 0
	for _, f := range deref(resp.JSON200.Files) {
		// The path comes from the server and names a file on
		// the user's disk, so it must stay inside oodle_eval/.
		if err := serverRelPath(f.Path, wsLibrary); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Warning: skipped a library file: %v.\n", err)
			continue
		}
		file, err := fetchLibraryFile(cmd, f.Path)
		if err != nil {
			return n, err
		}
		if err := writeFile(dir, f.Path, file.Source); err != nil {
			return n, err
		}
		if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(f.Path)), 0o444); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func writeFile(dir, rel, content string) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	// A second check on the local side: the joined path must stay
	// inside dir, whatever the separators of the platform.
	if r, err := filepath.Rel(dir, p); err != nil || r == ".." ||
		strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is not inside %s", rel, dir)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// A file from an earlier pull can be read-only.
	_ = os.Chmod(p, 0o644)
	return os.WriteFile(p, []byte(content), 0o644)
}

func withNewline(s string) string {
	if s != "" && !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}

// toPlainValue gives v in its JSON form: maps, lists and scalars
// with the API's key names.
func toPlainValue(v any) (any, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var plain any
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return nil, err
	}
	return dropNulls(plain), nil
}

func templatePins(fields map[string]any) map[string]int {
	out := map[string]int{}
	pins, _ := fields["libraryPins"].(map[string]any)
	for k, v := range pins {
		if f, ok := v.(float64); ok {
			out[k] = int(f)
		}
	}
	return out
}

// templateFileYAML writes the fields in templateFileKeys order,
// then any other field in name order.
func templateFileYAML(fields map[string]any) (string, error) {
	var node yaml.Node
	node.Kind = yaml.MappingNode
	keys := append([]string{}, templateFileKeys...)
	var extra []string
	for k := range fields {
		if !containsString(templateFileKeys, k) && k != "sourceCode" {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range append(keys, extra...) {
		v, ok := fields[k]
		if !ok || v == nil {
			continue
		}
		val, err := orderedNode(v, k == "params")
		if err != nil {
			return "", err
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k}, val)
	}
	out, err := output.MarshalYAML(&node)
	if err != nil {
		return "", err
	}
	return "# The template fields. The code is in evaluate.py.\n" +
		"# Push with: oodle genai templates push .\n" + string(out), nil
}

// paramKeyOrder is the order of a setting's fields in
// template.yaml: what it is before its default and options.
var paramKeyOrder = []string{
	"name", "type", "label", "description", "default", "required", "options",
}

// orderedNode encodes v. For the params list, each setting's
// fields follow paramKeyOrder; yaml.v3 would sort them, and put
// "default" before "name".
func orderedNode(v any, params bool) (*yaml.Node, error) {
	list, ok := v.([]any)
	if !params || !ok {
		var n yaml.Node
		err := n.Encode(v)
		return &n, err
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			var n yaml.Node
			if err := n.Encode(item); err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, &n)
			continue
		}
		keys := []string{}
		for _, k := range paramKeyOrder {
			if _, ok := m[k]; ok {
				keys = append(keys, k)
			}
		}
		var rest []string
		for k := range m {
			if !containsString(paramKeyOrder, k) {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		entry := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range append(keys, rest...) {
			var val yaml.Node
			if err := val.Encode(m[k]); err != nil {
				return nil, err
			}
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k}, &val)
		}
		seq.Content = append(seq.Content, entry)
	}
	return seq, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// workspace is a directory that pull wrote.
type workspace struct {
	dir    string
	fields map[string]any
	source string
	meta   workspaceMeta
	lock   sharedLock
	// shared is name to source for each shared/<name>.py.
	shared map[string]string
}

func loadWorkspace(dir string) (*workspace, error) {
	ws := &workspace{dir: dir, shared: map[string]string{}}
	read := func(rel string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(
				"%s has no %s; make the directory with "+
					"`oodle genai templates pull`", dir, rel,
			)
		}
		return b, err
	}
	src, err := read(wsEvaluate)
	if err != nil {
		return nil, err
	}
	ws.source = string(src)
	if err := readInputFile(filepath.Join(dir, wsTemplate), &ws.fields); err != nil {
		return nil, err
	}
	delete(ws.fields, "sourceCode")
	if b, err := read(wsMeta); err == nil {
		if err := yaml.Unmarshal(b, &ws.meta); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", wsMeta, err)
		}
	} else {
		return nil, err
	}
	if b, err := read(wsLock); err == nil {
		if err := yaml.Unmarshal(b, &ws.lock); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", wsLock, err)
		}
	} else {
		return nil, err
	}
	if ws.lock.Libraries == nil {
		ws.lock.Libraries = map[string]lockEntry{}
	}
	entries, err := os.ReadDir(filepath.Join(dir, wsShared))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".py") || name == "__init__.py" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, wsShared, name))
		if err != nil {
			return nil, err
		}
		ws.shared[strings.TrimSuffix(name, ".py")] = string(b)
	}
	return ws, nil
}

// changedLibraries returns the shared libraries whose file is new
// or differs from the pull, in name order. A library that is
// the same as at pull is not sent: the server already has it.
func (ws *workspace) changedLibraries() (created, changed []string) {
	for name, src := range ws.shared {
		entry, ok := ws.lock.Libraries[name]
		switch {
		case !ok:
			created = append(created, name)
		case sha(src) != entry.SHA256:
			changed = append(changed, name)
		}
	}
	sort.Strings(created)
	sort.Strings(changed)
	return created, changed
}

// imports returns the shared libraries that evaluate.py imports,
// directly or through the local shared files.
func (ws *workspace) imports() []string {
	seen := map[string]bool{}
	queue := sharedImports(ws.source)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		queue = append(queue, sharedImports(ws.shared[name])...)
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// sharedImports lists the shared libraries that Python source
// imports: `import shared.a, shared.b as x`, `from shared.a
// import y`, and `from shared import a, b`, also in parentheses
// over many lines. It reads text, not a syntax tree, so it errs
// toward finding an import.
func sharedImports(source string) []string {
	var out []string
	add := func(name string) {
		if name != "" && !containsString(out, name) {
			out = append(out, name)
		}
	}
	addList := func(list string) {
		for _, item := range strings.Split(list, ",") {
			if fields := strings.Fields(item); len(fields) > 0 {
				add(fields[0])
			}
		}
	}
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		if at := strings.Index(line, "#"); at >= 0 {
			lines[i] = line[:at]
		}
	}
	text := strings.Join(lines, "\n")
	text = strings.ReplaceAll(text, "\\\n", " ")
	for _, m := range fromSharedParenRE.FindAllStringSubmatch(text, -1) {
		addList(m[1])
	}
	text = fromSharedParenRE.ReplaceAllString(text, "")
	for _, line := range strings.Split(text, "\n") {
		for _, stmt := range strings.Split(line, ";") {
			stmt = strings.TrimSpace(stmt)
			if m := fromSharedModuleRE.FindStringSubmatch(stmt); m != nil {
				add(m[1])
				continue
			}
			if m := fromSharedRE.FindStringSubmatch(stmt); m != nil {
				addList(m[1])
				continue
			}
			if m := importStmtRE.FindStringSubmatch(stmt); m != nil {
				for _, item := range strings.Split(m[1], ",") {
					if mod := sharedModuleRE.FindStringSubmatch(strings.TrimSpace(item)); mod != nil {
						add(mod[1])
					}
				}
			}
		}
	}
	return out
}

func (ws *workspace) saveMeta() error {
	b, err := yaml.Marshal(ws.meta)
	if err != nil {
		return err
	}
	return writeFile(ws.dir, wsMeta, string(b))
}

func (ws *workspace) saveLock() error {
	b, err := yaml.Marshal(ws.lock)
	if err != nil {
		return err
	}
	return writeFile(ws.dir, wsLock, string(b))
}

// templateBody is the template as a create or update body: the
// fields of template.yaml and the source of evaluate.py.
func (ws *workspace) templateBody(v any) error {
	return ws.body(v, false)
}

// clearedFields are the fields that an update clears when
// template.yaml leaves them out, with their empty values. An
// update changes only the fields that it sends, so without these
// a setting deleted from the file would stay on the server.
var clearedFields = map[string]any{
	"params": []any{}, "libraryPins": map[string]any{}, "cleanValue": "",
}

// updateBody is the template as an update body, with the fields
// that template.yaml leaves out sent empty.
func (ws *workspace) updateBody(v any) error {
	return ws.body(v, true)
}

func (ws *workspace) body(v any, clear bool) error {
	fields := map[string]any{}
	if clear {
		for k, empty := range clearedFields {
			fields[k] = empty
		}
	}
	for k, val := range ws.fields {
		fields[k] = val
	}
	fields["sourceCode"] = ws.source
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func newGenAITemplatesPushCmd() *cobra.Command {
	var (
		dryRun bool
		force  bool
		pin    bool
	)
	cmd := &cobra.Command{
		Use:   "push <dir>",
		Short: "Save a local template directory to Oodle",
		Long: `Save a directory that ` + "`templates pull`" + ` wrote. In order:

 1. Validate the template (evaluate.py and template.yaml), with
    the new and changed files in shared/ as draft libraries, and
    stop on problems.
 2. Create each new file in shared/ as a shared library, and
    update each library whose file changed since the pull. A
    library that changed on the server since the pull is
    refused, so that the push does not replace another
    person's change. Compare the two, then pull again or push
    with --force.
 3. Create the template, or update the template that the
    directory came from. The template keeps its libraryPins,
    unless you set --pin-libraries: then it pins each library
    that the code imports to the version that it has now.

  oodle genai templates push ./refund-check --dry-run
  oodle genai templates push ./refund-check

--dry-run prints the plan and changes nothing.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return pushWorkspace(cmd, args[0], dryRun, force, pin)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan and change nothing")
	cmd.Flags().BoolVar(
		&force, "force", false,
		"Update shared libraries that changed on the server since the pull",
	)
	cmd.Flags().BoolVar(
		&pin, "pin-libraries", false,
		"Pin each imported shared library to its version after the push",
	)
	return cmd
}

func pushWorkspace(cmd *cobra.Command, dir string, dryRun, force, pin bool) error {
	w := cmd.OutOrStdout()
	ws, err := loadWorkspace(dir)
	if err != nil {
		return err
	}
	created, changed := ws.changedLibraries()
	for _, name := range append(append([]string{}, created...), changed...) {
		if !validLibraryName(name) {
			return fmt.Errorf(
				"shared/%s.py: a library name is a lower-case Python "+
					"identifier of at most 64 characters, not a keyword, "+
					"oodle_eval or shared", name,
			)
		}
		if strings.TrimSpace(ws.shared[name]) == "" {
			return fmt.Errorf("shared/%s.py is empty: a library needs Python source", name)
		}
	}

	// 1. Validate.
	drafts := append(append([]string{}, created...), changed...)
	if err := validateWorkspace(cmd, ws, drafts); err != nil {
		return err
	}
	fmt.Fprintln(w, "Validate: no problems.")

	// 2. Shared libraries. The list gives the server's current
	// version of each, to find changes made after the pull.
	libs, err := listCodeLibraries(cmd)
	if err != nil {
		return err
	}
	server := map[string]client.CodeLibrary{}
	for _, l := range libs {
		server[l.Name] = l
	}
	for _, warning := range ws.pinWarnings() {
		fmt.Fprintln(w, "Warning: "+warning)
	}
	var conflicts []string
	for _, name := range created {
		if l, ok := server[name]; ok && !force {
			conflicts = append(conflicts, fmt.Sprintf(
				"shared/%s.py is new here, but the server has a library %s "+
					"(v%d) that was created after the pull; pushing would "+
					"replace it", name, name, l.Version,
			))
		}
	}
	for _, name := range changed {
		l, ok := server[name]
		entry := ws.lock.Libraries[name]
		switch {
		case force:
		case !ok:
			conflicts = append(conflicts, fmt.Sprintf(
				"shared/%s.py changed, but the library %s was deleted on the "+
					"server after the pull. Push with --force to re-create it "+
					"from the file", name, name,
			))
		case l.Version != entry.Latest:
			conflicts = append(conflicts, fmt.Sprintf(
				"shared library %s changed on the server after the pull "+
					"(v%d then, v%d now). Compare:\n"+
					"    oodle genai code-libraries versions %s --version %d | diff - %s",
				name, entry.Latest, l.Version, name, l.Version,
				filepath.Join(dir, wsShared, name+".py"),
			))
		case entry.Version != l.Version:
			// The file holds the pinned version, not the latest.
			// Its edit saved as the new latest would drop the
			// versions between, for every template that runs the
			// latest.
			conflicts = append(conflicts, fmt.Sprintf(
				"shared/%s.py is based on v%d, but the server has v%d; pushing "+
					"would replace %s. Rebase your change on v%d "+
					"(oodle genai code-libraries versions %s --version %d) "+
					"or push with --force",
				name, entry.Version, l.Version,
				versionRange(entry.Version+1, l.Version), l.Version, name, l.Version,
			))
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf(
			"push refused, nothing changed:\n  %s\nTo take the server's "+
				"version, keep a copy of your change, then pull with --force "+
				"--discard-local. To replace the server's version, push with --force",
			strings.Join(conflicts, "\n  "),
		)
	}

	verb := "Updated"
	if ws.meta.ID == "" {
		verb = "Created"
	}
	if dryRun {
		fmt.Fprintln(w, "Plan (dry run, nothing changed):")
		for _, name := range created {
			if _, ok := server[name]; ok {
				fmt.Fprintf(w, "  update shared library %s (it exists on the server)\n", name)
			} else {
				fmt.Fprintf(w, "  create shared library %s\n", name)
			}
		}
		for _, name := range changed {
			l, ok := server[name]
			entry := ws.lock.Libraries[name]
			switch {
			case !ok:
				fmt.Fprintf(w, "  re-create shared library %s (deleted on the server)\n", name)
			case entry.Version != l.Version:
				fmt.Fprintf(w, "  update shared library %s (v%d -> v%d, based on v%d: "+
					"replaces %s)\n", name, l.Version, l.Version+1, entry.Version,
					versionRange(entry.Version+1, l.Version))
			default:
				fmt.Fprintf(w, "  update shared library %s (v%d -> v%d)\n",
					name, l.Version, l.Version+1)
			}
		}
		if pin {
			fmt.Fprintf(w, "  pin libraries: %s\n", strings.Join(ws.imports(), ", "))
		}
		if ws.meta.ID == "" {
			fmt.Fprintf(w, "  create template %q\n", ws.fields["name"])
		} else {
			fmt.Fprintf(w, "  update template %s\n", ws.meta.ID)
		}
		return nil
	}

	for _, name := range append(append([]string{}, created...), changed...) {
		lib, existed, err := pushLibrary(cmd, name, ws.shared[name], server)
		if err != nil {
			return fmt.Errorf("shared library %s: %w", name, err)
		}
		action := "Created"
		if existed {
			action = "Updated"
		}
		fmt.Fprintf(w, "%s shared library %s (v%d)\n", action, name, lib.Version)
		ws.lock.Libraries[name] = lockEntry{
			Version: lib.Version, Latest: lib.Version, SHA256: sha(ws.shared[name]),
		}
		server[name] = *lib
		// Save after each write, so that a failure later does
		// not make the next push see a false conflict.
		if err := ws.saveLock(); err != nil {
			return err
		}
	}

	// 3. The template.
	pins := templatePins(ws.fields)
	if pin {
		for _, name := range ws.imports() {
			if l, ok := server[name]; ok {
				pins[name] = l.Version
			}
		}
		pinsAny := map[string]any{}
		for k, v := range pins {
			pinsAny[k] = float64(v)
		}
		ws.fields["libraryPins"] = pinsAny
		fileYAML, err := templateFileYAML(ws.fields)
		if err != nil {
			return err
		}
		if err := writeFile(dir, wsTemplate, fileYAML); err != nil {
			return err
		}
	} else {
		for _, name := range changed {
			if v, ok := pins[name]; ok {
				fmt.Fprintf(w,
					"Note: the template pins %s to v%d, so it does not run "+
						"the new version. Use --pin-libraries to move the pin.\n",
					name, v)
			}
		}
	}

	var tplID string
	if ws.meta.ID == "" {
		var body client.CreateGenaiEvaluatorJSONRequestBody
		if err := ws.templateBody(&body); err != nil {
			return err
		}
		resp, err := getClient(cmd).Inner.CreateGenaiEvaluatorWithResponse(
			cmd.Context(), getInstance(cmd), body,
		)
		if err != nil {
			return fmt.Errorf("API request failed: %w", err)
		}
		if err := genaiCheck(resp.StatusCode(), resp.HTTPResponse, resp.Body); err != nil {
			return err
		}
		if resp.JSON201 == nil {
			return errEmptyResponse
		}
		tplID = resp.JSON201.Id
		ws.meta.ID = tplID
		if err := ws.saveMeta(); err != nil {
			return err
		}
	} else {
		var body client.UpdateGenaiEvaluatorJSONRequestBody
		if err := ws.updateBody(&body); err != nil {
			return err
		}
		resp, err := getClient(cmd).Inner.UpdateGenaiEvaluatorWithResponse(
			cmd.Context(), getInstance(cmd), ws.meta.ID, body,
		)
		if err != nil {
			return fmt.Errorf("API request failed: %w", err)
		}
		if err := genaiCheck(resp.StatusCode(), resp.HTTPResponse, resp.Body); err != nil {
			return err
		}
		tplID = ws.meta.ID
	}
	// The files as pushed are the new base for local-change
	// checks of a later pull.
	if err := ws.recordFiles(); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s template %q (%s)\n", verb, ws.fields["name"], tplID)
	return nil
}

// recordFiles saves the SHA256 of evaluate.py and template.yaml
// as they are on disk now.
func (ws *workspace) recordFiles() error {
	ws.meta.Files = map[string]string{}
	for _, name := range []string{wsEvaluate, wsTemplate} {
		b, err := os.ReadFile(filepath.Join(ws.dir, name))
		if err != nil {
			return err
		}
		ws.meta.Files[name] = sha(string(b))
	}
	return ws.saveMeta()
}

// versionRange writes "v3" or "v2–v3".
func versionRange(from, to int) string {
	if from >= to {
		return fmt.Sprintf("v%d", to)
	}
	return fmt.Sprintf("v%d–v%d", from, to)
}

// pinWarnings names each pin in template.yaml that is not the
// version of the local file from the pull. The file is then not
// the code that the template runs, and a change to it is based on
// the wrong version.
func (ws *workspace) pinWarnings() []string {
	var out []string
	pins := templatePins(ws.fields)
	names := make([]string, 0, len(pins))
	for n := range pins {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, ok := ws.lock.Libraries[name]
		if ok && entry.Version != pins[name] {
			out = append(out, fmt.Sprintf(
				"template.yaml pins %s to v%d, but shared/%s.py holds v%d from "+
					"the pull. Pull again to get v%d before you change the file",
				name, pins[name], name, entry.Version, pins[name],
			))
		}
	}
	return out
}

// validateWorkspace runs the server's template checks. The new
// and changed files in shared/ go as draft libraries, so that the
// checks see the code that this push saves, not the stored
// libraries.
func validateWorkspace(cmd *cobra.Command, ws *workspace, drafts []string) error {
	var body client.ValidateGenaiEvaluatorJSONRequestBody
	if err := ws.templateBody(&body); err != nil {
		return err
	}
	if len(drafts) > 0 {
		libs := map[string]string{}
		for _, name := range drafts {
			libs[name] = ws.shared[name]
		}
		body.Libraries = &libs
	}
	resp, err := getClient(cmd).Inner.ValidateGenaiEvaluatorWithResponse(
		cmd.Context(), getInstance(cmd), body,
	)
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
	}
	if err := genaiCheck(resp.StatusCode(), resp.HTTPResponse, resp.Body); err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return errEmptyResponse
	}
	if resp.JSON200.Valid {
		return nil
	}
	writeValidation(cmd.OutOrStdout(), resp.JSON200)
	cmd.SilenceUsage = true
	return errTemplateNotValid
}

// pushLibrary creates the library, or updates its source when
// it exists on the server.
func pushLibrary(
	cmd *cobra.Command,
	name, source string,
	server map[string]client.CodeLibrary,
) (*client.CodeLibrary, bool, error) {
	c := getClient(cmd).Inner
	if l, ok := server[name]; ok {
		resp, err := c.UpdateGenaiCodeLibraryWithResponse(
			cmd.Context(), getInstance(cmd), l.Id,
			client.UpdateGenaiCodeLibraryJSONRequestBody{SourceCode: &source},
		)
		if err != nil {
			return nil, true, fmt.Errorf("API request failed: %w", err)
		}
		if err := genaiCheck(resp.StatusCode(), resp.HTTPResponse, resp.Body); err != nil {
			return nil, true, err
		}
		if resp.JSON200 == nil {
			return nil, true, errEmptyResponse
		}
		return resp.JSON200, true, nil
	}
	resp, err := c.CreateGenaiCodeLibraryWithResponse(
		cmd.Context(), getInstance(cmd),
		client.CreateGenaiCodeLibraryJSONRequestBody{Name: name, SourceCode: source},
	)
	if err != nil {
		return nil, false, fmt.Errorf("API request failed: %w", err)
	}
	if err := genaiCheck(resp.StatusCode(), resp.HTTPResponse, resp.Body); err != nil {
		return nil, false, err
	}
	if resp.JSON201 == nil {
		return nil, false, errEmptyResponse
	}
	return resp.JSON201, false, nil
}
