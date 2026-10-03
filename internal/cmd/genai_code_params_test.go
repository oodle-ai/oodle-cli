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

// captureServer answers every request with reply and keeps the
// last request body, so that a test can check what a command
// sent.
type captureServer struct {
	mu   sync.Mutex
	body map[string]any
}

func (c *captureServer) start(t *testing.T, status int, reply string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("request body is not JSON: %v", err)
			}
			c.mu.Lock()
			c.body = body
			c.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, reply)
	}))
}

func (c *captureServer) sent() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runGenAICmd(
	t *testing.T,
	url string,
	format output.Format,
	cmd *cobra.Command,
	args ...string,
) (string, error) {
	t.Helper()
	out := new(bytes.Buffer)
	cmd.SetContext(withOutput(awsCtxWith(t, url), format))
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

const codeTemplateYAML = `name: Mentions refund
type: code
sourceCodeLanguage: python
params:
  - name: required
    type: string_list
    default: [refund]
  - name: strict
    type: boolean
    default: false
libraryPins:
  acme_text: 3
sourceCode: |
  def evaluate(ctx):
      return None
`

const templateReply = `{"id":"tpl-1","name":"Mentions refund","type":"code",` +
	`"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z",` +
	`"higherIsBetter":true,"version":1}`

// The create and update bodies are generated types. A field that
// the type does not declare is dropped with no error, so the new
// fields must reach the wire.
func TestTemplatesCreateSendsParamsAndPins(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		cmd    func() *cobra.Command
		args   []string
	}{
		{"create", http.StatusCreated, newGenAITemplatesCreateCmd, nil},
		{"update", http.StatusOK, newGenAITemplatesUpdateCmd, []string{"tpl-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &captureServer{}
			srv := c.start(t, tc.status, templateReply)
			defer srv.Close()

			path := writeTempFile(t, "tpl.yaml", codeTemplateYAML)
			args := append(append([]string{}, tc.args...), "-f", path)
			if _, err := runGenAICmd(t, srv.URL, output.FormatJSON, tc.cmd(), args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			sent := c.sent()
			params, ok := sent["params"].([]any)
			if !ok || len(params) != 2 {
				t.Fatalf("params = %#v", sent["params"])
			}
			strict := params[1].(map[string]any)
			if strict["default"] != false {
				t.Errorf("a false default was lost: %v", strict)
			}
			pins, ok := sent["libraryPins"].(map[string]any)
			if !ok || pins["acme_text"] != float64(3) {
				t.Errorf("libraryPins = %#v", sent["libraryPins"])
			}
		})
	}
}

const ruleReply = `{"id":"rule-1","name":"Refund mentioned","evaluatorId":"oodle-managed-code-keyword-check-v1",` +
	`"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","enabled":true,` +
	`"filters":null,"modelParams":null,"variableMapping":null,"generationSpansOnly":false,` +
	`"maxInvocationsPerHour":0,"samplingRate":1,"targetType":"trace"}`

func TestEvaluatorsCreateAndUpdateSendParams(t *testing.T) {
	file := `name: Refund mentioned
evaluatorId: oodle-managed-code-keyword-check-v1
params:
  required: [refund, return]
  mode: any
  case_sensitive: false
scoreInputRuleIds: [rule-9]
`
	for _, tc := range []struct {
		name   string
		status int
		cmd    func() *cobra.Command
		args   []string
	}{
		{"create", http.StatusCreated, newGenAIEvaluatorsCreateCmd, nil},
		{"update", http.StatusOK, newGenAIEvaluatorsUpdateCmd, []string{"rule-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &captureServer{}
			srv := c.start(t, tc.status, ruleReply)
			defer srv.Close()

			path := writeTempFile(t, "rule.yaml", file)
			args := append(append([]string{}, tc.args...), "-f", path)
			if _, err := runGenAICmd(t, srv.URL, output.FormatJSON, tc.cmd(), args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			sent := c.sent()
			params, ok := sent["params"].(map[string]any)
			if !ok || params["mode"] != "any" || params["case_sensitive"] != false {
				t.Fatalf("params = %#v", sent["params"])
			}
			if req, _ := params["required"].([]any); len(req) != 2 {
				t.Errorf("required = %#v", params["required"])
			}
			// The server derives scoreInputRuleIds. A file made
			// from `get -o yaml` carries them, and the body must
			// not send them back.
			if _, ok := sent["scoreInputRuleIds"]; ok {
				t.Errorf("sent the read-only scoreInputRuleIds: %v", sent)
			}
		})
	}
}

const rulesListReply = `{"data":[` +
	`{"id":"rule-1","name":"Combined quality","evaluatorId":"tpl-1",` +
	`"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","enabled":true,` +
	`"filters":null,"modelParams":null,"variableMapping":null,"generationSpansOnly":false,` +
	`"higherIsBetter":true,"maxInvocationsPerHour":0,"samplingRate":1,"targetType":"trace",` +
	`"params":{"weights":[1,2],"at":0.5},"scoreInputRuleIds":["rule-2"]},` +
	`{"id":"rule-2","name":"Helpfulness","evaluatorId":"tpl-2",` +
	`"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","enabled":true,` +
	`"filters":null,"modelParams":null,"variableMapping":null,"generationSpansOnly":false,` +
	`"higherIsBetter":true,"maxInvocationsPerHour":0,"samplingRate":1,"targetType":"trace"}]}`

// rulesServer serves the rule list and each rule by id, and
// counts the list requests.
func rulesServer(t *testing.T, lists *int) *httptest.Server {
	t.Helper()
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(rulesListReply), &list); err != nil {
		t.Fatal(err)
	}
	byID := map[string]json.RawMessage{}
	for _, r := range list.Data {
		var head struct{ ID string }
		_ = json.Unmarshal(r, &head)
		byID[head.ID] = r
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/evaluation-rules") {
			*lists++
			fmt.Fprint(w, rulesListReply)
			return
		}
		i := strings.LastIndex(r.URL.Path, "/evaluation-rules/")
		if i >= 0 {
			if body, ok := byID[r.URL.Path[i+len("/evaluation-rules/"):]]; ok {
				_, _ = w.Write(body)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"evaluation rule not found","error":"Not Found"}`)
	}))
}

func TestEvaluatorsGetShowsParamsAndScoreInputs(t *testing.T) {
	var lists int
	srv := rulesServer(t, &lists)
	defer srv.Close()

	out, err := runGenAICmd(t, srv.URL, output.FormatTable,
		newGenAIEvaluatorsGetCmd(), "Combined quality")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"PARAMS", "at=0.5 weights=[1,2]",
		"READS SCORES FROM", "Helpfulness",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}

	out, err = runGenAICmd(t, srv.URL, output.FormatYAML,
		newGenAIEvaluatorsGetCmd(), "rule-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"scoreInputRuleIds:", "- rule-2", "params:", "at: 0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml lacks %q:\n%s", want, out)
		}
	}
}

// An id goes to the single-rule route with no list request.
func TestEvaluatorsGetByIDSkipsTheList(t *testing.T) {
	var lists int
	srv := rulesServer(t, &lists)
	defer srv.Close()

	out, err := runGenAICmd(t, srv.URL, output.FormatJSON,
		newGenAIEvaluatorsGetCmd(), "rule-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"name": "Helpfulness"`) {
		t.Errorf("output = %s", out)
	}
	if lists != 0 {
		t.Errorf("sent %d list requests, want 0", lists)
	}
}

func TestEvaluatorsGetUnknown(t *testing.T) {
	var lists int
	srv := rulesServer(t, &lists)
	defer srv.Close()

	_, err := runGenAICmd(t, srv.URL, output.FormatJSON,
		newGenAIEvaluatorsGetCmd(), "nope")
	if err == nil || !strings.Contains(err.Error(), `no evaluator "nope"`) {
		t.Fatalf("err = %v, want a no-evaluator error", err)
	}
}

// A refused rename or delete names the evaluators that read the
// rule's scores, so that the user can see what to change first.
func TestGenAIErrorShowsDependents(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusConflict,
		`{"message":"other evaluators read this evaluator's scores",`+
			`"error":"Conflict","dependents":[{"id":"rule-1","name":"Combined quality"}]}`)
	defer srv.Close()

	_, err := runGenAICmd(t, srv.URL, output.FormatJSON,
		newGenAIEvaluatorsUpdateCmd(), "rule-2", "--disable")
	if err == nil {
		t.Fatal("a 409 did not fail")
	}
	for _, want := range []string{
		"other evaluators read this evaluator's scores",
		"Combined quality (rule-1)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestGenAIErrorMessageIgnoresOtherBodies(t *testing.T) {
	for _, body := range []string{"", "<html>", `{"errors":[{"message":"x"}]}`} {
		if got, _ := genaiErrorMessage([]byte(body)); got != "" {
			t.Errorf("genaiErrorMessage(%q) = %q, want empty", body, got)
		}
	}
}

// A template update that makes existing evaluators' settings not
// valid is refused. The error must name those evaluators.
func TestTemplatesUpdateConflictNamesEvaluators(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusConflict,
		`{"message":"the new settings do not fit 1 evaluator",`+
			`"error":"Conflict","dependents":[{"id":"rule-1","name":"Refund mentioned"}]}`)
	defer srv.Close()

	path := writeTempFile(t, "tpl.yaml", codeTemplateYAML)
	_, err := runGenAICmd(t, srv.URL, output.FormatJSON,
		newGenAITemplatesUpdateCmd(), "tpl-1", "-f", path)
	if err == nil {
		t.Fatal("a 409 did not fail")
	}
	for _, want := range []string{
		"the new settings do not fit 1 evaluator",
		"Affected evaluators:", "- Refund mentioned (rule-1)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

// The template holds a source line with trailing spaces and a tab
// indent, which yaml.v3 alone writes as one quoted line.
const templateGetReply = `{"id":"tpl-1","name":"Mentions refund","type":"code",` +
	`"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z",` +
	`"higherIsBetter":true,"version":1,"modelParams":null,"outputSchema":null,` +
	`"sourceCodeLanguage":"python",` +
	`"sourceCode":"def evaluate(ctx):  \n\treturn None\n",` +
	`"params":[{"name":"strict","type":"boolean","default":false}],` +
	`"libraryPins":{"acme_text":3}}`

// `templates get -o yaml` must give a file that `update -f` sends
// back with the same params, pins and source.
func TestTemplatesGetYAMLRoundTrips(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusOK, templateGetReply)
	defer srv.Close()

	out, err := runGenAICmd(t, srv.URL, output.FormatYAML,
		newGenAITemplatesGetCmd(), "tpl-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"libraryPins:", "sourceCode: |", "params:"} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"librarypins", "sourcecode", ": null"} {
		if strings.Contains(out, bad) {
			t.Errorf("yaml has %q:\n%s", bad, out)
		}
	}

	path := writeTempFile(t, "tpl.yaml", out)
	if _, err := runGenAICmd(t, srv.URL, output.FormatJSON,
		newGenAITemplatesUpdateCmd(), "tpl-1", "-f", path); err != nil {
		t.Fatalf("update -f: %v", err)
	}
	sent := c.sent()
	if sent["sourceCode"] != "def evaluate(ctx):  \n\treturn None\n" {
		t.Errorf("sourceCode = %q", sent["sourceCode"])
	}
	if pins, _ := sent["libraryPins"].(map[string]any); pins["acme_text"] != float64(3) {
		t.Errorf("libraryPins = %#v", sent["libraryPins"])
	}
	params, _ := sent["params"].([]any)
	if len(params) != 1 || params[0].(map[string]any)["default"] != false {
		t.Errorf("params = %#v", sent["params"])
	}
}

func TestTemplatesListYAMLUsesWireNames(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusOK,
		`{"data":[`+templateGetReply+`],"meta":{"page":1,"limit":50,"totalItems":1,"totalPages":1}}`)
	defer srv.Close()

	out, err := runGenAICmd(t, srv.URL, output.FormatYAML, newGenAITemplatesListCmd())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "libraryPins:") || strings.Contains(out, "librarypins") {
		t.Errorf("yaml keys are not the wire names:\n%s", out)
	}
}

