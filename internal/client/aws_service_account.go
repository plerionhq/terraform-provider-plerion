package client

import (
	"context"
	"net/http"
	"net/url"
)

// AWSServiceAccount is an AWS account that runs Plerion's workload scanners.
type AWSServiceAccount struct {
	AccountID                   string `json:"accountId"`
	Name                        string `json:"name"`
	ControlPlaneRoleARN         string `json:"controlPlaneRoleArn"`
	ApplianceInstanceProfileARN string `json:"applianceInstanceProfileArn"`
	Status                      string `json:"status"`
	Enabled                     bool   `json:"enabled"`
	AWSOrganizationID           string `json:"awsOrganizationId,omitempty"`
	TemplateVersion             string `json:"templateVersion,omitempty"`
}

// AWSServiceAccountInput is the body of a create request.
type AWSServiceAccountInput struct {
	ControlPlaneRoleARN         string `json:"controlPlaneRoleArn"`
	ApplianceInstanceProfileARN string `json:"applianceInstanceProfileArn"`
	Name                        string `json:"name,omitempty"`
	TemplateVersion             string `json:"templateVersion,omitempty"`
}

// AWSServiceAccountRegion is the network a service account's scanners run in.
type AWSServiceAccountRegion struct {
	AccountID        string `json:"accountId"`
	Region           string `json:"region"`
	VPCID            string `json:"vpcId"`
	SubnetID         string `json:"subnetId"`
	SecurityGroupID  string `json:"securityGroupId"`
	QueueURL         string `json:"queueUrl"`
	MaxInstanceLimit int64  `json:"maxInstanceLimit"`
	Enabled          bool   `json:"enabled"`
	TemplateVersion  string `json:"templateVersion,omitempty"`
}

// AWSServiceAccountRegionInput is the body of a PUT request. It never carries
// `enabled`: turning a region on or off stays in the Plerion console.
type AWSServiceAccountRegionInput struct {
	VPCID            string `json:"vpcId"`
	SubnetID         string `json:"subnetId"`
	SecurityGroupID  string `json:"securityGroupId"`
	QueueURL         string `json:"queueUrl"`
	MaxInstanceLimit int64  `json:"maxInstanceLimit,omitempty"`
	TemplateVersion  string `json:"templateVersion,omitempty"`
}

const serviceAccountsPath = "/v1/tenant/service-accounts"

func serviceAccountPath(accountID string) string {
	return serviceAccountsPath + "/" + url.PathEscape(accountID)
}

func serviceAccountRegionPath(accountID, region string) string {
	return serviceAccountPath(accountID) + "/regions/" + url.PathEscape(region)
}

// CreateAWSServiceAccount registers a service account. The account id comes
// from the control-plane role, which Plerion assumes before it writes.
func (c *Client) CreateAWSServiceAccount(ctx context.Context, in AWSServiceAccountInput) (*AWSServiceAccount, error) {
	var out AWSServiceAccount
	if err := c.doData(ctx, http.MethodPost, serviceAccountsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAWSServiceAccount reads one service account.
func (c *Client) GetAWSServiceAccount(ctx context.Context, accountID string) (*AWSServiceAccount, error) {
	var out AWSServiceAccount
	if err := c.doData(ctx, http.MethodGet, serviceAccountPath(accountID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAWSServiceAccount sends only the fields that changed. A nil value
// clears an optional field.
func (c *Client) UpdateAWSServiceAccount(ctx context.Context, accountID string, changes map[string]any) (*AWSServiceAccount, error) {
	var out AWSServiceAccount
	if err := c.doData(ctx, http.MethodPatch, serviceAccountPath(accountID), changes, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAWSServiceAccount removes a service account.
func (c *Client) DeleteAWSServiceAccount(ctx context.Context, accountID string) error {
	return c.doData(ctx, http.MethodDelete, serviceAccountPath(accountID), nil, nil)
}

// PutAWSServiceAccountRegion creates or replaces a region's network settings.
// Plerion checks through the service account's role that the VPC, subnet,
// security group and queue belong to this tenant before it writes.
func (c *Client) PutAWSServiceAccountRegion(ctx context.Context, accountID, region string, in AWSServiceAccountRegionInput) (*AWSServiceAccountRegion, error) {
	var out AWSServiceAccountRegion
	if err := c.doData(ctx, http.MethodPut, serviceAccountRegionPath(accountID, region), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAWSServiceAccountRegion reads one region.
func (c *Client) GetAWSServiceAccountRegion(ctx context.Context, accountID, region string) (*AWSServiceAccountRegion, error) {
	var out AWSServiceAccountRegion
	if err := c.doData(ctx, http.MethodGet, serviceAccountRegionPath(accountID, region), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAWSServiceAccountRegion removes a region.
func (c *Client) DeleteAWSServiceAccountRegion(ctx context.Context, accountID, region string) error {
	return c.doData(ctx, http.MethodDelete, serviceAccountRegionPath(accountID, region), nil, nil)
}
