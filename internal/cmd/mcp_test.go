package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestMcpCmd_SkipsConfig(t *testing.T) {
	root := NewRootCmd()
	for _, path := range [][]string{
		{"mcp"},
		{"mcp", "setup"},
		{"mcp", "serve"},
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

func TestMcpEndpointURL(t *testing.T) {
	tests := []struct {
		deployment string
		instance   string
		want       string
	}{
		{"ap1", "oodle_internal", "https://ap1.oodle.ai/v1/api/instance/oodle_internal/mcp"},
		{"us1", "my-inst", "https://us1.oodle.ai/v1/api/instance/my-inst/mcp"},
		{"https://custom.oodle.ai", "inst-1", "https://custom.oodle.ai/v1/api/instance/inst-1/mcp"},
	}
	for _, tt := range tests {
		got, err := mcpEndpointURL(tt.deployment, tt.instance)
		if err != nil {
			t.Errorf("mcpEndpointURL(%q, %q) error: %v", tt.deployment, tt.instance, err)
			continue
		}
		if got != tt.want {
			t.Errorf("mcpEndpointURL(%q, %q) = %q, want %q", tt.deployment, tt.instance, got, tt.want)
		}
	}
}

func TestPatchClaudeCodeConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := patchClaudeCodeConfig(path, "oodle-ai", "ap1", "oodle_internal"); err != nil {
		t.Fatalf("patchClaudeCodeConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	servers, ok := cfg["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("mcpServers key missing or wrong type")
	}
	entry, ok := servers["oodle-ai"].(map[string]interface{})
	if !ok {
		t.Fatal("oodle-ai entry missing")
	}
	if entry["type"] != "http" {
		t.Errorf("type = %v, want http", entry["type"])
	}
	if entry["url"] != "https://ap1.oodle.ai/v1/api/instance/oodle_internal/mcp" {
		t.Errorf("url = %v, want MCP endpoint URL", entry["url"])
	}
	oauth, ok := entry["oauth"].(map[string]interface{})
	if !ok {
		t.Fatal("oauth key missing")
	}
	if oauth["clientId"] == "" {
		t.Error("clientId is empty")
	}
}

func TestPatchClaudeCodeConfig_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	existing := `{
  "env": {"FOO": "bar"},
  "mcpServers": {
    "other-server": {"command": "other"}
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := patchClaudeCodeConfig(path, "oodle-ai", "us1", "my-inst"); err != nil {
		t.Fatalf("patchClaudeCodeConfig: %v", err)
	}

	data, _ := os.ReadFile(path)
	var cfg map[string]interface{}
	json.Unmarshal(data, &cfg)

	// Check existing keys preserved.
	env, _ := cfg["env"].(map[string]interface{})
	if env["FOO"] != "bar" {
		t.Error("existing env key was lost")
	}
	servers, _ := cfg["mcpServers"].(map[string]interface{})
	if _, ok := servers["other-server"]; !ok {
		t.Error("existing MCP server was lost")
	}
	if _, ok := servers["oodle-ai"]; !ok {
		t.Error("new oodle-ai entry was not added")
	}
}

func TestPatchClaudeCodeConfig_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	patchClaudeCodeConfig(path, "oodle-ai", "ap1", "inst-1")
	data1, _ := os.ReadFile(path)

	patchClaudeCodeConfig(path, "oodle-ai", "ap1", "inst-1")
	data2, _ := os.ReadFile(path)

	if string(data1) != string(data2) {
		t.Error("second patch changed the file content")
	}
}

func TestPatchCodexConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := patchCodexConfig(path, "oodle-ai", "ap1"); err != nil {
		t.Fatalf("patchCodexConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	var cfg map[string]interface{}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		t.Fatalf("parsing TOML: %v", err)
	}

	servers, ok := cfg["mcp_servers"].(map[string]interface{})
	if !ok {
		t.Fatal("mcp_servers key missing")
	}
	entry, ok := servers["oodle-ai"].(map[string]interface{})
	if !ok {
		t.Fatal("oodle-ai entry missing")
	}
	if entry["command"] != "oodle" {
		t.Errorf("command = %v, want oodle", entry["command"])
	}
}

func TestPatchCodexConfig_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	existing := `model = "gpt-5.5"

[mcp_servers.other-server]
url = "https://example.com"
`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := patchCodexConfig(path, "oodle-ai", "ap1"); err != nil {
		t.Fatalf("patchCodexConfig: %v", err)
	}

	data, _ := os.ReadFile(path)
	var cfg map[string]interface{}
	toml.Decode(string(data), &cfg)

	if cfg["model"] != "gpt-5.5" {
		t.Error("existing model key was lost")
	}
	servers, _ := cfg["mcp_servers"].(map[string]interface{})
	if _, ok := servers["other-server"]; !ok {
		t.Error("existing MCP server was lost")
	}
	if _, ok := servers["oodle-ai"]; !ok {
		t.Error("new oodle-ai entry was not added")
	}
}

func TestMcpAgentConfigPath(t *testing.T) {
	tests := []struct {
		agent   string
		wantDir string
		kind    agentKind
		wantErr bool
	}{
		{"codex", ".codex", agentKindCodex, false},
		{"claude-code", ".claude", agentKindClaudeCode, false},
		{"claude", ".claude", agentKindClaudeCode, false},
		{"unknown", "", 0, true},
	}
	for _, tt := range tests {
		path, kind, err := mcpAgentConfigPath(tt.agent)
		if tt.wantErr {
			if err == nil {
				t.Errorf("mcpAgentConfigPath(%q) expected error", tt.agent)
			}
			continue
		}
		if err != nil {
			t.Errorf("mcpAgentConfigPath(%q) error: %v", tt.agent, err)
			continue
		}
		if kind != tt.kind {
			t.Errorf("mcpAgentConfigPath(%q) kind = %d, want %d", tt.agent, kind, tt.kind)
		}
		if !strings.Contains(path, tt.wantDir) {
			t.Errorf("mcpAgentConfigPath(%q) path = %q, want to contain %q", tt.agent, path, tt.wantDir)
		}
	}
}

func TestRelaySSE(t *testing.T) {
	sse := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"
	reader := strings.NewReader(sse)
	var buf strings.Builder

	err := relaySSE(nopCloser{reader}, &buf)
	if err != nil {
		t.Fatalf("relaySSE: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	if got != `{"jsonrpc":"2.0","id":1,"result":{}}` {
		t.Errorf("relaySSE output = %q, want JSON-RPC response", got)
	}
}

func TestRelaySSE_MultiLineData(t *testing.T) {
	// SSE spec allows multi-line data fields joined with \n.
	sse := "data: {\"jsonrpc\":\"2.0\",\ndata: \"id\":1,\"result\":{}}\n\n"
	reader := strings.NewReader(sse)
	var buf strings.Builder

	err := relaySSE(nopCloser{reader}, &buf)
	if err != nil {
		t.Fatalf("relaySSE: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	want := "{\"jsonrpc\":\"2.0\",\n\"id\":1,\"result\":{}}"
	if got != want {
		t.Errorf("relaySSE multi-line output = %q, want %q", got, want)
	}
}

func TestRelaySSE_MultipleEvents(t *testing.T) {
	sse := "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}}\n\n"
	reader := strings.NewReader(sse)
	var buf strings.Builder

	err := relaySSE(nopCloser{reader}, &buf)
	if err != nil {
		t.Fatalf("relaySSE: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"id":1`) {
		t.Errorf("first event = %q, want id:1", lines[0])
	}
	if !strings.Contains(lines[1], `"id":2`) {
		t.Errorf("second event = %q, want id:2", lines[1])
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func TestMcpProxyLoop_ForwardsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header is present (injected by transport).
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer srv.Close()

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"

	var outBuf strings.Builder
	var errBuf strings.Builder

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("Authorization", "Bearer test-token")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	err := runMcpProxyLoop(
		t.Context(),
		strings.NewReader(input),
		&outBuf,
		&errBuf,
		client,
		srv.URL,
	)
	if err != nil {
		t.Fatalf("runMcpProxyLoop: %v", err)
	}

	got := strings.TrimSpace(outBuf.String())
	if !strings.Contains(got, `"tools"`) {
		t.Errorf("output = %q, want tools/list response", got)
	}
}

func TestMcpProxyLoop_SSEResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"))
	}))
	defer srv.Close()

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	var outBuf strings.Builder

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("Authorization", "Bearer test-token")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	err := runMcpProxyLoop(t.Context(), strings.NewReader(input), &outBuf, &strings.Builder{}, client, srv.URL)
	if err != nil {
		t.Fatalf("runMcpProxyLoop: %v", err)
	}

	got := strings.TrimSpace(outBuf.String())
	if !strings.Contains(got, `"result"`) {
		t.Errorf("SSE relay output = %q, want JSON-RPC result", got)
	}
}

func TestWriteJSONRPCError(t *testing.T) {
	var buf strings.Builder
	writeJSONRPCError(&buf, 42, -32603, "test error")

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(buf.String()), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Error("missing jsonrpc field")
	}
	errObj, _ := resp["error"].(map[string]interface{})
	if errObj["message"] != "test error" {
		t.Errorf("error message = %v, want test error", errObj["message"])
	}
}
