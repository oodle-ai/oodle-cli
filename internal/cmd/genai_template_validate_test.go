package cmd

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

func runValidate(
	t *testing.T,
	url string,
	format string,
	args ...string,
) (string, error) {
	t.Helper()
	cmd := newGenAITemplatesValidateCmd()
	cmd.Flags().StringP("output", "o", "", "")
	f := output.FormatJSON
	if format != "" {
		f = output.Format(format)
		args = append(args, "-o", format)
	}
	return runGenAICmd(t, url, f, cmd, args...)
}

func TestTemplatesValidatePrintsProblemsAndFails(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusOK,
		`{"valid":false,"problems":[`+
			`{"field":"params[0].default","message":"must be a list of strings"},`+
			`{"field":"","message":"no evaluator is named Helpfulness"}],`+
			`"scoreInputs":[],"libraries":[{"name":"acme_text","version":3}],`+
			`"notes":["acme_text runs at version 3"]}`)
	defer srv.Close()

	tpl := writeTempFile(t, "tpl.yaml", codeTemplateYAML)
	params := writeTempFile(t, "params.yaml", "required: [refund]\n")
	out, err := runValidate(t, srv.URL, "", "-f", tpl,
		"--rule-params", params, "--rule-name", "Refund rule")
	if !errors.Is(err, errTemplateNotValid) {
		t.Fatalf("err = %v, want errTemplateNotValid", err)
	}
	for _, want := range []string{
		"Not valid:",
		"  - params[0].default: must be a list of strings",
		"  - no evaluator is named Helpfulness",
		"Shared libraries: acme_text v3",
		"  - acme_text runs at version 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	sent := c.sent()
	if sent["type"] != "code" || sent["libraryPins"] == nil || sent["params"] == nil {
		t.Errorf("template fields missing from the body: %v", sent)
	}
	if rp, _ := sent["ruleParams"].(map[string]any); rp["required"] == nil {
		t.Errorf("ruleParams = %#v", sent["ruleParams"])
	}
	// The file's name is the template name, not the evaluator
	// name that the server checks score reads against.
	if sent["name"] != "Refund rule" {
		t.Errorf("name = %v, want the --rule-name value", sent["name"])
	}
}

func TestTemplatesValidateValid(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusOK,
		`{"valid":true,"problems":[],"scoreInputs":[{"id":"rule-2","name":"Helpfulness"}],`+
			`"libraries":[],"notes":[]}`)
	defer srv.Close()

	out, err := runValidate(t, srv.URL, "", "-f",
		writeTempFile(t, "tpl.yaml", codeTemplateYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Valid.") ||
		!strings.Contains(out, "Reads scores from: Helpfulness (rule-2)") {
		t.Errorf("output = %s", out)
	}
	if _, ok := c.sent()["name"]; ok {
		t.Errorf("sent the template name as the evaluator name: %v", c.sent())
	}

	out, err = runValidate(t, srv.URL, "json", "-f",
		writeTempFile(t, "tpl.yaml", codeTemplateYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"valid": true`) {
		t.Errorf("-o json output = %s", out)
	}
}

func TestTemplatesValidateSendsDraftLibraries(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusOK,
		`{"valid":true,"problems":[],"scoreInputs":[],`+
			`"libraries":[{"name":"acme_text","version":0,"draft":true},`+
			`{"name":"acme_policy","version":3}],"notes":[]}`)
	defer srv.Close()

	dir := t.TempDir()
	for name, src := range map[string]string{
		"acme_text.py": "def clean(t):\n    return t\n",
		"__init__.py":  "",
		"notes.txt":    "not a library",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runValidate(t, srv.URL, "", "-f",
		writeTempFile(t, "tpl.yaml", codeTemplateYAML), "--libraries", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Shared libraries: acme_text (draft), acme_policy v3") {
		t.Errorf("output = %s", out)
	}
	libs, _ := c.sent()["libraries"].(map[string]any)
	if len(libs) != 1 || libs["acme_text"] != "def clean(t):\n    return t\n" {
		t.Errorf("libraries = %v", libs)
	}

	bad := writeTempFile(t, "Bad-Name.py", "X = 1\n")
	if _, err := runValidate(t, srv.URL, "", "-f",
		writeTempFile(t, "tpl.yaml", codeTemplateYAML), "--libraries", bad); err == nil {
		t.Error("a bad library name was sent")
	}
}
