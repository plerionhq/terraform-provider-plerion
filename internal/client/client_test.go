package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	apiClient, err := New(Config{
		BaseURL:   server.URL,
		APIKey:    "secret-key",
		UserAgent: "terraform-provider-plerion/test",
		RetryWait: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}
	return apiClient
}

func TestNewRequiresAnAPIKey(t *testing.T) {
	if _, err := New(Config{BaseURL: "https://au.api.plerion.com", APIKey: "  "}); err == nil {
		t.Fatal("New() accepted a blank API key")
	}
}

func TestParseBaseURL(t *testing.T) {
	cases := []struct {
		input   string
		want    string
		wantErr string
	}{
		{input: "https://au.api.plerion.com", want: "https://au.api.plerion.com"},
		{input: "https://au.api.plerion.com/", want: "https://au.api.plerion.com"},
		{input: " https://us1.api.plerion.com ", want: "https://us1.api.plerion.com"},
		{input: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{input: "http://localhost:8080", want: "http://localhost:8080"},
		{input: "http://au.api.plerion.com", wantErr: "only accepts https"},
		{input: "au.api.plerion.com", wantErr: "not a valid URL"},
		{input: "ftp://au.api.plerion.com", wantErr: "must start with https://"},
		{input: "https://au.api.plerion.com/v1/tenant", wantErr: "must not include a path"},
		{input: "https://au.api.plerion.com?region=au", wantErr: "must not include a path"},
	}

	for _, testCase := range cases {
		t.Run(testCase.input, func(t *testing.T) {
			parsed, err := ParseBaseURL(testCase.input)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("ParseBaseURL(%q) error = %v, want it to contain %q", testCase.input, err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBaseURL(%q) returned an error: %v", testCase.input, err)
			}
			if parsed.String() != testCase.want {
				t.Fatalf("ParseBaseURL(%q) = %q, want %q", testCase.input, parsed.String(), testCase.want)
			}
		})
	}
}

func TestRequestsCarryAuthAndUserAgent(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Errorf("Authorization header = %q", got)
		}
		if got := request.Header.Get("User-Agent"); got != "terraform-provider-plerion/test" {
			t.Errorf("User-Agent header = %q", got)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type header = %q", got)
		}
		if request.URL.Path != "/v1/tenant/custom-checks" {
			t.Errorf("path = %q", request.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		if _, hasDescription := body["description"]; hasDescription {
			t.Error("request body includes description although it was not set")
		}
		if _, hasMeta := body["defaults"].(map[string]any)["meta"]; hasMeta {
			t.Error("request body includes defaults.meta although it was not set")
		}

		_, _ = response.Write([]byte(`{"customCheckId":"id-1","slug":"my-check","version":1,
			"defaults":{"severityLevel":"LOW","message":"m","informational":false,"meta":{"b":1,"a":2}}}`))
	})

	check, err := apiClient.CreateCustomCheck(context.Background(), CustomCheckInput{
		Slug:     "my-check",
		Title:    "My check",
		Target:   Target{AssetType: "AWS::S3::Bucket", Scope: "batch"},
		Defaults: Defaults{SeverityLevel: "LOW", Message: "m"},
		Type:     "rego",
		Body:     "package custom",
	})
	if err != nil {
		t.Fatalf("CreateCustomCheck() returned an error: %v", err)
	}
	if check.ID != "id-1" || check.Slug != "my-check" || check.Version != 1 {
		t.Fatalf("unexpected check: %+v", check)
	}
	if string(check.Defaults.Meta) != `{"b":1,"a":2}` {
		t.Fatalf("meta = %s, want the raw JSON", check.Defaults.Meta)
	}
}

