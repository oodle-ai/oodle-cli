package cmd

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

func TestSameDeployment(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"us1", "https://us1.oodle.ai", true},
		{"us1.oodle.ai", "https://us1.oodle.ai/", true},
		{"https://prod-02-us-west-2.api.oodle.ai", "us1", true},
		{"https://us1.oodle.ai", "app-dev.oodle.ai", false},
		{"us1", "ap1", false},
		{"", "us1", false},
		{"", "", false},
	} {
		if got := sameDeployment(tc.a, tc.b); got != tc.want {
			t.Errorf("sameDeployment(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestHasBrowserLogin(t *testing.T) {
	for _, tc := range []struct {
		deployment string
		want       bool
	}{
		{"us1", true},
		{"https://eu1.oodle.ai", true},
		{"app-dev.oodle.ai", false},
		{"https://oodle.example.com", false},
	} {
		if got := hasBrowserLogin(tc.deployment); got != tc.want {
			t.Errorf("hasBrowserLogin(%q) = %v, want %v", tc.deployment, got, tc.want)
		}
	}
}

// runSetupLogin runs setupLogin with a saved config and no terminal, and
// returns the step that it gives and what it printed.
func runSetupLogin(t *testing.T, config []string, deployment string) (string, string) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(strings.Join(config, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("OODLE_CONFIG", cfgPath)
	for _, v := range []string{"OODLE_API_KEY", "OODLE_INSTANCE", "OODLE_DEPLOYMENT",
		"OODLE_API_URL", "OODLE_URL", "OODLE_OAUTH_ACCESS_TOKEN", "OODLE_OAUTH_REFRESH_TOKEN"} {
		t.Setenv(v, "")
	}
	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	step := setupLogin(cmd, &rootFlags{}, deployment)
	return step, out.String()
}

func TestSetupLoginKeepsLoginForSameDeployment(t *testing.T) {
	step, out := runSetupLogin(t, []string{
		"api_key: k", "instance: inst-a", "api_url: https://us1.oodle.ai",
	}, "us1")
	if step != "" {
		t.Errorf("step = %q, want none", step)
	}
	if !strings.Contains(out, "logged in to us1.oodle.ai (instance inst-a)") {
		t.Errorf("output does not name the deployment and instance: %q", out)
	}
}

func TestSetupLoginRejectsLoginForOtherDeployment(t *testing.T) {
	step, out := runSetupLogin(t, []string{
		"api_key: k", "instance: inst-a", "api_url: https://us1.oodle.ai",
	}, "eu1")
	if !strings.Contains(out, "logged in to us1.oodle.ai, not to eu1.oodle.ai") {
		t.Errorf("output does not name both deployments: %q", out)
	}
	if strings.Contains(out, "✓") {
		t.Errorf("output reports a login: %q", out)
	}
	if step != "Log in to Oodle: oodle auth login --deployment eu1" {
		t.Errorf("step = %q", step)
	}
}

func TestSetupLoginGivesAPIKeyStepWithoutBrowserLogin(t *testing.T) {
	step, out := runSetupLogin(t, []string{
		"api_key: k", "instance: inst-a", "api_url: https://us1.oodle.ai",
	}, "app-dev.oodle.ai")
	if !strings.Contains(out, "app-dev.oodle.ai has no browser login") {
		t.Errorf("output does not say that there is no browser login: %q", out)
	}
	if strings.Contains(step, "oodle auth login") || !strings.Contains(step, "oodle configure --api-url https://app-dev.oodle.ai ") {
		t.Errorf("step = %q, want the API key step", step)
	}
}

func TestSetupLoginAcceptsAPIKeyFromEnvironment(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("OODLE_CONFIG", cfgPath)
	t.Setenv("OODLE_API_KEY", "k")
	t.Setenv("OODLE_INSTANCE", "inst-dev")
	t.Setenv("OODLE_DEPLOYMENT", "https://app-dev.oodle.ai")
	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	if step := setupLogin(cmd, &rootFlags{}, "app-dev.oodle.ai"); step != "" {
		t.Errorf("step = %q, want none; output %q", step, out.String())
	}
}
