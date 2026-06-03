package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/config"
	"golang.org/x/oauth2"
)

// TestMain isolates this package's tests from the developer's real config
// file. Several tests exercise the auth path end-to-end, which triggers
// MaybePersistRefreshedOAuthToken → cfg.Save() — without this hook, Save()
// would write to ~/.oodle/config.yaml and overwrite real credentials.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "oodle-cli-api-test-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	_ = os.Setenv("OODLE_CONFIG", filepath.Join(tmp, "config.yaml"))
	os.Exit(m.Run())
}

// fastRetryTransport returns a retryTransport with timing knobs zeroed for
// fast tests.
func fastRetryTransport(base http.RoundTripper, maxRetries int) *retryTransport {
	rt := newRetryTransport(base, maxRetries)
	rt.baseDelay = time.Microsecond
	rt.maxDelay = time.Millisecond
	rt.sleep = func(time.Duration) {}
	rt.logger = io.Discard
	return rt
}

func newTestClient(t *testing.T, baseURL string, maxRetries int) *Client {
	t.Helper()
	cfg := &config.Config{APIKey: "test-key", Instance: "test", APIURL: baseURL}
	httpClient := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, maxRetries)}
	authEditor := func(_ context.Context, req *http.Request) error {
		req.Header.Set("X-API-Key", cfg.APIKey)
		return nil
	}
	gen, err := client.NewClientWithResponses(baseURL,
		client.WithHTTPClient(httpClient),
		client.WithRequestEditorFn(authEditor))
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}
	return &Client{Inner: gen, Config: cfg}
}

func TestRetryOn500ThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 3)}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestNoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 3)}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (no retry)", got)
	}
}

func TestRetryOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 3)}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func TestMaxRetriesExceeded(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 2)}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	// 2 retries => 3 attempts.
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestAuthHeaderPresent(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errors": null}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, 1)
	_, err := c.Inner.ListApiKeysWithResponse(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ListApiKeysWithResponse: %v", err)
	}
	if seen != "test-key" {
		t.Errorf("X-API-Key = %q, want test-key", seen)
	}
}

func TestOAuthBearerPreferredOverAPIKey(t *testing.T) {
	var seenAuth, seenAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenAPIKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		APIKey:           "test-key",
		OAuthAccessToken: "oauth-access",
		Instance:         "demo",
		APIURL:           srv.URL,
	}
	c, err := NewClient(cfg, 1)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.Inner.ListApiKeysWithResponse(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ListApiKeysWithResponse: %v", err)
	}
	if seenAuth != "Bearer oauth-access" {
		t.Fatalf("Authorization = %q, want Bearer oauth-access", seenAuth)
	}
	if seenAPIKey != "" {
		t.Fatalf("X-API-Key should be empty when OAuth is used, got %q", seenAPIKey)
	}
}