func TestAPIErrorFormats(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantError  string
		wantDetail string
	}{
		{
			name:       "field errors",
			status:     http.StatusBadRequest,
			body:       `{"errors":[{"field":"slug","code":"InvalidBody","message":"must match pattern"},{"field":"body","code":"InvalidBody","message":"must have required property"}]}`,
			wantError:  "GET /v1/tenant/custom-checks/id-1 returned 400 Bad Request: slug: must match pattern; body: must have required property",
			wantDetail: "must match pattern; must have required property",
		},
		{
			name:       "message errors",
			status:     http.StatusNotFound,
			body:       `{"errors":[{"message":"Custom check not found"}]}`,
			wantError:  "returned 404 Not Found: Custom check not found",
			wantDetail: "Custom check not found",
		},
		{
			name:       "top-level message",
			status:     http.StatusUnauthorized,
			body:       `{"message":"Unauthorized"}`,
			wantError:  "returned 401 Unauthorized: Unauthorized",
			wantDetail: "Unauthorized",
		},
		{
			name:       "plain text",
			status:     http.StatusForbidden,
			body:       `Forbidden by gateway`,
			wantError:  "returned 403 Forbidden: Forbidden by gateway",
			wantDetail: "Forbidden by gateway",
		},
		{
			name:      "empty body",
			status:    http.StatusConflict,
			body:      ``,
			wantError: "returned 409 Conflict",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(testCase.status)
				_, _ = response.Write([]byte(testCase.body))
			})

			_, err := apiClient.GetCustomCheck(context.Background(), "id-1")
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want an *APIError", err)
			}
			if !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), testCase.wantError)
			}
			if apiErr.Detail() != testCase.wantDetail {
				t.Fatalf("Detail() = %q, want %q", apiErr.Detail(), testCase.wantDetail)
			}
		})
	}
}

func TestStatusHelpers(t *testing.T) {
	cases := map[int]func(error) bool{
		http.StatusNotFound:     IsNotFound,
		http.StatusConflict:     IsConflict,
		http.StatusUnauthorized: IsUnauthorized,
		http.StatusForbidden:    IsForbidden,
	}
	for status, helper := range cases {
		wrapped := errors.Join(errors.New("context"), &APIError{StatusCode: status})
		if !helper(wrapped) {
			t.Errorf("helper for %d did not match a wrapped APIError", status)
		}
		if helper(&APIError{StatusCode: http.StatusTeapot}) {
			t.Errorf("helper for %d matched a 418", status)
		}
		if helper(errors.New("plain")) {
			t.Errorf("helper for %d matched a plain error", status)
		}
	}
}

func TestThrottledRequestsAreRetried(t *testing.T) {
	var calls atomic.Int32
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			response.Header().Set("Retry-After", "0")
			response.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = response.Write([]byte(`{"customCheckId":"id-1"}`))
	})

	// A throttled POST was never processed, so retrying it is safe.
	if _, err := apiClient.CreateCustomCheck(context.Background(), CustomCheckInput{Slug: "a"}); err != nil {
		t.Fatalf("CreateCustomCheck() returned an error: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("server saw %d calls, want 3", calls.Load())
	}
}

func TestServerErrorsAreRetriedOnlyForIdempotentMethods(t *testing.T) {
	var calls atomic.Int32
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := apiClient.CreateCustomCheck(context.Background(), CustomCheckInput{Slug: "a"})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("POST: error = %v, calls = %d; want an error after exactly 1 call", err, calls.Load())
	}

	calls.Store(0)
	_, err = apiClient.GetCustomCheck(context.Background(), "id-1")
	if err == nil || calls.Load() != int32(defaultMaxRetries+1) {
		t.Fatalf("GET: error = %v, calls = %d; want an error after %d calls", err, calls.Load(), defaultMaxRetries+1)
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	var calls atomic.Int32
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.WriteHeader(http.StatusBadRequest)
	})

	if _, err := apiClient.GetCustomCheck(context.Background(), "id-1"); err == nil {
		t.Fatal("GetCustomCheck() did not return an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("server saw %d calls, want 1", calls.Load())
	}
}

func TestRetryStopsWhenContextIsCanceled(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Retry-After", "30")
		response.WriteHeader(http.StatusTooManyRequests)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := apiClient.GetCustomCheck(ctx, "id-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the client kept waiting after the context was canceled")
	}
}

