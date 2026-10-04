package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// fakeOodle is a stateful API server for the workspace commands:
// templates, starters, shared libraries with versions, the
// library files, validate, test-run and traces.
type fakeOodle struct {
	t  *testing.T
	mu sync.Mutex
	// libs is name to versions; the last is the latest.
	libs      map[string][]string
	templates map[string]map[string]any
	calls     []string
	bodies    map[string]map[string]any
	problems  []map[string]string
	runReply  string
	// badFiles adds library files whose paths leave oodle_eval/.
	badFiles bool
	// failFile makes the fetch of this library file fail.
	failFile string
}

func newFakeOodle(t *testing.T) *fakeOodle {
	return &fakeOodle{
		t: t,
		libs: map[string][]string{
			"acme_text":   {"def clean(t):\n    return t\n", "def clean(t):\n    return t.strip()\n"},
			"acme_policy": {"LIMIT = 3\n"},
		},
		templates: map[string]map[string]any{
			"tpl-1": {
				"id": "tpl-1", "name": "Refund check", "type": "code",
				"sourceCodeLanguage": "python", "higherIsBetter": true,
				"createdAt": "2026-09-01T00:00:00Z", "updatedAt": "2026-09-01T00:00:00Z",
				"version": 1, "prompt": "", "modelParams": nil, "outputSchema": nil,
				"sourceCode":  "from shared.acme_text import clean\n\ndef evaluate(ctx):\n    return None\n",
				"params":      []any{map[string]any{"name": "at", "type": "number", "default": 0.5}},
				"libraryPins": map[string]any{"acme_text": 1},
			},
		},
		bodies:   map[string]map[string]any{},
		runReply: `{"scores":[{"name":"refund_ok","value":true,"data_type":"BOOLEAN","comment":"ok"}],"logs":"checked 1 reply\n","duration_ms":12}`,
	}
}

func fakeLibID(name string) string { return "lib-" + name }

func (f *fakeOodle) libJSON(name string) map[string]any {
	v := f.libs[name]
	return map[string]any{
		"id": fakeLibID(name), "name": name, "description": "",
		"sourceCode": v[len(v)-1], "version": len(v),
		"createdAt": "2026-09-01T00:00:00Z", "updatedAt": "2026-09-01T00:00:00Z",
	}
}

