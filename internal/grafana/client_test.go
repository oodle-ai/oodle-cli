package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClient_RejectsUntrustedCertificateByDefault proves that the Grafana
// client authenticates the server before it sends the service account token.
// A self-signed endpoint is rejected unless the caller explicitly opts out of
// verification, so a host on the network path cannot impersonate Grafana.
func TestClient_RejectsUntrustedCertificateByDefault(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		},
	))
	defer srv.Close()

	ctx := context.Background()

	// The test server uses a self-signed certificate, so the default client
	// must refuse to talk to it.
	if err := NewClient(srv.URL, "token", false).CheckHealth(ctx); err == nil {
		t.Fatal("CheckHealth succeeded against a self-signed certificate; " +
			"TLS verification is disabled by default")
	}

	// The explicit opt-in keeps the self-signed server reachable.
	if err := NewClient(srv.URL, "token", true).CheckHealth(ctx); err != nil {
		t.Fatalf("CheckHealth with InsecureSkipVerify failed: %v", err)
	}
}
