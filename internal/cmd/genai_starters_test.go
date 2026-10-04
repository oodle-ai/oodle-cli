package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

const startersBody = `{"data":[{"id":"pii-leak","name":"PII and secret leak",` +
	`"category":"Safety and tone","description":"Output leaks PII.",` +
	`"sourceCode":"CHECKS = [\"email\"]\n\n\ndef evaluate(ctx):\n    return None\n"},` +
	`{"id":"keyword-check","name":"Keyword check","category":"Content",` +
	`"description":"Required words.","builtIn":true,` +
	`"primaryScoreType":"BOOLEAN","primaryHigherIsBetter":false,` +
	`"sourceCode":"def evaluate(ctx):\n    return None\n",` +
	`"params":[{"name":"required","type":"string_list","label":"Required words",` +
	`"default":["refund"]},{"name":"case_sensitive","type":"boolean","default":false},` +
	`{"name":"mode","type":"enum","options":["all","any"],"default":"all","required":true}]}]}`

func startersServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/langfuse/api/public/code-eval-starters") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, startersBody)
	}))
}

func TestTemplatesStartersLists(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	out := new(bytes.Buffer)
	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "pii-leak") {
		t.Errorf("list output lacks the starter: %s", out.String())
	}
}

// The printed file must be one `templates create -f` reads back
// into a code template with the source unchanged, line for line.
func TestTemplatesStarterPrintsACreateFile(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	out := new(bytes.Buffer)
	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(out)
	cmd.SetArgs([]string{"pii-leak"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "sourceCode: |") {
		t.Errorf("source is not a literal block:\n%s", out.String())
	}

	path := filepath.Join(t.TempDir(), "pii.yaml")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var body client.CreateGenaiEvaluatorJSONRequestBody
	if err := readInputFile(path, &body); err != nil {
		t.Fatalf("create -f cannot read it: %v", err)
	}
	if body.Type != "code" || body.Name != "PII and secret leak" {
		t.Errorf("name/type = %q/%q", body.Name, body.Type)
	}
	want := "CHECKS = [\"email\"]\n\n\ndef evaluate(ctx):\n    return None\n"
	if body.SourceCode == nil || *body.SourceCode != want {
		t.Errorf("source changed in the round trip: %v", body.SourceCode)
	}
}

func TestTemplatesStarterUnknownID(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"nope"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `no starter "nope"`) {
		t.Fatalf("err = %v, want a no-starter error", err)
	}
}

func TestTemplatesStartersListNamesTheSettings(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	out := new(bytes.Buffer)
	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(withOutput(awsCtxWith(t, srv.URL), output.FormatTable))
	cmd.SetOut(out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"SETTINGS", "required, case_sensitive, mode", "BUILT-IN", "yes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table lacks %q:\n%s", want, out.String())
		}
	}
}

// The settings in the printed file must arrive in the create
// body with their defaults, also a false default, which an
// omitempty on the wrong field type would drop.
func TestTemplatesStarterParamsRoundTrip(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	out := new(bytes.Buffer)
	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(out)
	cmd.SetArgs([]string{"keyword-check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "oodle-managed-code-keyword-check-v1") {
		t.Errorf("file does not name the built-in check:\n%s", out.String())
	}

	path := filepath.Join(t.TempDir(), "kw.yaml")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var body client.CreateGenaiEvaluatorJSONRequestBody
	if err := readInputFile(path, &body); err != nil {
		t.Fatalf("create -f cannot read it: %v\n%s", err, out.String())
	}
	params := deref(body.Params)
	if len(params) != 3 {
		t.Fatalf("params = %+v, want 3", params)
	}
	if params[0].Name != "required" || deref(params[0].Label) != "Required words" {
		t.Errorf("params[0] = %+v", params[0])
	}
	if d, ok := params[0].Default.([]any); !ok || len(d) != 1 || d[0] != "refund" {
		t.Errorf("required default = %#v", params[0].Default)
	}
	if params[1].Default != false {
		t.Errorf("case_sensitive default = %#v, want false", params[1].Default)
	}
	if !deref(params[2].Required) || len(deref(params[2].Options)) != 2 {
		t.Errorf("mode = %+v", params[2])
	}
}

func TestTemplatesStarterHonoursOutput(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	run := func(format string) (string, error) {
		out := new(bytes.Buffer)
		cmd := newGenAITemplatesStartersCmd()
		cmd.Flags().StringP("output", "o", "", "")
		cmd.SetContext(withOutput(awsCtxWith(t, srv.URL), output.Format(format)))
		cmd.SetOut(out)
		cmd.SetErr(new(bytes.Buffer))
		cmd.SetArgs([]string{"keyword-check", "-o", format})
		err := cmd.Execute()
		return out.String(), err
	}

	out, err := run("json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body client.CreateGenaiEvaluatorJSONRequestBody
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, out)
	}
	if body.Type != "code" || len(deref(body.Params)) != 3 {
		t.Errorf("JSON template = %+v", body)
	}

	if _, err := run("csv"); err == nil {
		t.Error("-o csv did not fail")
	}
}

// The primary score's type and direction go into the file as the
// template's scoreType and higherIsBetter, also a false direction.
func TestTemplatesStarterScoreTypeRoundTrips(t *testing.T) {
	srv := startersServer(t)
	defer srv.Close()

	out := new(bytes.Buffer)
	cmd := newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(out)
	cmd.SetArgs([]string{"keyword-check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"scoreType: boolean", "higherIsBetter: false"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("file lacks %q:\n%s", want, out.String())
		}
	}
	path := filepath.Join(t.TempDir(), "kw.yaml")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var body client.CreateGenaiEvaluatorJSONRequestBody
	if err := readInputFile(path, &body); err != nil {
		t.Fatal(err)
	}
	if deref(body.ScoreType) != "boolean" || body.HigherIsBetter == nil || *body.HigherIsBetter {
		t.Errorf("scoreType/higherIsBetter = %v/%v", body.ScoreType, body.HigherIsBetter)
	}

	// The PII starter has no primary score fields: none in the file.
	out.Reset()
	cmd = newGenAITemplatesStartersCmd()
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetOut(out)
	cmd.SetArgs([]string{"pii-leak"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "scoreType") || strings.Contains(out.String(), "higherIsBetter") {
		t.Errorf("file has empty score fields:\n%s", out.String())
	}
}
