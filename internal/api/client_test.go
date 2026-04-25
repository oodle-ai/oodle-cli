package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/config"
)

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
