package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const libraryManifest = `{
  "package": "oodle_eval", "version": "1.0.0",
  "import": "from oodle_eval.v1 import metrics, text, combine",
  "modules": [
    {"name": "metrics", "path": "oodle_eval.v1.metrics", "doc": "Built-in checks.",
     "functions": [
       {"name": "keyword_check",
        "signature": "keyword_check(ctx, *, required=(), name='keywords_ok')",
        "summary": "Check that the reply has the required words.",
        "doc": "Check that the reply has the required words.\n\nArgs:\n    required: Words.",
        "params": [{"name": "required", "kind": "keyword", "default": "()"}],
        "scores": ["keywords_ok", "keyword_coverage"],
        "file": "oodle_eval/v1/metrics.py", "line": 9}
     ]},
    {"name": "metrics.format", "path": "oodle_eval.v1.metrics.format",
     "functions": [
       {"name": "keyword_check",
        "signature": "keyword_check(ctx, *, required=(), name='keywords_ok')",
        "summary": "Check that the reply has the required words.",
        "scores": ["keywords_ok", "keyword_coverage"],
        "file": "oodle_eval/v1/metrics.py", "line": 9}
     ]},
    {"name": "pkg", "path": "oodle_eval.v1.pkg", "functions": []},
    {"name": "combine", "path": "oodle_eval.v1.combine",
     "functions": [
       {"name": "weighted_mean", "signature": "weighted_mean(scores, weights=None)",
        "summary": "Average the scores.", "scores": ["combined"]}
     ]}
  ],
  "runtime": [{"name": "Score", "kind": "class",
               "signature": "Score(value, data_type='NUMERIC')", "doc": "One named score.",
               "file": "oodle_eval/runtime.py", "line": 1,
               "methods": [{"name": "passed", "signature": "passed(at=None)",
                            "summary": "Say if the score passes."}]}],
  "context": [{"path": "ctx.params", "doc": "The evaluator's settings."}],
  "extra": "kept"
}`

// metricsSource has a helper, a decorated function with a
// multi-line signature that closes at column 0, a comment that
// belongs to the next function, and the next function.
const metricsSource = `import re


def _helper():
    return 1


@_declares("keywords_ok", "keyword_coverage")
def keyword_check(
    ctx,
    *,
    required=(),
):
    """Check that the reply has the required words."""

    return []


# The next check.
def contains(ctx):
    return []
`

const runtimeSource = "class Score:\n    def passed(self, at=None):\n        return True\n\n\nclass Other:\n    pass\n"

