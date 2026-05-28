package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/config"
)

type agentKind int

const (
	agentKindClaudeCode agentKind = iota
	agentKindCodex
)

func newMcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP server integrations for AI coding agents",
		Long: `Set up and run a local MCP proxy that authenticates with the Oodle API.

The proxy runs as a stdio process that AI agents (Codex, Claude Code) launch
automatically. It injects a fresh OAuth Bearer token on every request,
so no tokens are stored in agent config files.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newMcpSetupCmd())
	cmd.AddCommand(newMcpServeCmd())
	return cmd
}

// ---------------------------------------------------------------------------
// mcp setup
// ---------------------------------------------------------------------------

func newMcpSetupCmd() *cobra.Command {
	var deployment string
	var name string

	cmd := &cobra.Command{
		Use:   "setup <agent>",
		Short: "Configure an AI agent to use Oodle via a local MCP proxy",
		Long: `Patches the agent's configuration file to register an Oodle MCP server
that runs as a local stdio proxy (oodle mcp serve).

Supported agents: codex, claude-code

Examples:
  oodle mcp setup codex --deployment ap1
  oodle mcp setup claude-code -d us1 --name my-oodle`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMcpSetup(cmd, args[0], deployment, name)
		},
	}
	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment (us1, ap1, or full URL)")
	cmd.Flags().StringVar(&name, "name", "oodle-ai", "MCP server name in agent config")
	_ = cmd.MarkFlagRequired("deployment")
	return cmd
}

func runMcpSetup(cmd *cobra.Command, agent, deployment, name string) error {
	out := cmd.OutOrStdout()

	cfg, err := loadExistingConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg == nil || cfg.OAuthAccessToken == "" {
		return fmt.Errorf("no OAuth credentials found; run 'oodle auth login --deployment %s' first", deployment)
	}

	if strings.TrimSpace(cfg.Instance) == "" {
		return fmt.Errorf("no instance configured; run 'oodle auth login --deployment %s' first", deployment)
	}

	path, kind, err := mcpAgentConfigPath(agent)
	if err != nil {
		return err
	}

	if err := patchAgentConfig(kind, name, deployment, cfg.Instance); err != nil {
		return fmt.Errorf("patching %s config: %w", agent, err)
	}

	fmt.Fprintf(out, "Configured MCP server %q in %s\n", name, path)

	// Validate by listing tools via the proxy.
	fmt.Fprintf(out, "Verifying MCP connectivity...\n")
	if err := verifyMcpSetup(cmd.Context(), cfg, deployment); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: MCP verification failed: %v\n", err)
		fmt.Fprintln(cmd.ErrOrStderr(), "The config was written; check 'oodle auth status' and try 'oodle mcp serve' manually.")
	} else {
		fmt.Fprintln(out, "MCP connectivity verified (initialize succeeded).")
	}

	fmt.Fprintf(out, "\nRestart %s to pick up the new MCP server.\n", agentDisplayName(agent))
	return nil
}

func agentDisplayName(agent string) string {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude-code", "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	default:
		return agent
	}
}

func mcpAgentConfigPath(agent string) (string, agentKind, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", 0, fmt.Errorf("determining home directory: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude-code", "claude":
		return filepath.Join(home, ".claude", "settings.json"), agentKindClaudeCode, nil
	case "codex":
		return filepath.Join(home, ".codex", "config.toml"), agentKindCodex, nil
	default:
		return "", 0, fmt.Errorf("unsupported agent %q; supported: codex, claude-code", agent)
	}
}

func patchAgentConfig(kind agentKind, name, deployment, instance string) error {
	switch kind {
	case agentKindClaudeCode:
		return patchClaudeCodeConfig(name, deployment, instance)
	case agentKindCodex:
		return patchCodexConfig(name, deployment)
	default:
		return fmt.Errorf("unknown agent kind %d", kind)
	}
}

func patchClaudeCodeConfig(name, deployment, instance string) error {
	domain, err := normalizeDomain(deployment)
	if err != nil {
		return err
	}
	domain = deploymentDomainForDomain(domain)

	// Claude Code uses OAuth, so the MCP URL must use the OAuth deployment
	// domain — the protected resource metadata `resource` field must match
	// the URL origin.
	oauthDomain := oauthDeploymentDomainForDomain(domain)
	endpointURL := fmt.Sprintf("https://%s/v1/api/instance/%s/mcp", oauthDomain, instance)

	clientID, err := oauthClientIDForDomain(domain)
	if err != nil {
		return fmt.Errorf("resolving OAuth client ID for %s: %w", deployment, err)
	}

	// Use `claude mcp add` so Claude Code's own CLI manages the config file.
	// Remove first to make the operation idempotent.
	claudeBin := findClaudeBinary()
	_ = exec.Command(claudeBin, "mcp", "remove", name).Run()

	cmd := exec.Command(claudeBin, "mcp", "add",
		name, endpointURL,
		"--transport", "http",
		"--client-id", clientID,
		"--callback-port", "9400",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running '%s mcp add': %w", claudeBin, err)
	}
	return nil
}

func patchCodexConfig(name, deployment string) error {
	codexBin := findCodexBinary()

	// Remove first to make the operation idempotent.
	_ = exec.Command(codexBin, "mcp", "remove", name).Run()

	// codex mcp add <name> -- oodle mcp serve --deployment <dep>
	cmd := exec.Command(codexBin, "mcp", "add",
		name,
		"--", "oodle", "mcp", "serve", "--deployment", deployment,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running '%s mcp add': %w", codexBin, err)
	}
	return nil
}

// verifyMcpSetup sends an initialize JSON-RPC request to the remote MCP
// endpoint to verify auth and connectivity.
func verifyMcpSetup(ctx context.Context, cfg *config.Config, deployment string) error {
	endpointURL, err := mcpEndpointURL(deployment, cfg.Instance)
	if err != nil {
		return err
	}

	ts := api.BuildOAuthTokenSource(cfg)
	if ts == nil {
		return fmt.Errorf("no OAuth token source available")
	}

	tok, err := ts.Token()
	if err != nil {
		return fmt.Errorf("obtaining token: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"oodle-cli","version":"1.0.0"}}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", endpointURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("MCP endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// ---------------------------------------------------------------------------
// mcp serve
// ---------------------------------------------------------------------------

func newMcpServeCmd() *cobra.Command {
	var deployment string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a stdio MCP proxy with automatic OAuth token injection",
		Long: `Reads JSON-RPC requests from stdin, injects a fresh OAuth Bearer token,