func (f *fakeOodle) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeOodle) body(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

func (f *fakeOodle) server() *httptest.Server {
	t := f.t
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		reply := func(status int, v any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}

		p := r.URL.Path
		if i := strings.Index(p, "/traces/traces/"); i >= 0 {
			f.calls = append(f.calls, "GET traces")
			fmt.Fprint(w, `{"data":[{"traceID":"t1","spans":[`+
				`{"spanID":"child","parentSpanID":"root","traceID":"t1","startTime":2,"duration":1,"endTime":3,"operationName":"llm","processID":"p","logs":[],"references":[],"tags":[]},`+
				`{"spanID":"root","parentSpanID":"","traceID":"t1","startTime":1,"duration":5,"endTime":6,"operationName":"agent","processID":"p","logs":[],"references":[],"tags":[]}]}],`+
				`"limit":0,"offset":0,"total":1}`)
			return
		}
		i := strings.Index(p, "/langfuse/api/public/")
		if i < 0 {
			t.Errorf("unexpected path %s", p)
			return
		}
		route := p[i+len("/langfuse/api/public/"):]
		key := r.Method + " " + route
		f.calls = append(f.calls, key)
		if body != nil {
			f.bodies[key] = body
		}
		parts := strings.Split(route, "/")

		switch {
		case key == "GET code-eval-starters":
			fmt.Fprint(w, `{"data":[{"id":"keyword-check","name":"Keyword check","category":"Content",`+
				`"description":"Words.","builtIn":true,"primaryScoreType":"BOOLEAN","primaryHigherIsBetter":false,"sourceCode":"def evaluate(ctx):\n    return None\n",`+
				`"params":[{"name":"required","type":"string_list","default":["refund"]}]}]}`)
		case key == "GET code-eval-library/files":
			extra := ""
			if f.badFiles {
				extra = `,{"path":"oodle_eval/../../escape.py","size":1},` +
					`{"path":"/tmp/abs.py","size":1},` +
					`{"path":"oodle_eval/..\\..\\winescape.py","size":1},` +
					`{"path":"oodle_eval/C:/x.py","size":1}`
			}
			fmt.Fprint(w, `{"files":[{"path":"oodle_eval/runtime.py","size":10},`+
				`{"path":"oodle_eval/v1/metrics.py","size":10}`+extra+`]}`)
		case strings.HasPrefix(key, "GET code-eval-library/files/"):
			path := strings.TrimPrefix(route, "code-eval-library/files/")
			if path == f.failFile {
				reply(500, map[string]string{"message": "storage down", "error": "x"})
				return
			}
			reply(200, map[string]string{"path": path, "source": "# " + path + "\n"})
		case key == "GET code-libraries":
			data := []any{}
			for _, name := range []string{"acme_policy", "acme_text"} {
				if _, ok := f.libs[name]; ok {
					data = append(data, f.libJSON(name))
				}
			}
			for name := range f.libs {
				if name != "acme_policy" && name != "acme_text" {
					data = append(data, f.libJSON(name))
				}
			}
			reply(200, map[string]any{"data": data})
		case key == "POST code-libraries":
			name, _ := body["name"].(string)
			src, _ := body["sourceCode"].(string)
			f.libs[name] = []string{src}
			reply(201, f.libJSON(name))
		case r.Method == "PATCH" && parts[0] == "code-libraries":
			name := strings.TrimPrefix(parts[1], "lib-")
			if _, ok := f.libs[name]; !ok {
				reply(404, map[string]string{"message": "code library not found", "error": "Not Found"})
				return
			}
			src, _ := body["sourceCode"].(string)
			f.libs[name] = append(f.libs[name], src)
			reply(200, f.libJSON(name))
		case r.Method == "GET" && parts[0] == "code-libraries" && len(parts) == 4:
			name := strings.TrimPrefix(parts[1], "lib-")
			v, _ := strconv.Atoi(parts[3])
			reply(200, map[string]any{"version": v, "createdAt": "x", "sourceCode": f.libs[name][v-1]})
		case key == "POST eval-templates/validate":
			problems := []any{}
			for _, p := range f.problems {
				problems = append(problems, p)
			}
			// The server knows only its own libraries.
			drafts, _ := body["libraries"].(map[string]any)
			if src, _ := body["sourceCode"].(string); src != "" {
				for _, name := range sharedImports(src) {
					_, stored := f.libs[name]
					if _, draft := drafts[name]; !stored && !draft {
						problems = append(problems, map[string]string{
							"field": "sourceCode",
							"message": "the code imports shared." + name +
								", and there is no shared library with that name",
						})
					}
				}
			}
			reply(200, map[string]any{"valid": len(problems) == 0, "problems": problems,
				"scoreInputs": []any{}, "libraries": []any{}, "notes": []any{}})
		case key == "POST eval-templates":
			body["id"] = "tpl-new"
			body["createdAt"], body["updatedAt"] = "x", "x"
			f.templates["tpl-new"] = body
			reply(201, body)
		case r.Method == "GET" && parts[0] == "eval-templates" && len(parts) == 2:
			tpl, ok := f.templates[parts[1]]
			if !ok {
				reply(404, map[string]string{"message": "template not found", "error": "Not Found"})
				return
			}
			reply(200, tpl)
		case r.Method == "PATCH" && parts[0] == "eval-templates":
			tpl := f.templates[parts[1]]
			for k, v := range body {
				tpl[k] = v
			}
			reply(200, tpl)
		case r.Method == "POST" && len(parts) == 3 && parts[2] == "test-run":
			fmt.Fprint(w, f.runReply)
		default:
			t.Errorf("unexpected %s", key)
			w.WriteHeader(404)
		}
	}))
}

func runWS(t *testing.T, url string, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd.PersistentFlags().StringP("output", "o", "", "")
	return runGenAICmd(t, url, output.FormatTable, cmd, args...)
}

