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

	"github.com/oodle-ai/oodle-cli/internal/skills"
)

// runCmd builds a fresh root command, runs it with the given args, and
// returns combined stdout/stderr plus any error returned by Execute.
func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestSkillsCmd_SkipsConfig(t *testing.T) {
	root := NewRootCmd()
	for _, path := range [][]string{
		{"skills"},
		{"skills", "list"},
		{"skills", "install"},
		{"skills", "path"},
	} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("Find(%v): %v", path, err)
		}
		if !shouldSkipConfig(c) {
			t.Errorf("shouldSkipConfig(%v) = false, want true", path)
		}
	}
}

func TestSkillsList_TableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"oodle-cli","type":"dir"},{"name":"oodle-monitors","type":"dir"}]`))
	}))
	defer srv.Close()

	skills.SetContentsAPIURLOverride(srv.URL)
	defer skills.SetContentsAPIURLOverride("")

	out, err := runCmd(t, "skills", "list", "-o", "table")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "oodle-cli") {
		t.Errorf("output missing oodle-cli: %s", out)
	}
	if !strings.Contains(out, "oodle-monitors") {
		t.Errorf("output missing oodle-monitors: %s", out)
	}
}

func TestSkillsList_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"oodle-cli","type":"dir"},{"name":"oodle-monitors","type":"dir"}]`))
	}))
	defer srv.Close()

	skills.SetContentsAPIURLOverride(srv.URL)
	defer skills.SetContentsAPIURLOverride("")

	out, err := runCmd(t, "skills", "list", "-o", "json")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, out)
	}
	if len(parsed) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(parsed), parsed)
	}
	for i, e := range parsed {
		if _, ok := e["name"]; !ok {
			t.Errorf("entry %d missing 'name' key: %+v", i, e)
		}
	}
}

func TestSkillsInstall_NamedSkill(t *testing.T) {
	rawSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "---\nname: oodle-cli\n---\n# content")
	}))
	defer rawSrv.Close()

	skills.SetRawContentURLOverride(rawSrv.URL)
	defer skills.SetRawContentURLOverride("")

	tmp := t.TempDir()
	out, err := runCmd(t, "skills", "install", "oodle-cli", "--dir", tmp)
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	dest := filepath.Join(tmp, "oodle-cli", "SKILL.md")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("expected %s to exist: %v", dest, err)
	}
}

func TestSkillsInstall_AllSkills(t *testing.T) {
	listSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"oodle-cli","type":"dir"},{"name":"oodle-monitors","type":"dir"}]`))
	}))
	defer listSrv.Close()

	rawSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Path will be like "/oodle-cli/SKILL.md"
		parts := strings.Split(r.URL.Path, "/")
		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}
		_, _ = fmt.Fprintf(w, "---\nname: %s\n---\n# content", name)
	}))
	defer rawSrv.Close()

	skills.SetContentsAPIURLOverride(listSrv.URL)
	defer skills.SetContentsAPIURLOverride("")
	skills.SetRawContentURLOverride(rawSrv.URL)
	defer skills.SetRawContentURLOverride("")

	tmp := t.TempDir()
	out, err := runCmd(t, "skills", "install", "--dir", tmp)
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	for _, name := range []string{"oodle-cli", "oodle-monitors"} {
		dest := filepath.Join(tmp, name, "SKILL.md")
		if _, err := os.Stat(dest); err != nil {
			t.Errorf("expected %s to exist: %v", dest, err)
		}
	}
}

func TestSkillsInstall_NotFound(t *testing.T) {
	rawSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer rawSrv.Close()

	skills.SetRawContentURLOverride(rawSrv.URL)
	defer skills.SetRawContentURLOverride("")

	tmp := t.TempDir()
	out, err := runCmd(t, "skills", "install", "nonexistent", "--dir", tmp)
	if err == nil {
		t.Fatalf("expected error, got nil. output: %s", out)
	}
	if !strings.Contains(err.Error(), "skill not found: nonexistent") {
		t.Errorf("error = %q, want substring 'skill not found: nonexistent'", err.Error())
	}
}

func TestSkillsPath_ClaudeCode(t *testing.T) {
	out, err := runCmd(t, "skills", "path", "--target-agent", "claude-code")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, filepath.Join(".claude", "skills")) {
		t.Errorf("output missing .claude/skills: %s", out)
	}
}

func TestSkillsPath_Cursor(t *testing.T) {
	out, err := runCmd(t, "skills", "path", "--target-agent", "cursor")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, filepath.Join(".cursor", "skills")) {
		t.Errorf("output missing .cursor/skills: %s", out)
	}
}

func TestSkillsPath_Unknown(t *testing.T) {
	out, err := runCmd(t, "skills", "path", "--target-agent", "unknown-agent")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, filepath.Join(".agents", "skills")) {
		t.Errorf("output missing .agents/skills: %s", out)
	}
}
