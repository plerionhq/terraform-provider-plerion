package client

import (
	"context"
	"net/http"
	"net/url"
)

// AWSIntegration is an AWS account connected to Plerion.
type AWSIntegration struct {
	IntegrationID               string `json:"integrationId"`
	AccountID                   string `json:"accountId"`
	Name                        string `json:"name"`
	RoleARN                     string `json:"roleArn"`
	EnvironmentID               string `json:"environmentId,omitempty"`
	InstanceProfileARN          string `json:"instanceProfileArn,omitempty"`
	CWPPKMSAccessMode           string `json:"cwppKMSAccessMode,omitempty"`
	ServiceAccountID            string `json:"serviceAccountId,omitempty"`
	ServiceAccountAccessRoleARN string `json:"serviceAccountAccessRoleArn,omitempty"`
	ServiceAccountType          string `json:"serviceAccountType,omitempty"`
	Status                      string `json:"status"`
}

// AWSIntegrationInput is the body of a create request.
type AWSIntegrationInput struct {
	RoleARN                     string `json:"roleArn"`
	Name                        string `json:"name,omitempty"`
	EnvironmentID               string `json:"environmentId,omitempty"`
	InstanceProfileARN          string `json:"instanceProfileArn,omitempty"`
	CWPPKMSAccessMode           string `json:"cwppKMSAccessMode,omitempty"`
	ServiceAccountID            string `json:"serviceAccountId,omitempty"`
	ServiceAccountAccessRoleARN string `json:"serviceAccountAccessRoleArn,omitempty"`
	ServiceAccountType          string `json:"serviceAccountType,omitempty"`
}

const awsIntegrationsPath = "/v1/tenant/integrations/aws"

func awsIntegrationPath(id string) string {
	return awsIntegrationsPath + "/" + url.PathEscape(id)
}

// CreateAWSIntegration registers an AWS account. Plerion assumes the role
// before it writes anything.
func (c *Client) CreateAWSIntegration(ctx context.Context, in AWSIntegrationInput) (*AWSIntegration, error) {
	var out AWSIntegration
	if err := c.doData(ctx, http.MethodPost, awsIntegrationsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAWSIntegration reads one AWS integration.
func (c *Client) GetAWSIntegration(ctx context.Context, id string) (*AWSIntegration, error) {
	var out AWSIntegration
	if err := c.doData(ctx, http.MethodGet, awsIntegrationPath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAWSIntegration sends only the fields that changed. A nil value clears
// an optional field.
func (c *Client) UpdateAWSIntegration(ctx context.Context, id string, changes map[string]any) (*AWSIntegration, error) {
	var out AWSIntegration
	if err := c.doData(ctx, http.MethodPatch, awsIntegrationPath(id), changes, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAWSIntegration starts removing an integration. Plerion finishes the
// removal in the background, so callers wait for GetAWSIntegration to 404.
func (c *Client) DeleteAWSIntegration(ctx context.Context, id string) error {
	return c.doData(ctx, http.MethodDelete, awsIntegrationPath(id), nil, nil)
}