// The list goes last, and an empty code or trace is left out.
func TestGenAIErrorFormat(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusConflict,
		`{"message":"cannot delete","error":"Conflict",`+
			`"usedBy":[{"id":"tpl-1","name":"Refund check","kind":"template"}]}`)
	defer srv.Close()

	_, err := runGenAICmd(t, srv.URL, output.FormatJSON,
		newGenAITemplatesUpdateCmd(), "tpl-1", "-f",
		writeTempFile(t, "t.yaml", "name: x\n"))
	if err == nil {
		t.Fatal("a 409 did not fail")
	}
	want := "Error: cannot delete\nUsed by:\n  - template Refund check (tpl-1)"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// A rule 409 message that already names the dependents must not
// be followed by the same names again.
func TestGenAIErrorDoesNotRepeatNames(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusConflict,
		`{"message":"evaluators read this one's scores: Combined quality",`+
			`"error":"Conflict","dependents":[{"id":"rule-1","name":"Combined quality"}]}`)
	defer srv.Close()

	_, err := runGenAICmd(t, srv.URL, output.FormatJSON,
		newGenAIEvaluatorsUpdateCmd(), "rule-2", "--disable")
	if err == nil {
		t.Fatal("a 409 did not fail")
	}
	if n := strings.Count(err.Error(), "Combined quality"); n != 1 {
		t.Errorf("the name shows %d times: %v", n, err)
	}
}

func TestTemplatesListShowsCategory(t *testing.T) {
	c := &captureServer{}
	srv := c.start(t, http.StatusOK,
		`{"data":[{"id":"oodle-managed-code-keyword-check-v1","name":"Keyword check",`+
			`"type":"code","category":"Built-in checks","description":"Required words.",`+
			`"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z",`+
			`"higherIsBetter":true,"version":1}],`+
			`"meta":{"page":1,"limit":50,"totalItems":1,"totalPages":1}}`)
	defer srv.Close()

	out, err := runGenAICmd(t, srv.URL, output.FormatTable, newGenAITemplatesListCmd())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "CATEGORY") || !strings.Contains(out, "Built-in checks") {
		t.Errorf("table lacks the category:\n%s", out)
	}
}
