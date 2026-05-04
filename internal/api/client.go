package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/config"
	"golang.org/x/oauth2"
)

// Client wraps the generated OpenAPI client and adds Oodle-specific behaviour:
// API-key injection, retries on transient failures, and friendly error
// formatting.
type Client struct {
	// Inner is exported so command files can call generated methods directly.
	Inner  *client.ClientWithResponses
	Config *config.Config
}

// APIError is the structured representation of a non-2xx Oodle API response.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Cause      string
	Remedy     string
	TraceID    string
}

// Error implements the error interface.
func (e *APIError) Error() string {
	out := fmt.Sprintf("Error: %s (code: %s, trace: %s)", e.Message, e.Code, e.TraceID)
	if e.Cause != "" {
		out += "\nCause: " + e.Cause
	}
	if e.Remedy != "" {
		out += "\nRemedy: " + e.Remedy
	}
	return out
}

// retryTransport is an http.RoundTripper that retries transient failures with
// exponential backoff and jitter.
type retryTransport struct {
	base       http.RoundTripper
	maxRetries int

	// Tunables – overridable in tests.
	baseDelay  time.Duration
	maxDelay   time.Duration
	multiplier float64
	logger     io.Writer
	sleep      func(time.Duration)
	rng        *rand.Rand
}

// retryStatuses is the set of HTTP status codes considered transient.
var retryStatuses = map[int]bool{
	http.StatusTooManyRequests:     true, // 429
	http.StatusInternalServerError: true, // 500
	http.StatusBadGateway:          true, // 502
	http.StatusServiceUnavailable:  true, // 503
	http.StatusGatewayTimeout:      true, // 504
}

// idempotentMethods is the set of HTTP methods safe to retry. POST and PATCH
// are excluded because retrying them can duplicate side effects (e.g. create
// the same monitor twice if the server processed the original request but the
// response was lost).
var idempotentMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
	http.MethodPut:     true,
	http.MethodDelete:  true,
}

// isIdempotent returns true if requests using the given HTTP method can be
// safely retried.
func isIdempotent(method string) bool {
	return idempotentMethods[method]
}

func newRetryTransport(base http.RoundTripper, maxRetries int) *retryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	return &retryTransport{
		base:       base,
		maxRetries: maxRetries,
		baseDelay:  1 * time.Second,
		maxDelay:   30 * time.Second,
		multiplier: 2.0,
		logger:     os.Stderr,
		sleep:      time.Sleep,
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// RoundTrip executes the request, retrying transient failures. Only
// idempotent methods (GET, HEAD, OPTIONS, PUT, DELETE) are retried; POST and
// PATCH execute exactly once to avoid duplicating server-side side effects
// (e.g. creating the same monitor twice when a 5xx is returned after the
// resource was already persisted).
func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Non-idempotent methods bypass retry entirely.
	if !isIdempotent(req.Method) {
		return t.base.RoundTrip(req)
	}

	// Buffer the request body so it can be replayed on each retry.
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("reading request body: %w", err)
		}
		_ = req.Body.Close()
	}

	resetBody := func() {
		if bodyBytes == nil {
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
		req.ContentLength = int64(len(bodyBytes))
	}

	var lastResp *http.Response
	var lastErr error

	// Total attempts = maxRetries + 1 (the initial try).
	totalAttempts := t.maxRetries + 1
	for attempt := 0; attempt < totalAttempts; attempt++ {
		// Reset body before every attempt.
		if bodyBytes != nil {
			resetBody()
		}

		// Check ctx cancellation before each attempt.
		if err := req.Context().Err(); err != nil {
			return nil, err
		}

		resp, err := t.base.RoundTrip(req)
		lastResp, lastErr = resp, err

		// Successful or non-retryable: return immediately.
		if err == nil && !retryStatuses[resp.StatusCode] {
			return resp, nil
		}

		// If this was the last attempt, surface what we have.
		if attempt == totalAttempts-1 {
			break
		}

		// Drain & close the body so the connection can be reused.
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		delay := t.backoff(attempt)
		if err != nil {
			fmt.Fprintf(t.logger, "oodle: request error (attempt %d/%d): %v; retrying in %s\n",
				attempt+1, totalAttempts, err, delay)
		} else {
			fmt.Fprintf(t.logger, "oodle: server returned %d (attempt %d/%d); retrying in %s\n",
				resp.StatusCode, attempt+1, totalAttempts, delay)
		}
		t.sleep(delay)
	}

	return lastResp, lastErr
}

func (t *retryTransport) backoff(attempt int) time.Duration {
	d := float64(t.baseDelay) * math.Pow(t.multiplier, float64(attempt))
	if d > float64(t.maxDelay) {
		d = float64(t.maxDelay)
	}
	// Add 0–25% jitter.
	jitter := t.rng.Float64() * 0.25 * d
	return time.Duration(d + jitter)
}

