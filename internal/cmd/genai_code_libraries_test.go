package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	libID      = "0b6f7f55-2c55-4a6c-9d3b-6a0e0e0e0e01"
	libSource  = "def clean(text):\n    return \" \".join(text.split())\n"
	libBaseURL = "/langfuse/api/public/code-libraries"
)

// fakeLibraries is an API server for the code-libraries routes.
// It records each request, so that a test can check the path and
// the body that a command sent.
type fakeLibraries struct {
	mu       sync.Mutex
	requests []recordedRequest
	// deleteStatus and deleteBody replace the 204 of a delete.
	deleteStatus int
	deleteBody   string
}

type recordedRequest struct {
	method string
	path   string
	body   map[string]any
}

func (f *fakeLibraries) last(t *testing.T, method string) recordedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if f.requests[i].method == method {
			return f.requests[i]
		}
	}
	t.Fatalf("no %s request was sent", method)
	return recordedRequest{}
}

func (f *fakeLibraries) server(t *testing.T) *httptest.Server {
	t.Helper()
	lib := fmt.Sprintf(
		`{"id":%q,"name":"acme_text","description":"Text helpers",`+
			`"sourceCode":%q,"version":2,"createdAt":"2026-09-01T00:00:00Z",`+
			`"updatedAt":"2026-09-02T00:00:00Z"}`,
		libID, libSource,
	)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, recordedRequest{r.Method, r.URL.Path, body})
		f.mu.Unlock()

		i := strings.Index(r.URL.Path, libBaseURL)
		if i < 0 {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rest := r.URL.Path[i+len(libBaseURL):]
		w.Header().Set("Content-Type", "application/json")
		switch {
		case rest == "" && r.Method == http.MethodGet:
			fmt.Fprintf(w, `{"data":[%s]}`, lib)
		case rest == "" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, lib)
		case rest == "/"+libID && r.Method == http.MethodGet:
			fmt.Fprint(w, strings.TrimSuffix(lib, "}")+
				`,"usedBy":[{"id":"tpl-1","name":"Refund check","kind":"template"}]}`)
		case rest == "/"+libID && r.Method == http.MethodPatch:
			fmt.Fprint(w, lib)
		case rest == "/"+libID && r.Method == http.MethodDelete:
			if f.deleteStatus != 0 {
				w.WriteHeader(f.deleteStatus)
				fmt.Fprint(w, f.deleteBody)
				return
			}
			// A 204 has no body and so no content type.
			w.Header().Del("Content-Type")
			w.WriteHeader(http.StatusNoContent)
		case rest == "/"+libID+"/versions":
			fmt.Fprint(w, `{"data":[{"version":2,"createdAt":"2026-09-02T00:00:00Z",`+
				`"createdBy":"a@example.com"},{"version":1,"createdAt":"2026-09-01T00:00:00Z"}]}`)
		case rest == "/"+libID+"/versions/1":
			fmt.Fprint(w, `{"version":1,"createdAt":"2026-09-01T00:00:00Z",`+
				`"sourceCode":"def clean(text):\n    return text\n"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// runLibCmd runs one code-libraries subcommand against url.
// format is the output format in the context; the root sets it
// from -o or from the terminal.
func runLibCmd(
	t *testing.T,
	url string,
	format output.Format,
	cmd *cobra.Command,
	args ...string,
) (string, error) {
	t.Helper()
	out := new(bytes.Buffer)
	cmd.Flags().Bool("force", true, "")
	cmd.Flags().StringP("output", "o", "", "")
	cmd.SetContext(withOutput(awsCtxWith(t, url), format))
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestCodeLibrariesList(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatTable, newGenAICodeLibrariesListCmd())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"NAME", "acme_text", libID, "Text helpers"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

// A name must resolve to the id through the list, because the
// API addresses a library only by id.
func TestCodeLibrariesGetByName(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatTable,
		newGenAICodeLibrariesGetCmd(), "acme_text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "template Refund check") {
		t.Errorf("table lacks the template that imports the library:\n%s", out)
	}
	if got := f.last(t, http.MethodGet).path; !strings.HasSuffix(got, "/"+libID) {
		t.Errorf("get went to %s, want the library id", got)
	}
}

// The YAML of get must be a file that update reads back, with
// the source as a block and the API's key names.
func TestCodeLibrariesGetYAMLRoundTrips(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatYAML,
		newGenAICodeLibrariesGetCmd(), libID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "sourceCode: |") {
		t.Errorf("source is not a literal block:\n%s", out)
	}
	path := filepath.Join(t.TempDir(), "lib.yaml")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesUpdateCmd(), "acme_text", "-f", path); err != nil {
		t.Fatalf("update -f: %v", err)
	}
	sent := f.last(t, http.MethodPatch).body
	if sent["sourceCode"] != libSource || sent["description"] != "Text helpers" {
		t.Errorf("update body = %v", sent)
	}
	if _, ok := sent["name"]; ok {
		t.Errorf("update sent a name, which cannot change: %v", sent)
	}
}

func TestCodeLibrariesCreateFromPythonFile(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "acme_text.py")
	if err := os.WriteFile(path, []byte(libSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesCreateCmd(),
		"--source", path, "--description", "Text helpers"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := f.last(t, http.MethodPost).body
	want := map[string]any{
		"name": "acme_text", "description": "Text helpers", "sourceCode": libSource,
	}
	for k, v := range want {
		if sent[k] != v {
			t.Errorf("create %s = %v, want %v", k, sent[k], v)
		}
	}
}

// The flags replace the file values, so one YAML file can seed
// a library that another Python file supplies the source for.
func TestCodeLibrariesCreateFlagsReplaceTheFile(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "lib.yaml")
	pyPath := filepath.Join(dir, "other.py")
	if err := os.WriteFile(yamlPath, []byte(
		"name: acme_text\ndescription: From the file\n"+
			"sourceCode: |\n  x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pyPath, []byte(libSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesCreateCmd(), "-f", yamlPath, "--source", pyPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := f.last(t, http.MethodPost).body
	if sent["name"] != "acme_text" || sent["sourceCode"] != libSource ||
		sent["description"] != "From the file" {
		t.Errorf("create body = %v", sent)
	}
}

func TestCodeLibrariesCreateNeedsInput(t *testing.T) {
	_, err := runLibCmd(t, "http://127.0.0.1:1", output.FormatJSON,
		newGenAICodeLibrariesCreateCmd())
	if err == nil || !strings.Contains(err.Error(), "--file or --source") {
		t.Fatalf("err = %v, want an input error", err)
	}
}

func TestCodeLibrariesUpdateSendsOnlyTheSource(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "acme_text.py")
	if err := os.WriteFile(path, []byte(libSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesUpdateCmd(), libID, "--source", path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := f.last(t, http.MethodPatch).body
	if sent["sourceCode"] != libSource {
		t.Errorf("sourceCode = %v", sent["sourceCode"])
	}
	if _, ok := sent["description"]; ok {
		t.Errorf("update sent a description that was not given: %v", sent)
	}
}

func TestCodeLibrariesDelete(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesDeleteCmd(), "acme_text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Deleted shared library acme_text") {
		t.Errorf("output = %q", out)
	}
}

// A refused delete must name what imports the library, with its
// kind, because the user must change those first. A library that
// only another library imports also blocks the delete.
func TestCodeLibrariesDeleteInUse(t *testing.T) {
	f := &fakeLibraries{
		deleteStatus: http.StatusConflict,
		deleteBody: `{"message":"shared library acme_text is imported by 1 template",` +
			`"error":"conflict","usedBy":[{"id":"tpl-1","name":"Refund check","kind":"template"},` +
			`{"id":"lib-2","name":"acme_policy","kind":"library"}]}`,
	}
	srv := f.server(t)
	defer srv.Close()

	_, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesDeleteCmd(), "acme_text")
	if err == nil {
		t.Fatal("delete of a library in use did not fail")
	}
	for _, want := range []string{
		"shared library acme_text is imported by 1 template",
		"Used by:", "- template Refund check (tpl-1)",
		"- library acme_policy (lib-2)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if !strings.Contains(err.Error(), "Remedy: remove the import of shared.acme_text") {
		t.Errorf("error does not tell what to do: %v", err)
	}
	if strings.Contains(err.Error(), `"usedBy"`) {
		t.Errorf("error shows the raw JSON body: %v", err)
	}
}

// --restore saves the source of an older version as a new
// version, so that the history stays.
func TestCodeLibrariesUpdateRestore(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	if _, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesUpdateCmd(), "acme_text", "--restore", "1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := f.last(t, http.MethodPatch)
	if !strings.HasSuffix(sent.path, "/"+libID) {
		t.Errorf("update went to %s", sent.path)
	}
	if sent.body["sourceCode"] != "def clean(text):\n    return text\n" {
		t.Errorf("sourceCode = %q, want the source of version 1", sent.body["sourceCode"])
	}
	if _, ok := sent.body["description"]; ok {
		t.Errorf("a restore changed the description: %v", sent.body)
	}
}

// --restore gives the source, so a second source is a mistake.
func TestCodeLibrariesUpdateRestoreConflicts(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	for name, args := range map[string][]string{
		"with --source": {"--restore", "1", "--source", writeTempFile(t, "a.py", "x = 1\n")},
		"version 0":     {"--restore", "0"},
	} {
		_, err := runLibCmd(t, srv.URL, output.FormatJSON,
			newGenAICodeLibrariesUpdateCmd(), append([]string{"acme_text"}, args...)...)
		if err == nil {
			t.Errorf("%s: update did not fail", name)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 0 {
		t.Errorf("requests were sent: %v", f.requests)
	}
}

func TestCodeLibrariesVersions(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatTable,
		newGenAICodeLibrariesVersionsCmd(), "acme_text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "a@example.com") || !strings.Contains(out, "VERSION") {
		t.Errorf("versions table = %s", out)
	}
}

// With --version the source prints as Python, also to a pipe,
// so that `> v1.py` gives a file that runs.
func TestCodeLibrariesVersionPrintsSource(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	out, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesVersionsCmd(), "acme_text", "--version", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "def clean(text):\n    return text\n" {
		t.Errorf("output = %q, want the source only", out)
	}

	out, err = runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesVersionsCmd(), "acme_text", "--version", "1", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"version": 1`) {
		t.Errorf("-o json output = %s", out)
	}
}