forwards them to the remote Oodle MCP endpoint via Streamable HTTP,
and writes responses to stdout.

This command is intended to be launched by AI coding agents (Codex,
Claude Code) as a stdio MCP server. Use 'oodle mcp setup' to configure
the agent automatically.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMcpServe(cmd, deployment)
		},
	}
	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment (us1, ap1, or full URL)")
	_ = cmd.MarkFlagRequired("deployment")
	return cmd
}

func runMcpServe(cmd *cobra.Command, deployment string) error {
	errOut := cmd.ErrOrStderr()

	cfg, err := loadExistingConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg == nil || cfg.OAuthAccessToken == "" {
		return fmt.Errorf("no OAuth credentials found; run 'oodle auth login --deployment %s' first", deployment)
	}

	instance := cfg.Instance
	if strings.TrimSpace(instance) == "" {
		return fmt.Errorf("no instance configured; run 'oodle auth login --deployment %s' first", deployment)
	}

	endpointURL, err := mcpEndpointURL(deployment, instance)
	if err != nil {
		return err
	}

	ts := api.BuildOAuthTokenSource(cfg)
	if ts == nil {
		return fmt.Errorf("unable to build OAuth token source; run 'oodle auth login' first")
	}

	// Validate token at startup.
	if _, err := ts.Token(); err != nil {
		return fmt.Errorf("token validation failed: %w\nRun 'oodle auth login --deployment %s' to re-authenticate", err, deployment)
	}

	fmt.Fprintf(errOut, "oodle mcp: proxying to %s\n", endpointURL)

	var mu sync.Mutex
	httpClient := &http.Client{
		// No Timeout — SSE streams may be long-lived. The context on each
		// request handles cancellation instead.
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			tok, err := ts.Token()
			if err != nil {
				return nil, fmt.Errorf("obtaining OAuth token: %w", err)
			}
			req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
			api.MaybePersistRefreshedOAuthToken(cfg, tok, &mu)
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	return runMcpProxyLoop(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), errOut, httpClient, endpointURL)
}

