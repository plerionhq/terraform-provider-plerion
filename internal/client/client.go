// Package client is a small HTTP client for the Plerion customer API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	defaultTimeout    = 60 * time.Second
	defaultMaxRetries = 4
	defaultRetryWait  = time.Second
	maxRetryWait      = 30 * time.Second
	maxErrorBodyBytes = 64 * 1024
)

// Config holds the settings used to build a Client.
type Config struct {
	// BaseURL is the regional API root, for example https://au.api.plerion.com.
	BaseURL string
	// APIKey is a tenant API key, sent as a bearer token.
	APIKey string
	// UserAgent identifies the caller in API logs.
	UserAgent string
	// HTTPClient overrides the default HTTP client. Optional.
	HTTPClient *http.Client
	// RetryWait is the first backoff interval between retries. Optional.
	RetryWait time.Duration
	// MaxRetries is how many times a retryable request is retried. Optional;
	// a negative value disables retries.
	MaxRetries int
}

// Client calls the Plerion customer API.
type Client struct {
	baseURL    *url.URL
	apiKey     string
	userAgent  string
	httpClient *http.Client
	retryWait  time.Duration
	maxRetries int
}

// New validates the config and returns a Client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("an API key is required")
	}

	baseURL, err := ParseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	retryWait := cfg.RetryWait
	if retryWait <= 0 {
		retryWait = defaultRetryWait
	}

	maxRetries := cfg.MaxRetries
	switch {
	case maxRetries == 0:
		maxRetries = defaultMaxRetries
	case maxRetries < 0:
		maxRetries = 0
	}

	return &Client{
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		userAgent:  cfg.UserAgent,
		httpClient: httpClient,
		retryWait:  retryWait,
		maxRetries: maxRetries,
	}, nil
}

// ParseBaseURL checks that rawURL is an absolute HTTPS URL with no path, query
// or fragment. Plain HTTP is accepted for loopback hosts so tests can run
// against a local server.
func ParseBaseURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%q is not a valid URL. Use the full API address, for example https://au.api.plerion.com", rawURL)
	}

	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopback(parsed.Hostname()) {
			return nil, fmt.Errorf("%q uses http. The Plerion API only accepts https", rawURL)
		}
	default:
		return nil, fmt.Errorf("%q must start with https://", rawURL)
	}

	if strings.Trim(parsed.Path, "/") != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%q must not include a path, query or fragment. Use only the scheme and host, for example https://au.api.plerion.com", rawURL)
	}

	parsed.Path = ""
	return parsed, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// FieldError is one entry of the errors array the API returns.
type FieldError struct {
	Field   string `json:"field,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// APIError is returned for any response outside the 2xx range.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Errors     []FieldError
	Message    string
}

func (e *APIError) Error() string {
	var details []string
	for _, fieldError := range e.Errors {
		switch {
		case fieldError.Field != "" && fieldError.Message != "":
			details = append(details, fieldError.Field+": "+fieldError.Message)
		case fieldError.Message != "":
			details = append(details, fieldError.Message)
		case fieldError.Code != "":
			details = append(details, fieldError.Code)
		}
	}
	if len(details) == 0 && e.Message != "" {
		details = append(details, e.Message)
	}

	summary := fmt.Sprintf("%s %s returned %d %s", e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode))
	if len(details) == 0 {
		return summary
	}
	return summary + ": " + strings.Join(details, "; ")
}

// Detail returns the API's own explanation of the error, without the request
// line, or an empty string when the API gave none.
func (e *APIError) Detail() string {
	var details []string
	for _, fieldError := range e.Errors {
		if fieldError.Message != "" {
			details = append(details, fieldError.Message)
		}
	}
	if len(details) == 0 {
		return e.Message
	}
	return strings.Join(details, "; ")
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	return hasStatus(err, http.StatusNotFound)
}

// IsConflict reports whether err is a 409 from the API.
func IsConflict(err error) bool {
	return hasStatus(err, http.StatusConflict)
}

// IsUnauthorized reports whether err is a 401 from the API.
func IsUnauthorized(err error) bool {
	return hasStatus(err, http.StatusUnauthorized)
}

// IsForbidden reports whether err is a 403 from the API.
func IsForbidden(err error) bool {
	return hasStatus(err, http.StatusForbidden)
}

func hasStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

// do sends one API request, retrying where it is safe to, and decodes a JSON
// response into out when out is not nil.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		payload = encoded
	}

	endpoint := c.baseURL.JoinPath(path)
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}

	for attempt := 0; ; attempt++ {
		responseBody, status, header, err := c.send(ctx, method, endpoint.String(), payload)

		tflog.Debug(ctx, "Plerion API request", map[string]any{
			"method":  method,
			"path":    path,
			"status":  status,
			"attempt": attempt + 1,
		})

		if err == nil && status >= 200 && status < 300 {
			if out == nil || len(bytes.TrimSpace(responseBody)) == 0 {
				return nil
			}
			if decodeErr := json.Unmarshal(responseBody, out); decodeErr != nil {
				return fmt.Errorf("decoding the response from %s %s: %w", method, path, decodeErr)
			}
			return nil
		}

		if attempt >= c.maxRetries || !shouldRetry(ctx, method, status, err) {
			if err != nil {
				return fmt.Errorf("calling %s %s: %w", method, path, err)
			}
			return newAPIError(method, path, status, responseBody)
		}

		wait := c.backoff(attempt, header)
		tflog.Debug(ctx, "Retrying Plerion API request", map[string]any{
			"method": method,
			"path":   path,
			"status": status,
			"wait":   wait.String(),
		})

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) send(ctx context.Context, method, endpoint string, payload []byte) ([]byte, int, http.Header, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.userAgent != "" {
		request.Header.Set("User-Agent", c.userAgent)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, response.Header, err
	}
	return responseBody, response.StatusCode, response.Header, nil
}

// shouldRetry retries throttling for every method, because a throttled request
// was never processed. Server errors and network failures, including the
// HTTP client's own timeout, are retried only for methods that are safe to
// repeat: POST could create a second check.
func shouldRetry(ctx context.Context, method string, status int, err error) bool {
	if status == http.StatusTooManyRequests {
		return true
	}

	idempotent := method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
	if !idempotent {
		return false
	}

	if err != nil {
		// The HTTP client's timeout also reports context.DeadlineExceeded, so
		// check the caller's context rather than the error.
		return ctx.Err() == nil
	}

	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func (c *Client) backoff(attempt int, header http.Header) time.Duration {
	if header != nil {
		if seconds, err := strconv.Atoi(header.Get("Retry-After")); err == nil && seconds >= 0 {
			return min(time.Duration(seconds)*time.Second, maxRetryWait)
		}
	}
	return min(c.retryWait<<attempt, maxRetryWait)
}

func newAPIError(method, path string, status int, body []byte) *APIError {
	apiErr := &APIError{StatusCode: status, Method: method, Path: path}

	var decoded struct {
		Errors  []FieldError `json:"errors"`
		Message string       `json:"message"`
	}
	if len(body) > maxErrorBodyBytes {
		body = body[:maxErrorBodyBytes]
	}
	if err := json.Unmarshal(body, &decoded); err == nil {
		apiErr.Errors = decoded.Errors
		apiErr.Message = decoded.Message
	} else if text := strings.TrimSpace(string(body)); text != "" {
		apiErr.Message = text
	}
	return apiErr
}