func TestListAllFollowsCursors(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get("limit") != "200" || query.Get("assetType") != "AWS::S3::Bucket" || query.Get("scope") != "batch" {
			t.Errorf("unexpected query: %s", request.URL.RawQuery)
		}
		switch query.Get("cursor") {
		case "":
			_, _ = response.Write([]byte(`{"items":[{"slug":"one"},{"slug":"two"}],"nextCursor":"page-2"}`))
		case "page-2":
			_, _ = response.Write([]byte(`{"items":[{"slug":"three"}]}`))
		default:
			t.Errorf("unexpected cursor %q", query.Get("cursor"))
		}
	})

	checks, err := apiClient.ListAllCustomChecks(context.Background(), "AWS::S3::Bucket", "batch")
	if err != nil {
		t.Fatalf("ListAllCustomChecks() returned an error: %v", err)
	}
	var slugs []string
	for _, check := range checks {
		slugs = append(slugs, check.Slug)
	}
	if strings.Join(slugs, ",") != "one,two,three" {
		t.Fatalf("slugs = %v", slugs)
	}
}

func TestListAllStopsOnARepeatedCursor(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"items":[{"slug":"one"}],"nextCursor":"same"}`))
	})

	if _, err := apiClient.ListAllCustomChecks(context.Background(), "", ""); err == nil {
		t.Fatal("ListAllCustomChecks() did not stop on a repeated cursor")
	}
}

func TestFindCustomCheckBySlug(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"items":[{"customCheckId":"id-1","slug":"one"},{"customCheckId":"id-2","slug":"two"}]}`))
	})

	check, err := apiClient.FindCustomCheckBySlug(context.Background(), "two")
	if err != nil || check.ID != "id-2" {
		t.Fatalf("FindCustomCheckBySlug(two) = %+v, %v", check, err)
	}

	_, err = apiClient.FindCustomCheckBySlug(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Fatalf("FindCustomCheckBySlug(missing) error = %v, want a 404 APIError", err)
	}
}

func TestDeleteAcceptsAnEmptyBody(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			t.Errorf("method = %s", request.Method)
		}
		response.WriteHeader(http.StatusAccepted)
	})

	if err := apiClient.DeleteCustomCheck(context.Background(), "id-1"); err != nil {
		t.Fatalf("DeleteCustomCheck() returned an error: %v", err)
	}
}

func TestWaitForDryRunPollsUntilDone(t *testing.T) {
	var polls atomic.Int32
	apiClient := newTestClient(t, func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/tenant/custom-check-dry-runs/run-1" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if polls.Add(1) < 3 {
			_, _ = response.Write([]byte(`{"dryRunId":"run-1","status":"RUNNING"}`))
			return
		}
		_, _ = response.Write([]byte(`{"dryRunId":"run-1","status":"SUCCEEDED",
			"output":{"totalChecks":1,"succeededCount":1,"failedCount":0,"totalFindings":1},
			"findings":[{"id":"finding-1"}]}`))
	})

	dryRun, err := apiClient.WaitForCustomCheckDryRun(context.Background(), "run-1", time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForCustomCheckDryRun() returned an error: %v", err)
	}
	if dryRun.Status != DryRunSucceeded || dryRun.Output.TotalFindings != 1 || len(dryRun.Findings) != 1 {
		t.Fatalf("unexpected dry run: %+v", dryRun)
	}
	if polls.Load() != 3 {
		t.Fatalf("polled %d times, want 3", polls.Load())
	}
}

func TestWaitForDryRunHonorsTheContext(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"dryRunId":"run-1","status":"RUNNING"}`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := apiClient.WaitForCustomCheckDryRun(ctx, "run-1", 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestHTTPTimeoutsAreRetriedOnlyForIdempotentMethods(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond)
		}
		_, _ = response.Write([]byte(`{"customCheckId":"id-1"}`))
	}))
	t.Cleanup(server.Close)

	apiClient, err := New(Config{
		BaseURL:    server.URL,
		APIKey:     "secret-key",
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
		RetryWait:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}

	if _, err := apiClient.GetCustomCheck(context.Background(), "id-1"); err != nil {
		t.Fatalf("GET after a timeout returned an error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("GET: server saw %d calls, want 2", calls.Load())
	}

	calls.Store(0)
	if _, err := apiClient.CreateCustomCheck(context.Background(), CustomCheckInput{Slug: "a"}); err == nil {
		t.Fatal("POST after a timeout did not return an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST: server saw %d calls, want 1", calls.Load())
	}
}
