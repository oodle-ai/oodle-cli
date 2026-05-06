package skills

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentEnvVars lists every env var consulted by DetectAgent so individual
// tests can clear them all to ensure deterministic behavior.
var agentEnvVars = []string{
	"CLAUDECODE", "CLAUDE_CODE",
	"CURSOR_AGENT",
	"CODEX", "OPENAI_CODEX",
	"OPENCODE",
	"AIDER",
	"CLINE",
	"WINDSURF_AGENT",
	"GITHUB_COPILOT",
	"AMAZON_Q", "AWS_Q_DEVELOPER",
	"GEMINI_CODE_ASSIST",
	"SRC_CODY",
	"AGENT",
}

// clearAgentEnv unsets all agent detection env vars for the duration of the test.
func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, k := range agentEnvVars {
		t.Setenv(k, "")
	}
}

func TestDetectAgent_ClaudeCode_CLAUDECODE(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv("CLAUDECODE", "1")
	if got := DetectAgent(); got != "claude-code" {
		t.Errorf("DetectAgent() = %q, want claude-code", got)
	}
}

func TestDetectAgent_ClaudeCode_CLAUDE_CODE(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv("CLAUDE_CODE", "1")
	if got := DetectAgent(); got != "claude-code" {
		t.Errorf("DetectAgent() = %q, want claude-code", got)
	}
}

func TestDetectAgent_Cursor(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv("CURSOR_AGENT", "1")
	if got := DetectAgent(); got != "cursor" {
		t.Errorf("DetectAgent() = %q, want cursor", got)
	}
}

func TestDetectAgent_Codex(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv("CODEX", "1")
	if got := DetectAgent(); got != "codex" {
		t.Errorf("DetectAgent() = %q, want codex", got)
	}
}

func TestDetectAgent_Windsurf(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv("WINDSURF_AGENT", "1")
	if got := DetectAgent(); got != "windsurf" {
		t.Errorf("DetectAgent() = %q, want windsurf", got)
	}
}

func TestDetectAgent_GeminiCode(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv("GEMINI_CODE_ASSIST", "1")
	if got := DetectAgent(); got != "gemini-code" {
		t.Errorf("DetectAgent() = %q, want gemini-code", got)
	}
}

func TestDetectAgent_AmazonQ(t *testing.T) {
	clearAgentEnv(t)
	t.Setenv("AMAZON_Q", "1")
	if got := DetectAgent(); got != "amazon-q" {
		t.Errorf("DetectAgent() = %q, want amazon-q", got)
	}
}

func TestDetectAgent_None(t *testing.T) {
	clearAgentEnv(t)
	if got := DetectAgent(); got != "" {
		t.Errorf("DetectAgent() = %q, want empty string", got)
	}
}

func TestSkillsDir_ClaudeCode_NoExisting(t *testing.T) {
	tmp := t.TempDir()
	got := SkillsDir("claude-code", tmp)
	if !strings.HasSuffix(got, filepath.Join(".claude", "skills")) {
		t.Errorf("SkillsDir(claude-code) = %q, want suffix .claude/skills", got)
	}
}

func TestSkillsDir_Cursor_NoExisting(t *testing.T) {
	tmp := t.TempDir()
	got := SkillsDir("cursor", tmp)
	if !strings.HasSuffix(got, filepath.Join(".cursor", "skills")) {
		t.Errorf("SkillsDir(cursor) = %q, want suffix .cursor/skills", got)
	}
}

func TestSkillsDir_Unknown_NoExisting(t *testing.T) {
	tmp := t.TempDir()
	got := SkillsDir("", tmp)
	if !strings.HasSuffix(got, filepath.Join(".agents", "skills")) {
		t.Errorf("SkillsDir(\"\") = %q, want suffix .agents/skills", got)
	}
}

