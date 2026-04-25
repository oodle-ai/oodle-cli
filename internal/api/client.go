package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/config"
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

// RoundTrip executes the request, retrying transient failures.
func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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
	d := float64(t.baseDelay)
	for i := 0; i < attempt; i++ {
		d *= t.multiplier
	}
	if d > float64(t.maxDelay) {
		d = float64(t.maxDelay)
	}
	// Add 0–25% jitter.
	jitter := t.rng.Float64() * 0.25 * d
	return time.Duration(d + jitter)
}

// NewClient constructs a Client for the given config.
func NewClient(cfg *config.Config, maxRetries int) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if maxRetries <= 0 {
		maxRetries = 3
	}
	httpClient := &http.Client{
		Transport: newRetryTransport(http.DefaultTransport, maxRetries),
		Timeout:   60 * time.Second,
	}

	apiKey := cfg.APIKey
	authEditor := func(_ context.Context, req *http.Request) error {
		req.Header.Set("X-API-Key", apiKey)
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
		apiErr.Message = "Authentication failed. Check your API key."
	}

	return apiErr
}
