package client

import (
	"context"
	"net/http"
)

// Tenant is the tenant the API key belongs to.
type Tenant struct {
	TenantID       string `json:"tenantId"`
	OrganizationID string `json:"organizationId"`
	Name           string `json:"name"`
}

// GetTenant reads the tenant the API key belongs to.
func (c *Client) GetTenant(ctx context.Context) (*Tenant, error) {
	var tenant Tenant
	if err := c.doData(ctx, http.MethodGet, "/v1/tenant", nil, &tenant); err != nil {
		return nil, err
	}
	return &tenant, nil
}

// GetExternalID reads the tenant's external id, which AWS roles trust Plerion with.
func (c *Client) GetExternalID(ctx context.Context) (string, error) {
	var out struct {
		ExternalID string `json:"externalId"`
	}
	if err := c.doData(ctx, http.MethodGet, "/v1/tenant/external-id", nil, &out); err != nil {
		return "", err
	}
	return out.ExternalID, nil
}