func TestSkillsDir_ExistingCursorDir(t *testing.T) {
	tmp := t.TempDir()
	cursorDir := filepath.Join(tmp, ".cursor", "skills")
	if err := os.MkdirAll(cursorDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got := SkillsDir("claude-code", tmp)
	if got != cursorDir {
		t.Errorf("SkillsDir(claude-code) = %q, want %q (existing .cursor/skills should win)", got, cursorDir)
	}
}

func TestFindProjectRoot_WithGit(t *testing.T) {
	tmp := t.TempDir()
	// On macOS, t.TempDir often returns a /var/folders/... path which is a
	// symlink to /private/var/folders/...; resolve it so the comparison is
	// stable regardless of which form Getwd returns.
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(resolved, ".git"), 0755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	subdir := filepath.Join(resolved, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	got := FindProjectRoot()
	if got != resolved {
		t.Errorf("FindProjectRoot() = %q, want %q", got, resolved)
	}
}

func TestFindProjectRoot_NoGit(t *testing.T) {
	tmp := t.TempDir()
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Chdir(resolved); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	got := FindProjectRoot()
	if got != resolved {
		t.Errorf("FindProjectRoot() = %q, want %q", got, resolved)
	}
}

func TestExtractDescription_Found(t *testing.T) {
	in := "---\nname: foo\ndescription: bar baz\n---\n"
	if got := extractDescription(in); got != "bar baz" {
		t.Errorf("extractDescription = %q, want %q", got, "bar baz")
	}
}

func TestExtractDescription_Missing(t *testing.T) {
	if got := extractDescription("no frontmatter"); got != "" {
		t.Errorf("extractDescription = %q, want empty", got)
	}
}

func TestList_Integration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"oodle-monitors","type":"dir"},{"name":"oodle-cli","type":"dir"},{"name":"README.md","type":"file"}]`))
	}))
	defer srv.Close()

	SetContentsAPIURLOverride(srv.URL)
	defer SetContentsAPIURLOverride("")

	entries, err := List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[0].Name != "oodle-cli" {
		t.Errorf("entries[0].Name = %q, want oodle-cli", entries[0].Name)
	}
	if entries[1].Name != "oodle-monitors" {
		t.Errorf("entries[1].Name = %q, want oodle-monitors", entries[1].Name)
	}
}

func TestFetchContent_Found(t *testing.T) {
	body := "---\nname: oodle-cli\ndescription: test\n---\n# content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()

	SetRawContentURLOverride(srv.URL)
	defer SetRawContentURLOverride("")

	got, err := FetchContent(context.Background(), "oodle-cli")
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	if got != body {
		t.Errorf("FetchContent = %q, want %q", got, body)
	}
}

func TestFetchContent_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	SetRawContentURLOverride(srv.URL)
	defer SetRawContentURLOverride("")

	_, err := FetchContent(context.Background(), "oodle-cli")
	if err == nil {
		t.Fatal("FetchContent: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "skill not found: oodle-cli") {
		t.Errorf("error = %q, want substring 'skill not found: oodle-cli'", err.Error())
	}
}

func TestFetchAllContents_Parallel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract skill name from path like "/skill-a/SKILL.md"
		parts := strings.Split(r.URL.Path, "/")
		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}
		_, _ = fmt.Fprintf(w, "---\nname: %s\ndescription: desc for %s\n---\n# %s", name, name, name)
	}))
	defer srv.Close()

	SetRawContentURLOverride(srv.URL)
	defer SetRawContentURLOverride("")

	entries := []Entry{
		{Name: "skill-a"},
		{Name: "skill-b"},
		{Name: "skill-c"},
		{Name: "skill-d"},
		{Name: "skill-e"},
		{Name: "skill-f"},
	}

	results := FetchAllContents(context.Background(), entries)
	if len(results) != len(entries) {
		t.Fatalf("got %d results, want %d", len(results), len(entries))
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("results[%d] (%s): unexpected error: %v", i, r.Name, r.Err)
			continue
		}
		if r.Name != entries[i].Name {
			t.Errorf("results[%d].Name = %q, want %q", i, r.Name, entries[i].Name)
		}
		if !strings.Contains(r.Content, entries[i].Name) {
			t.Errorf("results[%d].Content missing skill name %q", i, entries[i].Name)
		}
	}
}

func TestFetchAllContents_PartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}
		if name == "bad-skill" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, "---\nname: %s\n---\n# %s", name, name)
	}))
	defer srv.Close()

	SetRawContentURLOverride(srv.URL)
	defer SetRawContentURLOverride("")

	entries := []Entry{
		{Name: "good-skill"},
		{Name: "bad-skill"},
		{Name: "another-good"},
	}

	results := FetchAllContents(context.Background(), entries)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("results[0] unexpected error: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Error("results[1] expected error for bad-skill, got nil")
	} else if !strings.Contains(results[1].Err.Error(), "skill not found: bad-skill") {
		t.Errorf("results[1].Err = %q, want substring 'skill not found: bad-skill'", results[1].Err.Error())
	}
	if results[2].Err != nil {
		t.Errorf("results[2] unexpected error: %v", results[2].Err)
	}
}

func TestFetchAllContents_ContextCancelled(t *testing.T) {
	// Use a server that blocks until the request context is done.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	SetRawContentURLOverride(srv.URL)
	defer SetRawContentURLOverride("")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	entries := []Entry{
		{Name: "skill-a"},
		{Name: "skill-b"},
	}

	results := FetchAllContents(ctx, entries)
	for i, r := range results {
		if r.Err == nil {
			t.Errorf("results[%d] expected error due to cancelled context, got nil", i)
		}
	}
}

func TestFetchAllContents_Empty(t *testing.T) {
	results := FetchAllContents(context.Background(), nil)
	if len(results) != 0 {
		t.Errorf("got %d results for nil entries, want 0", len(results))
	}

	results = FetchAllContents(context.Background(), []Entry{})
	if len(results) != 0 {
		t.Errorf("got %d results for empty entries, want 0", len(results))
	}
}