func TestCodeLibrariesUnknownName(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	_, err := runLibCmd(t, srv.URL, output.FormatJSON,
		newGenAICodeLibrariesGetCmd(), "nope")
	if err == nil || !strings.Contains(err.Error(), `no shared library "nope"`) {
		t.Fatalf("err = %v, want a no-library error", err)
	}
}

// An empty source must not reach the server as an empty PATCH
// that succeeds and changes nothing.
func TestCodeLibrariesUpdateRefusesEmptySource(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	cases := map[string][]string{
		"empty --source file": {"--source", writeTempFile(t, "empty.py", "")},
		"blank --source file": {"--source", writeTempFile(t, "blank.py", " \n\n")},
		"empty sourceCode":    {"-f", writeTempFile(t, "e.yaml", "sourceCode: \"\"\n")},
		"no field to change":  {"-f", writeTempFile(t, "n.yaml", "name: acme_text\n")},
	}
	for name, args := range cases {
		_, err := runLibCmd(t, srv.URL, output.FormatJSON,
			newGenAICodeLibrariesUpdateCmd(), append([]string{libID}, args...)...)
		if err == nil {
			t.Errorf("%s: update did not fail", name)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if r.method == http.MethodPatch {
			t.Errorf("a PATCH was sent: %v", r.body)
		}
	}
}

func TestCodeLibrariesSourceFromStdin(t *testing.T) {
	f := &fakeLibraries{}
	srv := f.server(t)
	defer srv.Close()

	cmd := newGenAICodeLibrariesCreateCmd()
	cmd.SetIn(strings.NewReader(libSource))
	if _, err := runLibCmd(t, srv.URL, output.FormatJSON, cmd,
		"--source", "-", "--name", "acme_text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := f.last(t, http.MethodPost).body
	if sent["sourceCode"] != libSource || sent["name"] != "acme_text" {
		t.Errorf("create body = %v", sent)
	}

	// Standard input has no file name to take the name from.
	cmd = newGenAICodeLibrariesCreateCmd()
	cmd.SetIn(strings.NewReader(libSource))
	_, err := runLibCmd(t, srv.URL, output.FormatJSON, cmd, "--source", "-")
	if err == nil || !strings.Contains(err.Error(), "needs a name") {
		t.Errorf("err = %v, want a name error", err)
	}
}