func readWS(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func pullInto(t *testing.T, url, ref string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ws")
	if _, err := runWS(t, url, newGenAITemplatesPullCmd(), ref, dir); err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	return dir
}

func TestPullWritesTheLayout(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()

	dir := pullInto(t, srv.URL, "tpl-1")

	// The workspace is the staging directory after a rename, and
	// a private staging directory would hide it from other tools.
	if info, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o755 {
		t.Errorf("workspace mode = %v, want 0755", info.Mode().Perm())
	}
	if got := readWS(t, dir, "evaluate.py"); !strings.HasPrefix(got, "from shared.acme_text import clean") {
		t.Errorf("evaluate.py = %q", got)
	}
	var tpl map[string]any
	if err := yaml.Unmarshal([]byte(readWS(t, dir, "template.yaml")), &tpl); err != nil {
		t.Fatal(err)
	}
	if tpl["name"] != "Refund check" || tpl["type"] != "code" || tpl["higherIsBetter"] != true {
		t.Errorf("template.yaml = %v", tpl)
	}
	for _, k := range []string{"sourceCode", "id", "createdAt", "version", "prompt", "modelParams"} {
		if _, ok := tpl[k]; ok {
			t.Errorf("template.yaml has %q", k)
		}
	}
	if pins, _ := tpl["libraryPins"].(map[string]any); pins["acme_text"] != 1 {
		t.Errorf("libraryPins = %v", tpl["libraryPins"])
	}

	// A pinned library is written at its pin, another at its
	// latest version.
	if got := readWS(t, dir, "shared/acme_text.py"); got != "def clean(t):\n    return t\n" {
		t.Errorf("acme_text at the pin = %q", got)
	}
	if got := readWS(t, dir, "shared/acme_policy.py"); got != "LIMIT = 3\n" {
		t.Errorf("acme_policy = %q", got)
	}
	var lock sharedLock
	if err := yaml.Unmarshal([]byte(readWS(t, dir, ".oodle/shared.lock.yaml")), &lock); err != nil {
		t.Fatal(err)
	}
	if e := lock.Libraries["acme_text"]; e.Version != 1 || e.Latest != 2 || e.SHA256 == "" {
		t.Errorf("lock acme_text = %+v", e)
	}
	if !strings.Contains(readWS(t, dir, ".oodle/template.yaml"), "id: tpl-1") {
		t.Errorf("meta = %s", readWS(t, dir, ".oodle/template.yaml"))
	}

	// The reference source is there and read-only.
	info, err := os.Stat(filepath.Join(dir, "oodle_eval", "v1", "metrics.py"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Errorf("oodle_eval file is writable: %v", info.Mode())
	}

	stub := readWS(t, dir, "__builtins__.pyi")
	for _, want := range []string{
		"from oodle_eval.runtime import", "Score as Score",
		"EvaluationResult as EvaluationResult", "Scores as Scores",
		"ctx: EvaluationContext",
	} {
		if !strings.Contains(stub, want) {
			t.Errorf("stub lacks %q", want)
		}
	}
	var pyright map[string]any
	if err := json.Unmarshal([]byte(readWS(t, dir, "pyrightconfig.json")), &pyright); err != nil {
		t.Fatalf("pyrightconfig.json is not JSON: %v", err)
	}
	if pyright["typeCheckingMode"] != "basic" {
		t.Errorf("pyright = %v", pyright)
	}
	if !strings.Contains(readWS(t, dir, "README.md"), "absolute imports") {
		t.Error("README.md lacks the rules")
	}
}

func TestPullStarterAndNewHaveNoID(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()

	dir := pullInto(t, srv.URL, "starter:keyword-check")
	if strings.Contains(readWS(t, dir, ".oodle/template.yaml"), "id:") {
		t.Error("a starter pull recorded a template id")
	}
	if tpl := readWS(t, dir, "template.yaml"); !strings.Contains(tpl, "scoreType: boolean") ||
		!strings.Contains(tpl, "higherIsBetter: false") {
		t.Errorf("starter score fields missing:\n%s", tpl)
	}
	if !strings.Contains(readWS(t, dir, "template.yaml"), "- name: required") {
		t.Errorf("starter params missing:\n%s", readWS(t, dir, "template.yaml"))
	}

	dir = pullInto(t, srv.URL, "new")
	if !strings.Contains(readWS(t, dir, "evaluate.py"), "def evaluate(ctx: EvaluationContext)") {
		t.Errorf("new evaluate.py = %s", readWS(t, dir, "evaluate.py"))
	}
}

func TestPullRefusesANonEmptyDir(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err = %v, want a not-empty refusal", err)
	}
	if f.called("GET") != 0 {
		t.Error("a refused pull sent requests")
	}
	// --force writes, keeps the user's file, and replaces the
	// read-only files of an earlier pull.
	for i := 0; i < 2; i++ {
		if _, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir, "--force"); err != nil {
			t.Fatalf("pull --force %d: %v", i, err)
		}
	}
	if readWS(t, dir, "mine.txt") != "x" {
		t.Error("--force removed a file that pull does not write")
	}
}

