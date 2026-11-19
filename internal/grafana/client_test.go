package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNewClientTLSVerificationIsDefault checks that the client verifies the
// Grafana server's TLS certificate unless the caller opts out explicitly.
func TestNewClientTLSVerificationIsDefault(t *testing.T) {
	defaultClient := NewClient("https://grafana.example", "token", false)
	tr, ok := defaultClient.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type %T", defaultClient.httpClient.Transport)
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("default client must verify TLS certificates")
	}

	insecureClient := NewClient("https://grafana.example", "token", true)
	tr, ok = insecureClient.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type %T", insecureClient.httpClient.Transport)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("opt-in client must skip TLS verification")
	}
}

// TestClientTLSVerificationAgainstSelfSignedServer is a reproducer: the default
// client must reject a server whose certificate it cannot verify, and the
// explicit opt-out must be the only way to reach it.
func TestClientTLSVerificationAgainstSelfSignedServer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "token", false).CheckHealth(context.Background()); err == nil {
		t.Fatal("default client reached a server with an unverifiable certificate")
	}

	if err := NewClient(srv.URL, "token", true).CheckHealth(context.Background()); err != nil {
		t.Fatalf("opt-in client failed to reach the server: %v", err)
	}
}
