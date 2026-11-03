package client

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOnboardingRoutesReadTheDataField(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/tenant/external-id" {
			t.Errorf("path = %q", request.URL.Path)
		}
		_, _ = response.Write([]byte(`{"data":{"externalId":"abc"}}`))
	})

	externalID, err := apiClient.GetExternalID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if externalID != "abc" {
		t.Errorf("externalId = %q, want abc", externalID)
	}
}

func TestOnboardingResponseWithoutDataIsAnError(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"tenantId":"t1"}`))
	})

	if _, err := apiClient.GetTenant(context.Background()); err == nil || !strings.Contains(err.Error(), "has no data") {
		t.Errorf("error = %v, want one about the missing data field", err)
	}
}

func TestHasCodeReadsTheErrorCode(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = response.Write([]byte(`{"errors":[{"code":"RoleNotAssumable","message":"Plerion could not assume the role yet."}]}`))
	})

	_, err := apiClient.CreateAWSIntegration(context.Background(), AWSIntegrationInput{RoleARN: "arn:aws:iam::111111111111:role/Plerion"})
	if !HasCode(err, "RoleNotAssumable") {
		t.Errorf("HasCode(%v, RoleNotAssumable) = false", err)
	}
	if HasCode(err, "IntegrationAlreadyExists") {
		t.Errorf("HasCode(%v, IntegrationAlreadyExists) = true", err)
	}
}

func TestCreatingAServiceAccountIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	apiClient := newTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.WriteHeader(http.StatusBadGateway)
	})

	_, err := apiClient.CreateAWSServiceAccount(context.Background(), AWSServiceAccountInput{})
	if err == nil || calls.Load() != 1 {
		t.Errorf("err = %v after %d calls, want one failed call", err, calls.Load())
	}
}

func TestRegionPathIsEscaped(t *testing.T) {
	apiClient := newTestClient(t, func(response http.ResponseWriter, request *http.Request) {
		if request.URL.EscapedPath() != "/v1/tenant/service-accounts/123456789012/regions/a%2Fb" {
			t.Errorf("path = %q", request.URL.EscapedPath())
		}
		_, _ = response.Write([]byte(`{"data":{}}`))
	})

	if _, err := apiClient.GetAWSServiceAccountRegion(context.Background(), "123456789012", "a/b"); err != nil {
		t.Fatal(err)
	}
}