func TestTemplatesTestSendsOnlyChangedLibraries(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")

	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_text.py"),
		[]byte("def clean(t):\n    return t.lower()\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_new.py"),
		[]byte("X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	params := writeTempFile(t, "p.yaml", "at: 0.7\n")
	scores := writeTempFile(t, "s.yaml", "Helpfulness:\n  - {name: Helpfulness, value: 0.8}\n")
	out, err := runWS(t, srv.URL, newGenAITemplatesTestCmd(), dir,
		"--span", "t1:s1", "--params", params, "--scores", scores)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"refund_ok", "true", "BOOLEAN", "Logs:", "checked 1 reply", "Duration: 12 ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	sent := f.body("POST eval-templates/tpl-1/test-run")
	libs, _ := sent["libraries"].(map[string]any)
	if len(libs) != 2 || libs["acme_text"] == nil || libs["acme_new"] == nil {
		t.Errorf("libraries = %v, want only acme_text and acme_new", libs)
	}
	if sent["traceId"] != "t1" || sent["spanId"] != "s1" {
		t.Errorf("span = %v/%v", sent["traceId"], sent["spanId"])
	}
	if !strings.Contains(fmt.Sprint(sent["sourceCode"]), "shared.acme_text") {
		t.Errorf("sourceCode = %v", sent["sourceCode"])
	}
	if sent["paramSpecs"] == nil || sent["libraryPins"] == nil || sent["scores"] == nil {
		t.Errorf("body lacks paramSpecs, libraryPins or scores: %v", sent)
	}
	if p, _ := sent["params"].(map[string]any); p["at"] != 0.7 {
		t.Errorf("params = %v", sent["params"])
	}
}

func TestTemplatesTestTraceUsesTheRootSpanAndFails(t *testing.T) {
	f := newFakeOodle(t)
	f.runReply = `{"error":{"code":"BLOCKED_IMPORT","message":"module 'os' is not allowed"},"logs":""}`
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "new")

	out, err := runWS(t, srv.URL, newGenAITemplatesTestCmd(), dir, "--trace", "t1")
	if err == nil {
		t.Fatal("a failed run did not fail the command")
	}
	if !strings.Contains(out, "BLOCKED_IMPORT: module 'os' is not allowed") {
		t.Errorf("output = %s", out)
	}
	// A workspace with no template id runs as a draft.
	sent := f.body("POST eval-templates/test/test-run")
	if sent["spanId"] != "root" || sent["startTimeUs"] == nil {
		t.Errorf("body = %v, want the root span and a window", sent)
	}
	if _, ok := sent["libraries"]; ok {
		t.Errorf("sent libraries that did not change: %v", sent["libraries"])
	}
}

