package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/config"
)

func setClientInfoForTest(t *testing.T, info ClientInfo) {
	t.Helper()
	previous := currentClientInfo()
	SetClientInfo(info)
	t.Cleanup(func() { SetClientInfo(previous) })
}

func headerServer(t *testing.T) (*httptest.Server, *http.Header) {
	t.Helper()
	seen := &http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errors": null}`))
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func TestGeneratedClientSendsClientHeaders(t *testing.T) {
	setClientInfoForTest(t, ClientInfo{Version: "1.2.3", Command: "logs query", Agent: "claude-code"})
	srv, seen := headerServer(t)

	c, err := NewClient(&config.Config{APIKey: "test-key", Instance: "test", APIURL: srv.URL}, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Only the request matters here, not how the body is parsed.
	_, _ = c.Inner.ListApiKeysWithResponse(context.Background(), "demo")

	want := map[string]string{
		"User-Agent":        "oodle-cli/1.2.3 (" + runtime.GOOS + "/" + runtime.GOARCH + "; agent=claude-code; cmd=logs query)",
		ClientHeader:        "oodle-cli/1.2.3",
		ClientAgentHeader:   "claude-code",
		ClientCommandHeader: "logs query",
		"X-API-Key":         "test-key",
	}
	for name, value := range want {
		if got := seen.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestAuthedHTTPClientSendsClientHeaders(t *testing.T) {
	setClientInfoForTest(t, ClientInfo{Version: "1.2.3", Command: "traces search"})
	srv, seen := headerServer(t)

	c := newTestClient(t, srv.URL, 0)
	resp, err := c.NewAuthedHTTPClient(5 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()

	if got := seen.Get(ClientHeader); got != "oodle-cli/1.2.3" {
		t.Errorf("%s = %q, want oodle-cli/1.2.3", ClientHeader, got)
	}
	if got := seen.Get(ClientCommandHeader); got != "traces search" {
		t.Errorf("%s = %q, want traces search", ClientCommandHeader, got)
	}
	// With no agent, the agent header is absent, so that the server
	// reads the request as one that a person or a script sent.
	if got, ok := (*seen)[http.CanonicalHeaderKey(ClientAgentHeader)]; ok {
		t.Errorf("%s = %q, want no header", ClientAgentHeader, got)
	}
}

func TestSetClientInfoRemovesCharactersHeadersCannotHold(t *testing.T) {
	setClientInfoForTest(t, ClientInfo{Version: " 1.0\r\n", Command: "logs\tquery", Agent: "agent\x00"})

	got := currentClientInfo()
	want := ClientInfo{Version: "1.0", Command: "logsquery", Agent: "agent"}
	if got != want {
		t.Errorf("client info = %+v, want %+v", got, want)
	}
}

func TestSetClientInfoDefaultsTheVersion(t *testing.T) {
	setClientInfoForTest(t, ClientInfo{})

	if got := currentClientInfo().Version; got != "dev" {
		t.Errorf("version = %q, want dev", got)
	}
}

func TestClientHeadersTransportDoesNotChangeTheCallersRequest(t *testing.T) {
	setClientInfoForTest(t, ClientInfo{Version: "1.2.3"})
	srv, _ := headerServer(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := NewClientHeadersTransport(nil).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if got := req.Header.Get(ClientHeader); got != "" {
		t.Errorf("caller's request has %s = %q, want no header", ClientHeader, got)
	}
}