// roundTripFunc adapts a function to the http.RoundTripper interface.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func runMcpProxyLoop(ctx context.Context, in io.Reader, out io.Writer, errOut io.Writer, httpClient *http.Client, endpointURL string) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB max line

	// Track the Mcp-Session-Id header for Streamable HTTP session continuity.
	var sessionID string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		// Extract JSON-RPC id for error responses.
		var rpcID interface{}
		var msg struct {
			ID interface{} `json:"id"`
		}
		if json.Unmarshal(line, &msg) == nil {
			rpcID = msg.ID
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(line))
		if err != nil {
			writeJSONRPCError(out, rpcID, -32603, fmt.Sprintf("building request: %v", err))
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			fmt.Fprintf(errOut, "oodle mcp: request error: %v\n", err)
			writeJSONRPCError(out, rpcID, -32603, fmt.Sprintf("transport error: %v", err))
			continue
		}

		func() {
			defer resp.Body.Close()

			// Capture session ID from the server.
			if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
				sessionID = sid
			}

			ct := resp.Header.Get("Content-Type")
			if strings.HasPrefix(ct, "text/event-stream") {
				if err := relaySSE(resp.Body, out); err != nil {
					fmt.Fprintf(errOut, "oodle mcp: SSE relay error: %v\n", err)
				}
			} else {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					fmt.Fprintf(errOut, "oodle mcp: reading response: %v\n", err)
					writeJSONRPCError(out, rpcID, -32603, "error reading response")
				} else {
					_, _ = out.Write(body)
					if len(body) > 0 && body[len(body)-1] != '\n' {
						_, _ = out.Write([]byte("\n"))
					}
				}
			}
		}()
	}

	return scanner.Err()
}

func relaySSE(body io.ReadCloser, out io.Writer) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	// Accumulate data lines per SSE event; a blank line terminates an event.
	var dataBuf strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if len(data) > 0 && data[0] == ' ' {
				data = data[1:] // strip single leading space per SSE spec
			}
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(data)
		} else if line == "" {
			// Blank line = end of event. Flush accumulated data.
			if dataBuf.Len() > 0 {
				_, _ = fmt.Fprintln(out, dataBuf.String())
				dataBuf.Reset()
			}
		}
		// Ignore event:, id:, retry:, and comment lines.
	}
	// Flush any trailing data that wasn't terminated by a blank line.
	if dataBuf.Len() > 0 {
		_, _ = fmt.Fprintln(out, dataBuf.String())
	}
	return scanner.Err()
}

func writeJSONRPCError(out io.Writer, id interface{}, code int, message string) {
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	data, _ := json.Marshal(resp)
	_, _ = out.Write(data)
	_, _ = out.Write([]byte("\n"))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mcpEndpointURL(deployment, instance string) (string, error) {
	domain, err := normalizeDomain(deployment)
	if err != nil {
		return "", err
	}
	domain = deploymentDomainForDomain(domain)
	return fmt.Sprintf("https://%s/v1/api/instance/%s/mcp", domain, instance), nil
}

// findClaudeBinary returns the path to the Claude Code CLI binary.
// It checks ~/.claude/local/claude first (Claude Code's standard install
// location), then falls back to whatever "claude" resolves to on PATH.
func findClaudeBinary() string {
	home, err := os.UserHomeDir()
	if err == nil {
		localBin := filepath.Join(home, ".claude", "local", "claude")
		if _, err := os.Stat(localBin); err == nil {
			return localBin
		}
	}
	return "claude"
}

// findCodexBinary returns the path to the Codex CLI binary.
func findCodexBinary() string {
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	return "codex"
}