func libraryServer(t *testing.T) *httptest.Server {
	t.Helper()
	const base = "/langfuse/api/public/code-eval-library"
	files := map[string]string{
		"oodle_eval/v1/metrics.py":      metricsSource,
		"oodle_eval/runtime.py":         runtimeSource,
		"oodle_eval/v1/pkg/__init__.py": "from oodle_eval.v1.pkg.a import x\n",
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		i := strings.Index(r.URL.Path, base)
		if i < 0 {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		rest := r.URL.Path[i+len(base):]
		switch {
		case rest == "":
			fmt.Fprint(w, libraryManifest)
		case rest == "/files":
			fmt.Fprint(w, `{"files":[{"path":"oodle_eval/runtime.py","size":120},`+
				`{"path":"oodle_eval/v1/metrics.py","size":400},`+
				`{"path":"oodle_eval/v1/pkg/__init__.py","size":30}]}`)
		case strings.HasPrefix(rest, "/files/"):
			path := strings.TrimPrefix(rest, "/files/")
			src, ok := files[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"no such file","error":"Not Found"}`)
				return
			}
			body, _ := json.Marshal(map[string]string{"path": path, "source": src})
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
}

// runLibrary runs the command as the root runs it: the context
// holds the format that -o selects, and the flag records that
// the user set it.
func runLibrary(t *testing.T, srvURL string, args ...string) (string, error) {
	t.Helper()
	out := new(bytes.Buffer)
	cmd := newGenAILibraryCmd()
	// Persistent, as on the root, so that `files` sees it too.
	cmd.PersistentFlags().StringP("output", "o", "", "")
	// The root puts the -o format in the context; the default
	// is JSON, as for a pipe.
	format := output.FormatJSON
	for i, a := range args {
		if a == "-o" && i+1 < len(args) {
			format = output.Format(args[i+1])
		}
	}
	cmd.SetContext(withOutput(awsCtxWith(t, srvURL), format))
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestLibraryListsFunctionsAsATable(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	// The context format is JSON, as for a pipe. With no -o the
	// command must still print the table.
	out, err := runLibrary(t, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"FUNCTION", "metrics.keyword_check",
		"Check that the reply has the required words.",
		"combine.weighted_mean",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestLibraryPrintsTheRawManifest(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got["extra"] != "kept" || got["package"] != "oodle_eval" {
		t.Errorf("manifest changed on the way out: %v", got)
	}
}

func TestLibraryShowsOneFunction(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	for _, name := range []string{
		"metrics.keyword_check", "keyword_check",
		"oodle_eval.v1.metrics.keyword_check",
	} {
		out, err := runLibrary(t, srv.URL, name)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		for _, want := range []string{
			"metrics.keyword_check(ctx, *, required=(), name='keywords_ok')",
			"Args:",
			"Scores: keywords_ok, keyword_coverage",
			"Import: from oodle_eval.v1 import metrics, text, combine",
			"Source: oodle_eval/v1/metrics.py:9",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", name, want, out)
			}
		}
	}
}

func TestLibraryShowsModulesRuntimeAndContext(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	tests := map[string]string{
		"combine":    "combine.weighted_mean",
		"Score":      "One named score.",
		"ctx.params": "The evaluator's settings.",
	}
	for arg, want := range tests {
		out, err := runLibrary(t, srv.URL, arg)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", arg, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s: output lacks %q:\n%s", arg, want, out)
		}
	}
}

func TestLibraryFunctionAsJSON(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "keyword_check", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var fn map[string]any
	if err := json.Unmarshal([]byte(out), &fn); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if fn["name"] != "keyword_check" || fn["scores"] == nil {
		t.Errorf("function = %v", fn)
	}
}

func TestLibraryUnknownEntry(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	_, err := runLibrary(t, srv.URL, "nope")
	if err == nil || !strings.Contains(err.Error(), `no library entry "nope"`) {
		t.Fatalf("err = %v, want a no-entry error", err)
	}
}

// The templates group had "library" as an alias. The alias must
// not come back: cobra takes the first command that matches, and
// `oodle genai library` would open the templates group.
func TestGenAILibraryIsNotATemplatesAlias(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"genai", "library"})
	if err != nil {
		t.Fatalf("Find(genai library): %v", err)
	}
	if cmd.Name() != "library" {
		t.Errorf("genai library resolved to %q", cmd.Name())
	}
}

func TestLibraryHonoursCSV(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "-o", "csv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, "FUNCTION,SUMMARY\n") ||
		!strings.Contains(out, "metrics.keyword_check,") {
		t.Errorf("not CSV:\n%s", out)
	}

	out, err = runLibrary(t, srv.URL, "keyword_check", "-o", "csv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "FUNCTION,SUMMARY\nmetrics.keyword_check,Check that the reply has the required words.\n" {
		t.Errorf("one-row CSV = %q", out)
	}
}

func TestLibrarySourceSlicesTheFunction(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "keyword_check", "--source")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `@_declares("keywords_ok", "keyword_coverage")
def keyword_check(
    ctx,
    *,
    required=(),
):
    """Check that the reply has the required words."""

    return []
`
	if out != want {
		t.Errorf("source =\n%s\nwant:\n%s", out, want)
	}
}

func TestLibraryRuntimeClassShowsMethodsAndSource(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "Score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Methods:", "passed(at=None)", "Say if the score passes.",
		"Source: oodle_eval/runtime.py:1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	out, err = runLibrary(t, srv.URL, "Score", "--source")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "class Score:\n    def passed(self, at=None):\n        return True\n" {
		t.Errorf("class source = %q", out)
	}
}

func TestLibrarySourceOfAModuleAndErrors(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "metrics", "--source")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != metricsSource {
		t.Errorf("module source = %q", out)
	}
	if _, err := runLibrary(t, srv.URL, "--source"); err == nil {
		t.Error("--source with no name did not fail")
	}
	if _, err := runLibrary(t, srv.URL, "ctx.params", "--source"); err == nil {
		t.Error("--source of a context value did not fail")
	}
	// A function with no location in the manifest.
	if _, err := runLibrary(t, srv.URL, "weighted_mean", "--source"); err == nil ||
		!strings.Contains(err.Error(), "no source location") {
		t.Errorf("err = %v, want a no-location error", err)
	}
}

func TestLibraryFiles(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "files")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "PATH") || !strings.Contains(out, "oodle_eval/v1/metrics.py") {
		t.Errorf("files table = %s", out)
	}

	out, err = runLibrary(t, srv.URL, "files", "oodle_eval/v1/metrics.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != metricsSource {
		t.Errorf("file = %q", out)
	}

	out, err = runLibrary(t, srv.URL, "files", "oodle_eval/runtime.py", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"path": "oodle_eval/runtime.py"`) {
		t.Errorf("-o json = %s", out)
	}

	_, err = runLibrary(t, srv.URL, "files", "nope.py")
	if err == nil || !strings.Contains(err.Error(), "no such file") ||
		!strings.Contains(err.Error(), "nope.py") ||
		strings.Count(err.Error(), "Error:") != 1 {
		t.Errorf("err = %v, want the server's message", err)
	}
}

func TestSliceDefinitionBounds(t *testing.T) {
	if _, ok := sliceDefinition("def a():\n    pass\n", 9); ok {
		t.Error("a line past the end was accepted")
	}
	got, ok := sliceDefinition("def a():\n    pass", 1)
	if !ok || got != "def a():\n    pass\n" {
		t.Errorf("last definition = %q", got)
	}
}

// A package lists its submodule's check again. One definition
// is one entry, shown under the shorter module name.
func TestLibraryMergesTheSameDefinition(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "keyword_check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, "metrics.keyword_check(") {
		t.Errorf("output = %s", out)
	}
	if _, err := runLibrary(t, srv.URL, "keyword_check", "--source"); err != nil {
		t.Errorf("--source: %v", err)
	}
	out, err = runLibrary(t, srv.URL, "metrics.format.keyword_check")
	if err != nil || !strings.HasPrefix(out, "metrics.format.keyword_check(") {
		t.Errorf("metrics.format.keyword_check: %v %s", err, out)
	}
}

// A package's source is its __init__.py.
func TestLibraryPackageSource(t *testing.T) {
	srv := libraryServer(t)
	defer srv.Close()

	out, err := runLibrary(t, srv.URL, "pkg", "--source")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "from oodle_eval.v1.pkg.a import x\n" {
		t.Errorf("package source = %q", out)
	}
	_, err = runLibrary(t, srv.URL, "combine", "--source")
	if err == nil || !strings.Contains(err.Error(), "oodle_eval/v1/combine.py") {
		t.Errorf("err = %v, want it to name the paths it tried", err)
	}
}