// jsonFixTransport wraps a transport and normalizes text/plain responses to
// application/json. Some Oodle API endpoints return JSON bodies with
// Content-Type: text/plain, which causes oapi-codegen to skip JSON parsing.
// This transport fixes the Content-Type header so the generated client can
// parse the response correctly.
type jsonFixTransport struct {
	base http.RoundTripper
}

func (t *jsonFixTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.HasPrefix(ct, "text/plain") {
		resp.Header.Set("Content-Type", "application/json")
	}
	return resp, nil
}

// NewClient constructs a Client for the given config.
func NewClient(cfg *config.Config, maxRetries int) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	httpClient := &http.Client{
		Transport: &jsonFixTransport{base: newRetryTransport(http.DefaultTransport, maxRetries)},
		Timeout:   60 * time.Second,
	}

	var (
		mu          sync.Mutex
		tokenSource oauth2.TokenSource
	)
	if cfg.OAuthAccessToken != "" {
		tokenSource = buildOAuthTokenSource(cfg)
	}

	authEditor := func(_ context.Context, req *http.Request) error {
		if tokenSource != nil {
			tok, err := tokenSource.Token()
			if err == nil && tok != nil && tok.AccessToken != "" {
				req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
				maybePersistRefreshedOAuthToken(cfg, tok, &mu)
				return nil
			}
		}
		req.Header.Set("X-API-Key", cfg.APIKey)
		return nil
	}

	gen, err := client.NewClientWithResponses(
		cfg.APIURL,
		client.WithHTTPClient(httpClient),
		client.WithRequestEditorFn(authEditor),
	)
	if err != nil {
		return nil, fmt.Errorf("creating API client: %w", err)
	}

	return &Client{Inner: gen, Config: cfg}, nil
}

func buildOAuthTokenSource(cfg *config.Config) oauth2.TokenSource {
	if cfg == nil || cfg.OAuthAccessToken == "" {
		return nil
	}
	if cfg.OAuthRefreshToken == "" || cfg.OAuthClientID == "" || cfg.OAuthAuthServer == "" {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: cfg.OAuthAccessToken})
	}

	expiry, ok := cfg.OAuthExpiryTime()
	if !ok {
		// No persisted expiry means we should proactively refresh.
		expiry = time.Now().Add(-1 * time.Minute)
	}
	seed := &oauth2.Token{
		AccessToken:  cfg.OAuthAccessToken,
		RefreshToken: cfg.OAuthRefreshToken,
		Expiry:       expiry,
	}
	oauthCfg := &oauth2.Config{
		ClientID: cfg.OAuthClientID,
		Endpoint: oauth2.Endpoint{
			TokenURL:  strings.TrimRight(cfg.OAuthAuthServer, "/") + "/oauth/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	return oauth2.ReuseTokenSource(seed, oauthCfg.TokenSource(context.Background(), seed))
}

func maybePersistRefreshedOAuthToken(cfg *config.Config, tok *oauth2.Token, mu *sync.Mutex) {
	if cfg == nil || tok == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()

	expiry := cfg.OAuthTokenExpiry
	if !tok.Expiry.IsZero() {
		expiry = tok.Expiry.UTC().Format(time.RFC3339)
	}
	changed := cfg.OAuthAccessToken != tok.AccessToken ||
		(cfg.OAuthRefreshToken != tok.RefreshToken && tok.RefreshToken != "") ||
		cfg.OAuthTokenExpiry != expiry
	if !changed {
		return
	}

	cfg.OAuthAccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		cfg.OAuthRefreshToken = tok.RefreshToken
	}
	cfg.OAuthTokenExpiry = expiry
	_ = cfg.Save()
}

// CheckResponse returns nil for 2xx responses or an *APIError describing the
// failure for non-2xx responses.
func CheckResponse(resp *http.Response, body []byte) error {
	if resp == nil {
		return fmt.Errorf("nil response")
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	apiErr := &APIError{StatusCode: resp.StatusCode}

	// Try to parse the Oodle error envelope.
	var envelope client.OodleUtilHttputilsModelsErrors
	if len(body) > 0 && json.Unmarshal(body, &envelope) == nil && envelope.Errors != nil && len(*envelope.Errors) > 0 {
		first := (*envelope.Errors)[0]
		if first.Message != nil {
			apiErr.Message = *first.Message
		}
		if first.Code != nil {
			apiErr.Code = *first.Code
		}
		if first.Cause != nil {
			apiErr.Cause = *first.Cause
		}
		if first.Remedy != nil {
			apiErr.Remedy = *first.Remedy
		}
		if first.OodleTraceId != nil {
			apiErr.TraceID = *first.OodleTraceId
		}
	}

	if apiErr.Message == "" {
		// Fallback to raw body.
		if len(body) > 0 {
			apiErr.Message = string(bytes.TrimSpace(body))
		} else {
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
	}

	if resp.StatusCode == http.StatusUnauthorized {
		apiErr.Message = "Authentication failed. Check your API key or OAuth login."
	}

	return apiErr
}
