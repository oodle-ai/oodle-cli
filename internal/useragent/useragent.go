// Package useragent names the CLI on every HTTP request it sends.
//
// Without a User-Agent, Go sends "Go-http-client/2.0". The Oodle servers
// then cannot tell the CLI from any other Go program, so a heavy query
// cannot be traced back to the tool or the agent that sent it.
package useragent

import (
	"net/http"
	"strings"
	"sync"
)

const product = "oodle-cli"

// maxClientLen keeps a long or hostile client name from filling the header.
const maxClientLen = 64

var (
	mu      sync.RWMutex
	version = "dev"
)

// SetVersion sets the version that String reports.
func SetVersion(v string) {
	if v = sanitize(v); v == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	version = v
}

// String returns the CLI User-Agent, for example "oodle-cli/1.4.0".
func String() string {
	mu.RLock()
	defer mu.RUnlock()
	return product + "/" + version
}

// ForMCPClient returns the User-Agent of the MCP proxy for one agent, for
// example "codex-mcp-client/0.46.0 oodle-cli/1.4.0 (mcp-proxy)". The agent
// name comes from the clientInfo of its MCP initialize request.
//
// The agent comes first on purpose. The Oodle MCP server takes the client
// name from the first product token when a request has no clientInfo,
// which is every request after initialize on older protocol versions.
// With the proxy first, every call would name oodle-cli, not the agent.
func ForMCPClient(name, version string) string {
	proxy := String() + " (mcp-proxy)"
	client := sanitize(name)
	if client == "" {
		return proxy
	}
	if v := sanitize(version); v != "" {
		client += "/" + v
	}
	return client + " " + proxy
}

// Transport returns a RoundTripper that sets the CLI User-Agent on a
// request that has none. A nil base means http.DefaultTransport.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base}
}

type transport struct {
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") != "" {
		return t.base.RoundTrip(req)
	}
	// A RoundTripper must not change the request it was given.
	clone := req.Clone(req.Context())
	clone.Header.Set("User-Agent", String())
	return t.base.RoundTrip(clone)
}

// sanitize keeps the characters that are safe in a User-Agent product
// token. The client name comes from the agent, so it is not trusted to be
// a valid header value.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if b.Len() >= maxClientLen {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == '+':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
