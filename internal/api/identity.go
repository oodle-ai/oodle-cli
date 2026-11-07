package api

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
)

// Headers that name the client on every request to Oodle. The Oodle
// servers read them to attribute work to oodle-cli, and to tell a CLI
// run by an AI coding agent apart from one run by a person. Without
// them a CLI request looks the same as any other Go program.
const (
	// ClientHeader holds "oodle-cli/<version>".
	ClientHeader = "OODLE-CLIENT"
	// ClientAgentHeader holds the name of the AI coding agent that
	// runs the CLI. It is not sent when no agent is detected.
	ClientAgentHeader = "OODLE-CLIENT-AGENT"
	// ClientCommandHeader holds the command path without the binary
	// name, for example "logs query".
	ClientCommandHeader = "OODLE-CLIENT-COMMAND"
)

// ClientInfo identifies this CLI process to the Oodle servers.
type ClientInfo struct {
	Version string
	Command string
	Agent   string
}

var (
	clientInfoMu sync.RWMutex
	clientInfo   = ClientInfo{Version: "dev"}
)

// SetClientInfo records the identity that every later request sends.
// The root command calls it once, before any client is built. A process
// runs one command, so a package-level value is enough.
func SetClientInfo(info ClientInfo) {
	info.Version = headerValue(info.Version)
	if info.Version == "" {
		info.Version = "dev"
	}
	info.Command = headerValue(info.Command)
	info.Agent = headerValue(info.Agent)

	clientInfoMu.Lock()
	defer clientInfoMu.Unlock()
	clientInfo = info
}

func currentClientInfo() ClientInfo {
	clientInfoMu.RLock()
	defer clientInfoMu.RUnlock()
	return clientInfo
}

// UserAgent returns the User-Agent this CLI sends. Traces record the
// User-Agent of each request, so it names the CLI, the agent and the
// command even where the servers do not read the OODLE-CLIENT headers.
func UserAgent() string {
	info := currentClientInfo()
	parts := []string{runtime.GOOS + "/" + runtime.GOARCH}
	if info.Agent != "" {
		parts = append(parts, "agent="+info.Agent)
	}
	if info.Command != "" {
		parts = append(parts, "cmd="+info.Command)
	}
	return fmt.Sprintf(
		"oodle-cli/%s (%s)",
		info.Version,
		strings.Join(parts, "; "),
	)
}

// ApplyClientHeaders sets the User-Agent and the OODLE-CLIENT headers on
// req. Use it for a request that does not go through a client from this
// package.
func ApplyClientHeaders(req *http.Request) {
	info := currentClientInfo()
	req.Header.Set("User-Agent", UserAgent())
	req.Header.Set(ClientHeader, "oodle-cli/"+info.Version)
	if info.Agent != "" {
		req.Header.Set(ClientAgentHeader, info.Agent)
	} else {
		req.Header.Del(ClientAgentHeader)
	}
	if info.Command != "" {
		req.Header.Set(ClientCommandHeader, info.Command)
	} else {
		req.Header.Del(ClientCommandHeader)
	}
}

// clientHeadersTransport applies the client headers to every request.
type clientHeadersTransport struct {
	base http.RoundTripper
}

// NewClientHeadersTransport wraps base so that every request names this
// CLI. A nil base means http.DefaultTransport.
func NewClientHeadersTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &clientHeadersTransport{base: base}
}

func (t *clientHeadersTransport) RoundTrip(
	req *http.Request,
) (*http.Response, error) {
	// A RoundTripper must not change the request it receives.
	req = req.Clone(req.Context())
	ApplyClientHeaders(req)
	return t.base.RoundTrip(req)
}

// headerValue removes the characters that cannot appear in a header
// value, so that a strange version string or env value cannot break the
// request.
func headerValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}
