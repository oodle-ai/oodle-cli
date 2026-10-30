package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// patchCursorConfig adds an OAuth MCP server entry to ~/.cursor/mcp.json.
// Cursor runs the OAuth login itself on the first use, so the entry holds
// no token. Cursor has no CLI that edits this file, so the command edits
// it and keeps every other entry.
func patchCursorConfig(name, deployment, toolsets string) error {
	domain, err := normalizeDomain(deployment)
	if err != nil {
		return err
	}
	domain = deploymentDomainForDomain(domain)
	clientID, err := oauthClientIDForDomain(domain)
	if err != nil {
		return fmt.Errorf("resolving OAuth client ID for %s: %w", deployment, err)
	}

	// The same URL and client ID that the Cursor card in the Oodle UI shows.
	endpointURL := fmt.Sprintf("https://%s/mcp/v1", oauthDeploymentDomainForDomain(domain))
	if toolsets != "" {
		endpointURL += "?toolsets=" + url.QueryEscape(toolsets)
	}

	path, _, err := mcpAgentConfigPath("cursor", "")
	if err != nil {
		return err
	}
	return editCursorMcpConfig(path, name, map[string]interface{}{
		"url":  endpointURL,
		"auth": map[string]interface{}{"CLIENT_ID": clientID},
	})
}

// editCursorMcpConfig sets the named server in an mcp.json file, or removes
// it when entry is nil. A file that is not valid JSON is left as it is,
// because a rewrite would lose what the user wrote. The old file is kept
// as <path>.bak.
func editCursorMcpConfig(path, name string, entry map[string]interface{}) error {
	doc := map[string]interface{}{}
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		// An empty file or a "null" file has no entries to keep.
		if len(bytes.TrimSpace(old)) > 0 {
			if err := json.Unmarshal(old, &doc); err != nil {
				return fmt.Errorf("%s is not valid JSON, so it is not changed: %w", path, err)
			}
		}
		if doc == nil {
			doc = map[string]interface{}{}
		}
	case os.IsNotExist(err):
		if entry == nil {
			return nil
		}
	default:
		return err
	}

	servers, _ := doc["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	if entry == nil {
		delete(servers, name)
	} else {
		servers[name] = entry
	}
	doc["mcpServers"] = servers

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if old != nil {
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return fmt.Errorf("writing backup: %w", err)
		}
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
