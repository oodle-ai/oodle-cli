package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestEditCursorMcpConfig_KeepsOtherEntries checks that the Oodle entry is
// added and removed without a change to the user's other servers and keys.
func TestEditCursorMcpConfig_KeepsOtherEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	original := `{"mcpServers":{"github":{"url":"https://example.com/mcp"}},"other":1}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	entry := map[string]interface{}{"url": "https://eu1.api.oodle.ai/mcp/v1"}
	if err := editCursorMcpConfig(path, "oodle-ai", entry); err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]interface{})
	if servers["github"] == nil || servers["oodle-ai"] == nil || doc["other"] == nil {
		t.Errorf("config after add = %s", data)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != original {
		t.Errorf("backup = %q, want the original file", bak)
	}

	if err := editCursorMcpConfig(path, "oodle-ai", nil); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	_ = json.Unmarshal(data, &doc)
	servers = doc["mcpServers"].(map[string]interface{})
	if servers["oodle-ai"] != nil || servers["github"] == nil {
		t.Errorf("config after remove = %s", data)
	}
}

// TestEditCursorMcpConfig_InvalidJSON checks that a file the command cannot
// read is left as it is.
func TestEditCursorMcpConfig_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{ // comment\n}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := editCursorMcpConfig(path, "oodle-ai", map[string]interface{}{}); err == nil {
		t.Fatal("expected an error for a file that is not JSON")
	}
	if data, _ := os.ReadFile(path); string(data) != "{ // comment\n}" {
		t.Errorf("file changed to %q", data)
	}
}

// TestEditCursorMcpConfig_EmptyOrNull checks that an empty file or a "null"
// file gets the entry, and does not stop the command.
func TestEditCursorMcpConfig_EmptyOrNull(t *testing.T) {
	for _, content := range []string{"", "null\n"} {
		path := filepath.Join(t.TempDir(), "mcp.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := editCursorMcpConfig(path, "oodle-ai", map[string]interface{}{"url": "u"}); err != nil {
			t.Fatalf("content %q: %v", content, err)
		}
		var doc map[string]map[string]interface{}
		data, _ := os.ReadFile(path)
		if err := json.Unmarshal(data, &doc); err != nil || doc["mcpServers"]["oodle-ai"] == nil {
			t.Errorf("content %q: config = %s", content, data)
		}
	}
}
