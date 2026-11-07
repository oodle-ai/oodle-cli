package useragent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForMCPClient(t *testing.T) {
	proxy := String() + " (mcp-proxy)"
	cases := []struct {
		name, version, want string
	}{
		{"codex-mcp-client", "0.46.0", "codex-mcp-client/0.46.0 " + proxy},
		{"claude-code", "", "claude-code " + proxy},
		{"", "1.0", proxy},
		// The name comes from the agent, so it must not break the header.
		{"bad)\r\nX-Evil: 1", "1 (x)", "bad___X-Evil__1/1__x_ " + proxy},
	}
	for _, c := range cases {
		if got := ForMCPClient(c.name, c.version); got != c.want {
			t.Errorf("ForMCPClient(%q, %q) = %q, want %q", c.name, c.version, got, c.want)
		}
	}
}

func TestForMCPClientLimitsLength(t *testing.T) {
	got := ForMCPClient(strings.Repeat("a", 500), "")
	if len(got) > maxClientLen+len(" ")+len(String())+len(" (mcp-proxy)") {
		t.Errorf("User-Agent is %d bytes, want the client name cut to %d", len(got), maxClientLen)
	}
}

func TestTransport(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
	}))
	defer srv.Close()
	client := &http.Client{Transport: Transport(nil)}

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("User-Agent") != "" {
		t.Error("Transport changed the caller's request")
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("User-Agent", "kept/1")
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}

	want := []string{String(), "kept/1"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("User-Agents = %q, want %q", got, want)
	}
}