func TestPushUpdatesChangedLibrariesAndTemplate(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")

	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_policy.py"),
		[]byte("LIMIT = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_new.py"),
		[]byte("X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "from shared.acme_new import X\nfrom shared.acme_policy import LIMIT\n\ndef evaluate(ctx):\n    return None\n"
	if err := os.WriteFile(filepath.Join(dir, "evaluate.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir, "--pin-libraries")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Validate: no problems.",
		"Created shared library acme_new (v1)",
		"Updated shared library acme_policy (v2)",
		`Updated template "Refund check" (tpl-1)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// Validate gets the new and changed libraries as drafts, and
	// not the unchanged one.
	vlibs, _ := f.body("POST eval-templates/validate")["libraries"].(map[string]any)
	if len(vlibs) != 2 || vlibs["acme_new"] != "X = 1\n" || vlibs["acme_policy"] != "LIMIT = 4\n" {
		t.Errorf("validate libraries = %v", vlibs)
	}
	if f.called("PATCH code-libraries/lib-acme_text") != 0 {
		t.Error("pushed acme_text, which did not change")
	}
	sent := f.body("PATCH eval-templates/tpl-1")
	if sent["sourceCode"] != src {
		t.Errorf("template source = %v", sent["sourceCode"])
	}
	pins, _ := sent["libraryPins"].(map[string]any)
	if pins["acme_new"] != float64(1) || pins["acme_policy"] != float64(2) || pins["acme_text"] != float64(1) {
		t.Errorf("libraryPins = %v", pins)
	}
	// The lock follows the push, so a second push has nothing
	// to send and sees no conflict.
	if _, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir); err != nil {
		t.Fatalf("second push: %v", err)
	}
	if n := f.called("PATCH code-libraries/"); n != 1 {
		t.Errorf("library PATCH count = %d, want 1", n)
	}
}

func TestPushCreatesATemplateAndRecordsItsID(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "starter:keyword-check")

	out, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `Created template "Keyword check" (tpl-new)`) {
		t.Errorf("output = %s", out)
	}
	if !strings.Contains(readWS(t, dir, ".oodle/template.yaml"), "id: tpl-new") {
		t.Error("the new id was not recorded")
	}
	sent := f.body("POST eval-templates")
	if sent["params"] == nil || sent["sourceCode"] == nil {
		t.Errorf("create body = %v", sent)
	}
}

func TestPushRefusesAServerChange(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")

	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_policy.py"),
		[]byte("LIMIT = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.libs["acme_policy"] = append(f.libs["acme_policy"], "LIMIT = 9\n")
	f.mu.Unlock()

	_, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir)
	if err == nil {
		t.Fatal("push replaced a server change")
	}
	for _, want := range []string{
		"acme_policy changed on the server after the pull (v1 then, v2 now)",
		"oodle genai code-libraries versions acme_policy --version 2 | diff -",
		"--force",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if f.called("PATCH") != 0 || f.called("POST code-libraries") != 0 ||
		f.body("POST eval-templates") != nil {
		t.Error("a refused push wrote something")
	}

	if _, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir, "--force"); err != nil {
		t.Fatalf("push --force: %v", err)
	}
	if f.called("PATCH code-libraries/lib-acme_policy") != 1 {
		t.Error("push --force did not update the library")
	}
}

func TestPushDryRunChangesNothing(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_new.py"),
		[]byte("X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockBefore := readWS(t, dir, ".oodle/shared.lock.yaml")

	out, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir, "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Plan (dry run", "create shared library acme_new", "update template tpl-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if f.called("POST code-libraries") != 0 || f.called("PATCH") != 0 {
		t.Error("a dry run wrote something")
	}
	if readWS(t, dir, ".oodle/shared.lock.yaml") != lockBefore {
		t.Error("a dry run changed the lock")
	}
}

func TestPushStopsOnValidationProblems(t *testing.T) {
	f := newFakeOodle(t)
	f.problems = []map[string]string{{"field": "params[0].default", "message": "must be a number"}}
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")

	out, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir)
	if err != errTemplateNotValid {
		t.Fatalf("err = %v, want errTemplateNotValid", err)
	}
	if !strings.Contains(out, "params[0].default: must be a number") {
		t.Errorf("output = %s", out)
	}
	if f.called("PATCH") != 0 || f.called("POST code-libraries") != 0 {
		t.Error("push wrote after a validation problem")
	}
}

func TestPushRefusesABadLibraryName(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	if err := os.WriteFile(filepath.Join(dir, "shared", "Bad-Name.py"),
		[]byte("X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir)
	if err == nil || !strings.Contains(err.Error(), "shared/Bad-Name.py") {
		t.Fatalf("err = %v, want a name error", err)
	}
}

// The import cases that the server checks,
// so that the CLI finds the imports that the server finds.
func TestSharedImports(t *testing.T) {
	cases := []struct {
		name, src string
		want      bool
	}{
		{"import", "import shared.acme\n", true},
		{"import as", "import shared.acme as a\n", true},
		{"from module", "from shared.acme import x\n", true},
		{"from package", "from shared import other, acme\n", true},
		{"longer name", "from shared.acme_v2 import x\n", false},
		{"other package", "from notshared.acme import x\n", false},
		{"comment", "# from shared import acme\n", false},
		{"import list", "import os, shared.b, shared.acme\n", true},
		{"from list in parentheses",
			"from shared import (\n    b,\n    acme as a,\n)\n", true},
		{"continued line", "from shared import b, \\\n    acme\n", true},
		{"after a semicolon", "x = 1; import shared.acme\n", true},
		{"indented", "def f():\n    import shared.acme\n", true},
	}
	for _, tc := range cases {
		if got := containsString(sharedImports(tc.src), "acme"); got != tc.want {
			t.Errorf("%s: found = %v, want %v (%v)", tc.name, got, tc.want, sharedImports(tc.src))
		}
	}
	got := strings.Join(sharedImports("import shared.a, shared.b\nfrom shared import (c,\n d)\n"), ",")
	if got != "c,d,a,b" {
		t.Errorf("sharedImports = %s", got)
	}
}

func TestTemplatesTestSpanFile(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "new")

	span := writeTempFile(t, "span.json",
		`{"span_id":"s1","trace_id":"t1","tags":{"gen_ai.completion":"Refund sent."}}`)
	if _, err := runWS(t, srv.URL, newGenAITemplatesTestCmd(), dir, "--span-file", span); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := f.body("POST eval-templates/test/test-run")
	data, _ := sent["spanData"].(map[string]any)
	tags, _ := data["tags"].(map[string]any)
	if data["span_id"] != "s1" || tags["gen_ai.completion"] != "Refund sent." {
		t.Errorf("spanData = %v", sent["spanData"])
	}
	if _, ok := sent["spanId"]; ok {
		t.Errorf("sent spanId with spanData: %v", sent)
	}

	if _, err := runWS(t, srv.URL, newGenAITemplatesTestCmd(), dir); err == nil {
		t.Error("a run with no span did not fail")
	}
}

// An import of a library that is neither stored nor a draft is a
// problem that push reports; nothing hides it.
func TestPushReportsAMissingLibrary(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	src := "from shared.nowhere import X\n\ndef evaluate(ctx):\n    return None\n"
	if err := os.WriteFile(filepath.Join(dir, "evaluate.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir)
	if err != errTemplateNotValid {
		t.Fatalf("err = %v, want errTemplateNotValid", err)
	}
	if !strings.Contains(out, "imports shared.nowhere") {
		t.Errorf("output = %s", out)
	}
}

// A judge run reports its error as a string, not {code, message}.
func TestTemplatesTestStringError(t *testing.T) {
	f := newFakeOodle(t)
	f.runReply = `{"error":"provider timed out"}`
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "new")

	out, err := runWS(t, srv.URL, newGenAITemplatesTestCmd(), dir, "--span", "t1:s1")
	if err != errTestRunFailed || !strings.Contains(out, "Error: provider timed out") {
		t.Errorf("err = %v, output = %s", err, out)
	}
}

// Editing a pinned library would make a new latest from an old
// version and drop the versions between for everyone.
func TestPushRefusesAnEditOfAnOldVersion(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1") // pins acme_text to v1; v2 is the latest

	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_text.py"),
		[]byte("def clean(t):\n    return t.upper()\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{dir}, {dir, "--dry-run"}} {
		_, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), args...)
		if err == nil {
			t.Fatalf("push %v did not refuse", args)
		}
		for _, want := range []string{
			"shared/acme_text.py is based on v1, but the server has v2",
			"pushing would replace v2",
			"oodle genai code-libraries versions acme_text --version 2",
			"--force",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("push %v: error lacks %q: %v", args, want, err)
			}
		}
	}
	if f.called("PATCH") != 0 {
		t.Error("a refused push wrote something")
	}
	out, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir, "--force", "--dry-run")
	if err != nil || !strings.Contains(out, "based on v1: replaces v2") {
		t.Errorf("dry run with --force: %v\n%s", err, out)
	}
	if _, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir, "--force"); err != nil {
		t.Fatalf("push --force: %v", err)
	}
	if n := len(f.libs["acme_text"]); n != 3 {
		t.Errorf("acme_text has %d versions, want 3", n)
	}
}

func TestVersionRange(t *testing.T) {
	if got := versionRange(2, 3); got != "v2–v3" {
		t.Errorf("versionRange(2, 3) = %q", got)
	}
	if got := versionRange(3, 3); got != "v3" {
		t.Errorf("versionRange(3, 3) = %q", got)
	}
}

// A library name or a file path from the server must not name a
// file outside the directory.
func TestPullSkipsUnsafeNamesAndPaths(t *testing.T) {
	f := newFakeOodle(t)
	f.libs["../../escaped_lib"] = []string{"X = 1\n"}
	f.libs["class"] = []string{"X = 1\n"}
	f.badFiles = true
	srv := f.server()
	defer srv.Close()

	root := t.TempDir()
	dir := filepath.Join(root, "a", "ws")
	out, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		`skipped the shared library "../../escaped_lib"`,
		`skipped the shared library "class"`,
		"oodle_eval/../../escape.py", "/tmp/abs.py", "winescape.py", "C:/x.py",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, p := range []string{
		filepath.Join(root, "escaped_lib.py"), filepath.Join(root, "escape.py"),
		filepath.Join(root, "a", "escape.py"), filepath.Join(dir, "shared", "class.py"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written", p)
		}
	}
	for _, p := range []string{"escape.py", "winescape.py"} {
		matches, _ := filepath.Glob(filepath.Join(root, "*", p))
		if len(matches) > 0 {
			t.Errorf("%v was written", matches)
		}
	}
}

func TestServerRelPath(t *testing.T) {
	for _, ok := range []string{"oodle_eval/a.py", "oodle_eval/v1/metrics/format.py"} {
		if err := serverRelPath(ok, "oodle_eval"); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "oodle_eval", "oodle_eval/", "oodle_eval/../x.py", "oodle_eval/./x.py",
		"oodle_eval//x.py", "/oodle_eval/x.py", "oodle_eval\\x.py",
		"oodle_eval/..\\..\\x.py", "other/x.py", "C:/oodle_eval/x.py",
	} {
		if err := serverRelPath(bad, "oodle_eval"); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// A setting, pin or clean value deleted from template.yaml must
// be cleared on the server; an update changes only what it sends.
func TestPushClearsDeletedFields(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	if err := os.WriteFile(filepath.Join(dir, "template.yaml"),
		[]byte("name: Refund check\ntype: code\nsourceCodeLanguage: python\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir); err != nil {
		t.Fatalf("push: %v", err)
	}
	sent := f.body("PATCH eval-templates/tpl-1")
	if p, ok := sent["params"].([]any); !ok || len(p) != 0 {
		t.Errorf("params = %#v, want []", sent["params"])
	}
	if p, ok := sent["libraryPins"].(map[string]any); !ok || len(p) != 0 {
		t.Errorf("libraryPins = %#v, want {}", sent["libraryPins"])
	}
	if sent["cleanValue"] != "" {
		t.Errorf("cleanValue = %#v, want \"\"", sent["cleanValue"])
	}

	// test sends them empty too, so that stored values do not
	// fill in.
	if _, err := runWS(t, srv.URL, newGenAITemplatesTestCmd(), dir, "--span", "t:s"); err != nil {
		t.Fatalf("test: %v", err)
	}
	run := f.body("POST eval-templates/tpl-1/test-run")
	if p, ok := run["paramSpecs"].([]any); !ok || len(p) != 0 {
		t.Errorf("paramSpecs = %#v, want []", run["paramSpecs"])
	}
	if p, ok := run["libraryPins"].(map[string]any); !ok || len(p) != 0 {
		t.Errorf("libraryPins = %#v, want {}", run["libraryPins"])
	}
}

// pull --force must not replace a local change unless
// --discard-local is set.
func TestPullForceKeepsLocalEdits(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")

	edit := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("evaluate.py", "def evaluate(ctx):\n    return 'mine'\n")
	edit("shared/acme_policy.py", "LIMIT = 99\n")
	edit("shared/mine_new.py", "Y = 2\n") // not on the server: kept

	_, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir, "--force")
	if err == nil {
		t.Fatal("pull --force replaced local edits")
	}
	for _, want := range []string{"evaluate.py", "shared/acme_policy.py", "--discard-local"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "mine_new") {
		t.Errorf("a new local file is listed as a conflict: %v", err)
	}
	if readWS(t, dir, "evaluate.py") != "def evaluate(ctx):\n    return 'mine'\n" {
		t.Error("a refused pull changed evaluate.py")
	}

	out, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir, "--force", "--discard-local")
	if err != nil {
		t.Fatalf("pull --discard-local: %v", err)
	}
	if !strings.Contains(out, "Replaced your change in evaluate.py") {
		t.Errorf("output = %s", out)
	}
	if readWS(t, dir, "shared/acme_policy.py") != "LIMIT = 3\n" {
		t.Error("--discard-local kept the edit")
	}
	if readWS(t, dir, "shared/mine_new.py") != "Y = 2\n" {
		t.Error("a new local library was removed")
	}
	// An unchanged workspace pulls again with --force.
	if _, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir, "--force"); err != nil {
		t.Errorf("pull --force of an unchanged dir: %v", err)
	}
	// After a push, the pushed files are the new base.
	edit("evaluate.py", "from shared.acme_text import clean\n\ndef evaluate(ctx):\n    return None\n")
	if _, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir); err != nil {
		t.Fatalf("push: %v", err)
	}
	if _, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir, "--force"); err != nil {
		t.Errorf("pull --force after a push: %v", err)
	}
}

// A library deleted on the server and unchanged here goes away
// on the next pull.
func TestPullRemovesADeletedLibrary(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	delete(f.libs, "acme_policy")

	out, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir, "--force")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Removed shared/acme_policy.py") {
		t.Errorf("output = %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "shared", "acme_policy.py")); err == nil {
		t.Error("the deleted library stayed")
	}
}

// evaluate.py holds the source byte for byte.
func TestPullKeepsTheSourceExact(t *testing.T) {
	f := newFakeOodle(t)
	f.templates["tpl-1"]["sourceCode"] = "def evaluate(ctx):\n    return None"
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	if got := readWS(t, dir, "evaluate.py"); got != "def evaluate(ctx):\n    return None" {
		t.Errorf("evaluate.py = %q", got)
	}
}

// A failed download leaves no directory, and leaves an existing
// one as it was.
func TestPullFailureChangesNothing(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	before := readWS(t, dir, ".oodle/shared.lock.yaml")
	f.libs["acme_policy"] = append(f.libs["acme_policy"], "LIMIT = 5\n")
	f.failFile = "oodle_eval/v1/metrics.py"

	if _, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", dir, "--force"); err == nil {
		t.Fatal("the pull did not fail")
	}
	if readWS(t, dir, ".oodle/shared.lock.yaml") != before {
		t.Error("a failed pull changed the lock")
	}
	if readWS(t, dir, "shared/acme_policy.py") != "LIMIT = 3\n" {
		t.Error("a failed pull changed a library")
	}
	fresh := filepath.Join(t.TempDir(), "fresh")
	if _, err := runWS(t, srv.URL, newGenAITemplatesPullCmd(), "tpl-1", fresh); err == nil {
		t.Fatal("the pull did not fail")
	}
	if _, err := os.Stat(fresh); err == nil {
		t.Error("a failed pull left a directory")
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), ".oodle-pull-*"))
	if len(leftovers) > 0 {
		t.Errorf("staging left behind: %v", leftovers)
	}
}

func TestTemplatesValidateDir(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_new.py"), []byte("X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newGenAITemplatesValidateCmd()
	out, err := runWS(t, srv.URL, cmd, "-d", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	sent := f.body("POST eval-templates/validate")
	libs, _ := sent["libraries"].(map[string]any)
	if len(libs) != 1 || libs["acme_new"] != "X = 1\n" {
		t.Errorf("libraries = %v", libs)
	}
	if !strings.Contains(fmt.Sprint(sent["sourceCode"]), "shared.acme_text") || sent["name"] != nil {
		t.Errorf("body = %v", sent)
	}
	if _, err := runWS(t, srv.URL, newGenAITemplatesValidateCmd()); err == nil {
		t.Error("validate with neither -f nor -d did not fail")
	}
}

// A pin in template.yaml that is not the pulled file's version
// is warned about.
func TestPinWarning(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	tpl := strings.Replace(readWS(t, dir, "template.yaml"), "acme_text: 1", "acme_text: 2", 1)
	if err := os.WriteFile(filepath.Join(dir, "template.yaml"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir, "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "template.yaml pins acme_text to v2, but shared/acme_text.py holds v1") {
		t.Errorf("output = %s", out)
	}
}

// A library deleted on the server with a local change is
// re-created only with --force.
func TestPushDeletedLibraryNeedsForce(t *testing.T) {
	f := newFakeOodle(t)
	srv := f.server()
	defer srv.Close()
	dir := pullInto(t, srv.URL, "tpl-1")
	if err := os.WriteFile(filepath.Join(dir, "shared", "acme_policy.py"), []byte("LIMIT = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	delete(f.libs, "acme_policy")

	_, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir)
	if err == nil || !strings.Contains(err.Error(),
		"acme_policy was deleted on the server after the pull. Push with --force to re-create it") {
		t.Fatalf("err = %v", err)
	}
	out, err := runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir, "--force", "--dry-run")
	if err != nil || !strings.Contains(out, "re-create shared library acme_policy") {
		t.Errorf("dry run: %v\n%s", err, out)
	}
	out, err = runWS(t, srv.URL, newGenAITemplatesPushCmd(), dir, "--force")
	if err != nil || !strings.Contains(out, "Created shared library acme_policy (v1)") {
		t.Errorf("push --force: %v\n%s", err, out)
	}
}