func TestOAuthRefreshFlowAndPersist(t *testing.T) {
	var seenAuth string
	var tokenCalls int32

	cfg := &config.Config{
		OAuthAccessToken:  "expired-access",
		OAuthRefreshToken: "refresh-token",
		OAuthTokenExpiry:  time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
		OAuthClientID:     "client-id",
		Instance:          "demo",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			atomic.AddInt32(&tokenCalls, 1)
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.Form.Get("grant_type") != "refresh_token" {
				t.Fatalf("grant_type = %q, want refresh_token", r.Form.Get("grant_type"))
			}
			if r.Form.Get("refresh_token") != "refresh-token" {
				t.Fatalf("refresh_token = %q, want refresh-token", r.Form.Get("refresh_token"))
			}
			if r.Form.Get("client_id") != "client-id" {
				t.Fatalf("client_id = %q, want client-id", r.Form.Get("client_id"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"fresh-refresh","token_type":"Bearer","expires_in":3600}`))
			return
		}
		seenAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cfg.APIURL = srv.URL
	cfg.OAuthAuthServer = srv.URL

	c, err := NewClient(cfg, 1)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.Inner.ListApiKeysWithResponse(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ListApiKeysWithResponse: %v", err)
	}
	if seenAuth != "Bearer fresh-access" {
		t.Fatalf("Authorization = %q, want Bearer fresh-access", seenAuth)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", got)
	}
	if cfg.OAuthAccessToken != "fresh-access" {
		t.Fatalf("cfg.OAuthAccessToken = %q, want fresh-access", cfg.OAuthAccessToken)
	}
	if cfg.OAuthRefreshToken != "fresh-refresh" {
		t.Fatalf("cfg.OAuthRefreshToken = %q, want fresh-refresh", cfg.OAuthRefreshToken)
	}
	if cfg.OAuthTokenExpiry == "" {
		t.Fatal("cfg.OAuthTokenExpiry should be set after refresh")
	}
}

func TestCheckResponse_OodleErrorJSON(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"errors": []map[string]any{
			{
				"message":      "bad request",
				"code":         "INVALID_INPUT",
				"cause":        "missing field foo",
				"remedy":       "include foo",
				"oodleTraceId": "trace-123",
			},
		},
	})
	resp := &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(""))}
	err := CheckResponse(resp, body)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Message != "bad request" || apiErr.Code != "INVALID_INPUT" ||
		apiErr.Cause != "missing field foo" || apiErr.Remedy != "include foo" ||
		apiErr.TraceID != "trace-123" || apiErr.StatusCode != 400 {
		t.Errorf("unexpected APIError: %+v", apiErr)
	}
	msg := apiErr.Error()
	if !strings.Contains(msg, "bad request") || !strings.Contains(msg, "INVALID_INPUT") ||
		!strings.Contains(msg, "trace-123") || !strings.Contains(msg, "Cause:") ||
		!strings.Contains(msg, "Remedy:") {
		t.Errorf("Error() = %q", msg)
	}
}

func TestCheckResponse_MalformedJSON(t *testing.T) {
	body := []byte("not json at all")
	resp := &http.Response{StatusCode: http.StatusBadGateway}
	err := CheckResponse(resp, body)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if !strings.Contains(apiErr.Message, "not json at all") {
		t.Errorf("Message = %q, want raw body", apiErr.Message)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
}

func TestBuildOAuthTokenSourceWithoutRefreshFallsBackToStatic(t *testing.T) {
	cfg := &config.Config{
		OAuthAccessToken: "only-access",
	}
	ts := BuildOAuthTokenSource(cfg)
	if ts == nil {
		t.Fatal("BuildOAuthTokenSource returned nil")
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token() returned error: %v", err)
	}
	if tok.AccessToken != "only-access" {
		t.Fatalf("AccessToken = %q, want only-access", tok.AccessToken)
	}
}

func TestMaybePersistRefreshedOAuthTokenUpdatesConfig(t *testing.T) {
	cfg := &config.Config{
		OAuthAccessToken:  "old",
		OAuthRefreshToken: "old-refresh",
	}
	tok := &oauth2.Token{
		AccessToken:  "new",
		RefreshToken: "new-refresh",
		Expiry:       time.Now().Add(1 * time.Hour),
	}
	var mu sync.Mutex
	MaybePersistRefreshedOAuthToken(cfg, tok, &mu)
	if cfg.OAuthAccessToken != "new" {
		t.Fatalf("OAuthAccessToken = %q, want new", cfg.OAuthAccessToken)
	}
	if cfg.OAuthRefreshToken != "new-refresh" {
		t.Fatalf("OAuthRefreshToken = %q, want new-refresh", cfg.OAuthRefreshToken)
	}
	if cfg.OAuthTokenExpiry == "" {
		t.Fatal("OAuthTokenExpiry should be set")
	}
	if _, err := time.Parse(time.RFC3339, cfg.OAuthTokenExpiry); err != nil {
		t.Fatalf("OAuthTokenExpiry parse error: %v", err)
	}
}

func TestCheckResponse_401SpecialMessage(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusUnauthorized}
	err := CheckResponse(resp, nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if !strings.Contains(apiErr.Message, "Authentication failed") {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestCheckResponse_2xxNoError(t *testing.T) {
	resp := &http.Response{StatusCode: 200}
	if err := CheckResponse(resp, []byte(`{}`)); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestNewClient_DefaultsRetries(t *testing.T) {
	cfg := &config.Config{APIKey: "k", Instance: "i", APIURL: "https://example.com"}
	c, err := NewClient(cfg, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Inner == nil {
		t.Fatal("Inner is nil")
	}
}

func TestNewClient_ZeroRetriesAllowed(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	// With 0 retries, there should be exactly 1 attempt (no retries).
	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 0)}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (0 retries = 1 attempt)", got)
	}
}

// TestNoRetryOnPOST verifies that POST requests are NOT retried even on a
// retryable status, since retrying a non-idempotent mutation can duplicate
// server-side side effects (e.g. create the same monitor twice).
func TestNoRetryOnPOST(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 3)}
	resp, err := c.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (POST must not be retried)", got)
	}
}

// TestNoRetryOnPATCH verifies PATCH is treated as non-idempotent.
func TestNoRetryOnPATCH(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 3)}
	req, err := http.NewRequest(http.MethodPatch, srv.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (PATCH must not be retried)", got)
	}
}

// TestRetryOnPUT verifies idempotent PUT continues to be retried.
func TestRetryOnPUT(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 3)}
	req, err := http.NewRequest(http.MethodPut, srv.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2 (PUT should retry once)", got)
	}
}

// TestRetryOnDELETE verifies idempotent DELETE is retried.
func TestRetryOnDELETE(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &http.Client{Transport: fastRetryTransport(http.DefaultTransport, 3)}
	req, err := http.NewRequest(http.MethodDelete, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2 (DELETE should retry once)", got)
	}
}

func TestNewClient_ZeroRetriesViaNewClient(t *testing.T) {
	cfg := &config.Config{APIKey: "k", Instance: "i", APIURL: "https://example.com"}
	c, err := NewClient(cfg, 0)
	if err != nil {
		t.Fatalf("NewClient with 0 retries: %v", err)
	}
	if c.Inner == nil {
		t.Fatal("Inner is nil")
	}
}
