package cmd

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentPrompt(t *testing.T) {
	for _, tc := range []struct {
		agent, integration, want string
	}{
		{"claude-code", "", "/oodle-onboarding"},
		{"claude-code", "otel", "/oodle-onboarding set up the otel integration"},
		{"codex", "", "Use the oodle-onboarding skill to set up Oodle."},
		{"codex", "otel", "Use the oodle-onboarding skill to set up the otel integration."},
	} {
		if got := agentPrompt(tc.agent, tc.integration); got != tc.want {
			t.Errorf("agentPrompt(%q, %q) = %q, want %q", tc.agent, tc.integration, got, tc.want)
		}
	}
}

func TestLoggedIn(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  authStatusRow
		want bool
	}{
		{"no credentials", authStatusRow{PreferredAuth: "none"}, false},
		{"api key", authStatusRow{PreferredAuth: "api-key"}, true},
		{"valid oauth", authStatusRow{PreferredAuth: "oauth"}, true},
		{"expired with refresh", authStatusRow{PreferredAuth: "oauth", OAuthExpired: true, RefreshEnabled: true}, true},
		{"expired without refresh", authStatusRow{PreferredAuth: "oauth", OAuthExpired: true}, false},
	} {
		if got := loggedIn(tc.row); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSetupRejectsBadIntegration(t *testing.T) {
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"setup", "--integration", "otel; rm -rf /"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not an integration type") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartableAgents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tools are shell scripts")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	got := startableAgents([]string{"claude-code", "codex", "cursor"})
	if len(got) != 1 || got[0].name != "codex" {
		t.Fatalf("got %v, want only codex (claude is not on PATH)", got)
	}
	if got := startableAgents([]string{"cursor"}); len(got) != 0 {
		t.Fatalf("got %v, want none (codex is not in the agent list)", got)
	}
}

func TestChooseAgent(t *testing.T) {
	agents := []startableAgent{{"claude-code", "Claude Code", "claude"}, {"codex", "Codex", "codex"}}
	for _, tc := range []struct {
		answer string
		want   string
		ok     bool
	}{
		{"\n", "claude-code", true},
		{"2\n", "codex", true},
		{"n\n", "", false},
		{"7\n", "", false},
	} {
		var out bytes.Buffer
		got, ok := chooseAgent(bufio.NewReader(strings.NewReader(tc.answer)), &out, agents, "app")
		if ok != tc.ok || got.name != tc.want {
			t.Errorf("answer %q: got %q %v, want %q %v", tc.answer, got.name, ok, tc.want, tc.ok)
		}
	}

	// One agent: a yes or no question, and an empty answer means yes.
	got, ok := chooseAgent(bufio.NewReader(strings.NewReader("\n")), &bytes.Buffer{}, agents[:1], "app")
	if !ok || got.name != "claude-code" {
		t.Errorf("one agent: got %q %v", got.name, ok)
	}
}
